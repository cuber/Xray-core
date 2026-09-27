package anytls_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/xtls/xray-core/app/observatory/burst"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/proxy/anytls"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet/tagged"
)

// This alias only observes completion: Core still routes and executes the real
// native handler, including its dialer, TLS verification, pool and copy workers.
type obCountsOutbound struct {
	outbound.Handler
	done    chan struct{}
	uploads chan chan obCountsRequest
}

func (*obCountsOutbound) Tag() string  { return "counted-client" }
func (*obCountsOutbound) Start() error { return nil }
func (*obCountsOutbound) Close() error { return nil } // The original registration owns it.
func (h *obCountsOutbound) Dispatch(ctx context.Context, link *transport.Link) {
	defer func() { h.done <- struct{}{} }()
	if h.uploads != nil {
		completed := make(chan obCountsRequest, 16)
		// This fixture admits one stream at a time, including peer/dispatch
		// joins before the next stream. Pair its peer with this reader only.
		select {
		case h.uploads <- completed:
		case <-ctx.Done():
			return
		}
		defer close(completed)
		link = &transport.Link{Reader: &obCountsUploadReader{Reader: link.Reader, completed: completed}, Writer: link.Writer}
	}
	h.Handler.Dispatch(ctx, link)
}

type obCountsRequest struct {
	method, uri string
}

type obCountsUploadReader struct {
	buf.Reader
	pending   []byte
	completed chan obCountsRequest
}

func (r *obCountsUploadReader) Interrupt() { common.Interrupt(r.Reader) }

func (r *obCountsUploadReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	// Copy calls us again only after the previous WriteMultiBuffer returned,
	// including padding, watcher join and write unlock. Receiving an echo or
	// observing physical Write enter is not an equivalent completion barrier.
	for len(r.pending) != 0 {
		input := bytes.NewReader(r.pending)
		reader := bufio.NewReader(input)
		req, err := http.ReadRequest(reader)
		if err != nil {
			break // An HTTP header may span any number of Core buffers.
		}
		_, err = io.Copy(io.Discard, req.Body)
		req.Body.Close()
		if err != nil {
			break // Do not acknowledge a partial body or chunked trailer.
		}
		consumed := len(r.pending) - input.Len() - reader.Buffered()
		if consumed <= 0 {
			return nil, fmt.Errorf("HTTP upload parser made no progress")
		}
		select {
		case r.completed <- obCountsRequest{req.Method, req.URL.RequestURI()}:
		default:
			return nil, fmt.Errorf("HTTP upload completion queue overflow")
		}
		r.pending = r.pending[consumed:]
	}
	mb, err := r.Reader.ReadMultiBuffer()
	for _, b := range mb {
		r.pending = append(r.pending, b.Bytes()...)
	}
	if len(r.pending) > 64*1024 {
		buf.ReleaseMulti(mb)
		return nil, fmt.Errorf("HTTP upload exceeds fixture limit")
	}
	return mb, err
}

type obCountsScriptReader struct {
	chunks      []string
	interrupted bool
}

func (r *obCountsScriptReader) Interrupt() { r.interrupted = true }
func (r *obCountsScriptReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	if len(r.chunks) == 0 {
		return nil, io.EOF
	}
	b := buf.New()
	b.Write([]byte(r.chunks[0]))
	r.chunks = r.chunks[1:]
	return buf.MultiBuffer{b}, nil
}

func TestOBUploadCompletionBarrier(t *testing.T) {
	for _, request := range []string{
		"GET /ordinary?ob=counts HTTP/1.1\r\nHost: counts.test\r\n\r\n",
		"POST /ordinary?ob=counts HTTP/1.1\r\nHost: counts.test\r\nContent-Length: 4\r\n\r\ndata",
		"POST /ordinary?ob=counts HTTP/1.1\r\nHost: counts.test\r\nTransfer-Encoding: chunked\r\n\r\n4\r\ndata\r\n0\r\n\r\n",
	} {
		for split := 1; split < len(request); split++ {
			source := &obCountsScriptReader{chunks: []string{request[:split], request[split:]}}
			completed := make(chan obCountsRequest, 4)
			r := &obCountsUploadReader{Reader: source, completed: completed}
			for range 2 {
				mb, err := r.ReadMultiBuffer()
				buf.ReleaseMulti(mb)
				if err != nil || len(completed) != 0 {
					t.Fatalf("premature completion at split %d: err=%v completions=%d", split, err, len(completed))
				}
			}
			mb, err := r.ReadMultiBuffer()
			buf.ReleaseMulti(mb)
			if err != io.EOF || len(completed) != 1 {
				t.Fatalf("missing next-read completion at split %d: %v", split, err)
			}
			wantMethod := strings.SplitN(request, " ", 2)[0]
			if got := <-completed; got != (obCountsRequest{wantMethod, "/ordinary?ob=counts"}) {
				t.Fatalf("wrong request identity: %+v", got)
			}
			r.Interrupt()
			if !source.interrupted {
				t.Fatal("reader erased Interrupt")
			}
		}
	}
	completed := make(chan obCountsRequest, 4)
	r := &obCountsUploadReader{Reader: &obCountsScriptReader{chunks: []string{
		"GET /one HTTP/1.1\r\nHost: counts.test\r\n\r\nGET /two HTTP/1.1\r\nHost: counts.test\r\n\r\n",
	}}, completed: completed}
	mb, err := r.ReadMultiBuffer()
	buf.ReleaseMulti(mb)
	if err != nil || len(completed) != 0 {
		t.Fatal("coalesced requests acknowledged before write completion")
	}
	mb, err = r.ReadMultiBuffer()
	buf.ReleaseMulti(mb)
	if err != io.EOF || len(completed) != 2 {
		t.Fatal("coalesced request boundaries lost", err, len(completed))
	}
	for _, uri := range []string{"/one", "/two"} {
		if got := <-completed; got != (obCountsRequest{"GET", uri}) {
			t.Fatal("coalesced requests reordered", got)
		}
	}
}

