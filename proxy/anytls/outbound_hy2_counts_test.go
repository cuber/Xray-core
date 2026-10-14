package anytls_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/apernet/quic-go/quicvarint"
	hyproxy "github.com/xtls/xray-core/proxy/hysteria"
	hytransport "github.com/xtls/xray-core/transport/internet/hysteria"
)

// This independent server observes actual QUIC accepts, not UDP sockets or
// addresses. Core's native Hy2 client is unchanged; native hub tests remain
// separate. Auth/control HTTP3 streams do not count as Hy2 TCP business streams.
type hy2CountsPeer struct {
	t                                              *testing.T
	listener                                       *quic.Listener
	packet                                         net.PacketConn
	accepted, closed, streams, streamsClosed, auth atomic.Int64
	streamDone                                     chan struct{}
	acceptDone                                     chan struct{}
	serveWG, streamWG                              sync.WaitGroup
	mu                                             sync.Mutex
	stopping                                       bool
	connections                                    map[*quic.Conn]*http3.Server
	identities                                     map[string]bool
	once                                           sync.Once
}

func newHy2CountsPeer(t *testing.T, pair tls.Certificate, target string) *hy2CountsPeer {
	t.Helper()
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := quic.Listen(packet, &tls.Config{Certificates: []tls.Certificate{pair}, NextProtos: []string{http3.NextProtoH3}}, &quic.Config{EnableDatagrams: true, MaxIdleTimeout: 30 * time.Second})
	if err != nil {
		packet.Close()
		t.Fatal(err)
	}
	p := &hy2CountsPeer{t: t, listener: listener, packet: packet, streamDone: make(chan struct{}, 16), acceptDone: make(chan struct{}), connections: make(map[*quic.Conn]*http3.Server), identities: make(map[string]bool)}
	t.Cleanup(p.stop)
	go func() {
		defer close(p.acceptDone)
		for {
			conn, err := listener.Accept(context.Background())
			if err != nil {
				return
			}
			id := p.accepted.Add(1)
			var authenticated atomic.Bool
			server := &http3.Server{EnableDatagrams: true}
			server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Host != hytransport.URLHost || r.URL.Path != hytransport.URLPath || r.Header.Get(hytransport.RequestHeaderAuth) != "hop-secret" {
					t.Error("unexpected Hy2 authentication request")
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if authenticated.CompareAndSwap(false, true) {
					p.auth.Add(1)
				}
				w.Header().Set(hytransport.ResponseHeaderUDPEnabled, "false")
				w.Header().Set(hytransport.CommonHeaderCCRX, "0")
				w.WriteHeader(hytransport.StatusAuthOK)
			})
			server.StreamDispatcher = func(frame http3.FrameType, stream *quic.Stream, err error) (bool, error) {
				if err != nil || frame != hytransport.FrameTypeTCPRequest {
					return false, err
				}
				if !authenticated.Load() {
					return false, fmt.Errorf("unauthenticated Hy2 TCP stream")
				}
				key := fmt.Sprintf("%d/%d", id, stream.StreamID())
				p.mu.Lock()
				if p.stopping {
					p.mu.Unlock()
					return false, net.ErrClosed
				}
				if p.identities[key] {
					p.mu.Unlock()
					t.Errorf("duplicate QUIC connection/stream %s", key)
					return false, fmt.Errorf("duplicate stream")
				}
				p.identities[key] = true
				p.streamWG.Add(1)
				p.streams.Add(1)
				p.mu.Unlock()
				go func() {
					defer p.streamWG.Done()
					defer func() { p.streamsClosed.Add(1); p.streamDone <- struct{}{} }()
					defer stream.CancelRead(0)
					defer stream.Close()
					stream.SetDeadline(time.Now().Add(5 * time.Second))
					actual, err := quicvarint.Read(quicvarint.NewReader(stream))
					if err != nil || actual != hytransport.FrameTypeTCPRequest {
						t.Errorf("Hy2 frame=%d err=%v", actual, err)
						return
					}
					address, err := hyproxy.ReadTCPRequest(stream)
					if err != nil || address != target {
						t.Errorf("Hy2 target=%q want=%q err=%v", address, target, err)
						return
					}
					upstream, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(conn.Context(), "tcp", address)
					if err != nil {
						t.Error(err)
						return
					}
					defer upstream.Close()
					if err := hyproxy.WriteTCPResponse(stream, true, ""); err != nil {
						t.Error(err)
						return
					}
					stream.SetDeadline(time.Time{})
					done := make(chan struct{}, 2)
					go func() { io.Copy(upstream, stream); done <- struct{}{} }()
					go func() { io.Copy(stream, upstream); done <- struct{}{} }()
					<-done
					stream.CancelRead(0)
					stream.Close()
					upstream.Close()
					<-done
				}()
				return true, nil
			}
			p.mu.Lock()
			p.connections[conn] = server
			p.mu.Unlock()
			p.serveWG.Add(1)
			go func() {
				defer p.serveWG.Done()
				server.ServeQUICConn(conn)
				conn.CloseWithError(0, "fixture serve ended")
				<-conn.Context().Done()
				p.closed.Add(1)
			}()
		}
	}()
	return p
}

func (p *hy2CountsPeer) stop() {
	p.once.Do(func() {
		p.listener.Close()
		obCountsWait(p.t, p.acceptDone, "Hy2 QUIC accept loop")
		p.mu.Lock()
		p.stopping = true
		pairs := make(map[*quic.Conn]*http3.Server, len(p.connections))
		for c, s := range p.connections {
			pairs[c] = s
		}
		p.mu.Unlock()
		for c, s := range pairs {
			c.CloseWithError(0, "fixture shutdown")
			s.Close()
		}
		p.packet.Close()
		done := make(chan struct{})
		go func() { p.serveWG.Wait(); p.streamWG.Wait(); close(done) }()
		obCountsWait(p.t, done, "Hy2 ServeQUICConn and relay workers")
	})
}

func (p *hy2CountsPeer) assertCounts(streams int64) {
	p.t.Helper()
	got := [5]int64{p.accepted.Load(), p.auth.Load(), p.closed.Load(), p.streams.Load(), p.streamsClosed.Load()}
	if want := [5]int64{1, 1, 1, streams, streams}; got != want {
		p.t.Fatalf("QUIC accepted/auth/closed/TCP streams/closed=%v want=%v", got, want)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.connections) != 1 || int64(len(p.identities)) != streams {
		p.t.Fatal("QUIC connection/stream identity mismatch")
	}
}

func TestAnyTLSOutboundHy2RelayLayerCounts(t *testing.T) {
	for _, mode := range []string{"proxySettings", "dialerProxy"} {
		t.Run(mode, func(t *testing.T) { runAnyTLSRelayCounts(t, mode, "hysteria") })
	}
}