type obCountsHTTP struct {
	t        *testing.T
	requests atomic.Int64
	uploads  chan chan obCountsRequest
}

func (h *obCountsHTTP) NewConnectionEx(ctx context.Context, conn net.Conn, _, target M.Socksaddr, _ N.CloseHandlerFunc) {
	defer conn.Close()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	var completed chan obCountsRequest
	select {
	case completed = <-h.uploads:
	case <-ctx.Done():
		return
	case <-deadline.C:
		h.t.Error("missing upload reader for HTTP peer")
		return
	}
	if target.Fqdn != "counts.test" || target.Port != 8080 {
		h.t.Errorf("unexpected remote HTTP destination: %v", target)
		return
	}
	if err := N.ReportHandshakeSuccess(conn); err != nil {
		h.t.Error(err)
		return
	}
	reader := bufio.NewReader(conn)
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			if err != io.EOF && !strings.Contains(err.Error(), "closed") {
				h.t.Errorf("HTTP peer read: %v", err)
			}
			return
		}
		req.Body.Close()
		h.requests.Add(1)
		if req.Method != http.MethodGet || (req.URL.Path != "/ordinary" && req.URL.Path != "/keepalive") || req.URL.RawQuery != "ob=counts" {
			h.t.Errorf("unexpected request: %s %s", req.Method, req.URL)
			return
		}
		deadline.Reset(5 * time.Second)
		select {
		case got, ok := <-completed:
			if !ok || got != (obCountsRequest{req.Method, req.URL.RequestURI()}) {
				h.t.Errorf("HTTP upload not completed: got=%+v open=%v", got, ok)
				return
			}
		case <-ctx.Done():
			return
		case <-deadline.C:
			h.t.Error("HTTP upload completion barrier timed out")
			return
		}
		deadline.Stop()
		closeHTTP := req.Close || req.URL.Path == "/ordinary"
		connection := "keep-alive"
		if closeHTTP {
			connection = "close"
		}
		if _, err := fmt.Fprintf(conn, "HTTP/1.1 204 No Content\r\nConnection: %s\r\n\r\n", connection); err != nil {
			h.t.Error(err)
			return
		}
		if closeHTTP {
			return
		}
	}
}

func obCountsWait(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out joining %s", label)
	}
}

func TestAnyTLSOutboundOBResourceCounts(t *testing.T) {
	for _, endpoint := range []string{"ordinary", "keepalive"} {
		for _, keep := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/keepAlive=%v", endpoint, keep), func(t *testing.T) {
				f, config := outboundFixture(t, "")
				certs := f.inbound["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)["certificates"].([]any)
				cert := certs[0].(map[string]any)
				pair, err := tls.X509KeyPair([]byte(strings.Join(cert["certificate"].([]string), "\n")), []byte(strings.Join(cert["key"].([]string), "\n")))
				if err != nil {
					t.Fatal(err)
				}
				listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12})
				if err != nil {
					t.Fatal(err)
				}
				uploads := make(chan chan obCountsRequest, 16)
				peer := &obCountsHTTP{t: t, uploads: uploads}
				var streams, sessions, closedStreams, closedSessions atomic.Int64
				streamDone := make(chan struct{}, 64)
				service, err := engine.NewService("remote-secret", engine.ServiceOptions{
					Handler:       peer,
					SessionReady:  func(context.Context) { sessions.Add(1) },
					SessionClosed: func(context.Context) { closedSessions.Add(1) },
					StreamOpen:    func(context.Context) bool { streams.Add(1); return true },
					StreamClose:   func(context.Context) { closedStreams.Add(1); streamDone <- struct{}{} },
				})
				if err != nil {
					listener.Close()
					t.Fatal(err)
				}
				var workers sync.WaitGroup
				var sockets sync.Map
				accepted := atomic.Int64{}
				acceptDone := make(chan struct{})
				go func() {
					defer close(acceptDone)
					for {
						conn, err := listener.Accept()
						if err != nil {
							return
						}
						accepted.Add(1)
						sockets.Store(conn, struct{}{})
						workers.Add(1)
						go func() {
							defer workers.Done()
							defer sockets.Delete(conn)
							defer conn.Close()
							if err := service.NewConnection(context.Background(), conn, M.Socksaddr{}, nil); err != nil {
								t.Errorf("AnyTLS peer: %v", err)
							}
						}()
					}
				}()
				var cleanup sync.Once
				stop := func() {
					cleanup.Do(func() {
						listener.Close()
						obCountsWait(t, acceptDone, "TLS accept loop")
						if err := f.instance.Close(); err != nil {
							t.Error(err)
						}
						joined := make(chan struct{})
						go func() { workers.Wait(); close(joined) }()
						select {
						case <-joined:
						case <-time.After(5 * time.Second):
							t.Error("Core shutdown did not release TLS peer workers")
							sockets.Range(func(key, _ any) bool { key.(net.Conn).Close(); return true })
							obCountsWait(t, joined, "forced peer cleanup")
						}
					})
				}
				t.Cleanup(stop)
				ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), core.XrayKey(1), f.instance), 30*time.Second)
				defer cancel()
				if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
					t.Fatal(err)
				}
				value, err := config.ProxySettings.GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				value.(*anytls.ClientConfig).Server.Port = uint32(listener.Addr().(*net.TCPAddr).Port)
				config.ProxySettings = serial.ToTypedMessage(value)
				if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
					t.Fatal(err)
				}
				manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
				observed := &obCountsOutbound{Handler: manager.GetHandler("client"), done: make(chan struct{}, 64), uploads: uploads}
				if err := manager.AddHandler(ctx, observed); err != nil {
					t.Fatal(err)
				}
				dispatcher := f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher)
				joinStream := func() {
					obCountsWait(t, observed.done, "native outbound Dispatch")
					obCountsWait(t, streamDone, "peer stream handler")
				}
				assertCounts := func(requests, logical, closed int64) {
					t.Helper()
					if got := [6]int64{peer.requests.Load(), streams.Load(), sessions.Load(), closedStreams.Load(), closedSessions.Load(), accepted.Load()}; got != [6]int64{requests, logical, 1, closed, 0, 1} {
						t.Fatalf("HTTP/logical/TLS/closed-stream/closed-session/accepted = %v; want %v", got, [6]int64{requests, logical, 1, closed, 0, 1})
					}
				}
				url := "http://counts.test:8080/" + endpoint + "?ob=counts"
				tr := &http.Transport{DisableKeepAlives: !keep, DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
					dest, err := xnet.ParseDestination(network + ":" + addr)
					if err != nil {
						return nil, err
					}
					return tagged.Dialer(ctx, dispatcher, dest, observed.Tag())
				}}
				client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
				defer client.CloseIdleConnections()
				reused := keep && endpoint == "keepalive"
				var logical int64
				for i := int64(1); i <= 3; i++ {
					resp, err := client.Get(url)
					if err != nil {
						t.Fatal(err)
					}
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if resp.StatusCode != http.StatusNoContent {
						t.Fatal(resp.Status)
					}
					if !reused {
						logical++
						joinStream()
						assertCounts(i, logical, logical)
					} else {
						logical = 1
						assertCounts(i, logical, 0)
					}
				}
				client.CloseIdleConnections()
				if reused {
					joinStream()
				}
				assertCounts(3, logical, logical)

				// Check owns a new HTTP client on every call, even with KeepAlive.
				// All three fresh logical streams must reuse the surviving native pool.
				ping := burst.NewHealthPing(ctx, dispatcher, &burst.HealthPingConfig{
					Destination: url, HttpMethod: http.MethodGet, KeepAlive: keep,
					Timeout: int64(5 * time.Second), Interval: int64(time.Second), SamplingCount: 1,
				})
				for i := int64(1); i <= 3; i++ {
					if err := ping.Check([]string{observed.Tag()}); err != nil {
						t.Fatal(err)
					}
					if result := ping.Results[observed.Tag()]; result == nil || !result.Get().Alive {
						t.Fatal("real OB sample was not healthy")
					}
					joinStream()
					assertCounts(3+i, logical+i, logical+i)
				}
				stop()
				if closedSessions.Load() != 1 || closedStreams.Load() != logical+3 {
					t.Fatalf("shutdown leaked resources: sessions=%d/%d streams=%d/%d", closedSessions.Load(), sessions.Load(), closedStreams.Load(), streams.Load())
				}
				sockets.Range(func(_, _ any) bool { t.Error("owned TLS socket survived joined shutdown"); return true })
				t.Logf("HTTP=6 logical=%d TLS=1; HTTP idle close preserved pool; shutdown joined all peer workers", logical+3)
			})
		}
	}
}
