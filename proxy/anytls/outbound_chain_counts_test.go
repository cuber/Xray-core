package anytls_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
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
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/tagged"
)

type chainCountsDiagnosticDialer struct {
	internet.DefaultSystemDialer
	t *testing.T
}

func (d *chainCountsDiagnosticDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, opts *internet.SocketConfig) (net.Conn, error) {
	c, err := d.DefaultSystemDialer.Dial(ctx, source, dest, opts)
	if err != nil {
		return nil, err
	}
	return &chainCountsDiagnosticConn{Conn: c, t: d.t, born: time.Now()}, nil
}

type chainCountsDiagnosticConn struct {
	net.Conn
	t      *testing.T
	born   time.Time
	writes atomic.Int64
}

func (c *chainCountsDiagnosticConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	defer c.writes.Add(-1)
	return c.Conn.Write(p)
}
func (c *chainCountsDiagnosticConn) Close() error {
	c.t.Logf("chain physical close remote=%v age=%v activeWrites=%d stack=%s", c.RemoteAddr(), time.Since(c.born), c.writes.Load(), debug.Stack())
	return c.Conn.Close()
}

type chainCountsAlias struct {
	outbound.Handler
	tag    string
	opened atomic.Int64
	closed atomic.Int64
	done   chan struct{}
	ledger *chainCountsLedger
}

func (h *chainCountsAlias) Tag() string { return h.tag }
func (*chainCountsAlias) Start() error  { return nil }
func (*chainCountsAlias) Close() error  { return nil } // Original registration owns the native handler.
func (h *chainCountsAlias) Dispatch(ctx context.Context, link *transport.Link) {
	h.opened.Add(1)
	if h.ledger != nil {
		link = &transport.Link{Reader: &chainCountsUpload{Reader: link.Reader, ledger: h.ledger}, Writer: link.Writer}
	}
	defer func() { h.closed.Add(1); h.done <- struct{}{} }()
	h.Handler.Dispatch(ctx, link)
}

type chainCountsReceipt struct {
	wire     []byte
	uploaded chan struct{}
	once     sync.Once
	count    int
}

type chainCountsLedger struct {
	mu       sync.Mutex
	next     uint64
	requests map[uint64]*chainCountsReceipt
}

func (l *chainCountsLedger) register(payload string) []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	wire := make([]byte, 12+len(payload))
	binary.BigEndian.PutUint32(wire, uint32(len(payload)))
	binary.BigEndian.PutUint64(wire[4:], l.next)
	copy(wire[12:], payload)
	l.requests[l.next] = &chainCountsReceipt{wire: wire, uploaded: make(chan struct{})}
	return wire
}

// A following read proves the previous complete framed upload has returned
// from the native writer. The real destination does not echo before this barrier.
type chainCountsUpload struct {
	buf.Reader
	ledger  *chainCountsLedger
	pending []byte
}

func (r *chainCountsUpload) Interrupt() { common.Interrupt(r.Reader) }
func (r *chainCountsUpload) ReadMultiBuffer() (buf.MultiBuffer, error) {
	for len(r.pending) >= 12 {
		n := 12 + int(binary.BigEndian.Uint32(r.pending))
		if n > len(r.pending) {
			break
		}
		id := binary.BigEndian.Uint64(r.pending[4:])
		r.ledger.mu.Lock()
		receipt := r.ledger.requests[id]
		r.ledger.mu.Unlock()
		if receipt != nil {
			receipt.once.Do(func() { close(receipt.uploaded) })
		}
		r.pending = r.pending[n:]
	}
	mb, err := r.Reader.ReadMultiBuffer()
	for _, b := range mb {
		r.pending = append(r.pending, b.Bytes()...)
	}
	return mb, err
}

func chainCountsDestination(t *testing.T, ledger *chainCountsLedger) (net.Listener, *atomic.Int64, func(bool)) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int64
	var completed atomic.Int64
	var workers sync.WaitGroup
	var sockets sync.Map
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			sockets.Store(conn, true)
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer completed.Add(1)
				defer sockets.Delete(conn)
				defer conn.Close()
				for {
					header := make([]byte, 12)
					_, err := io.ReadFull(conn, header)
					if err == io.EOF {
						return
					}
					if err != nil {
						t.Errorf("destination header: %v", err)
						return
					}
					n := int(binary.BigEndian.Uint32(header))
					if n > 1<<20 {
						t.Errorf("invalid destination size %d", n)
						return
					}
					wire := append(header, make([]byte, n)...)
					if _, err := io.ReadFull(conn, wire[12:]); err != nil {
						t.Error(err)
						return
					}
					id := binary.BigEndian.Uint64(header[4:])
					ledger.mu.Lock()
					r := ledger.requests[id]
					valid := r != nil && bytes.Equal(r.wire, wire)
					if valid {
						r.count++
					}
					ledger.mu.Unlock()
					if !valid {
						t.Errorf("unexpected destination receipt id=%d", id)
						return
					}
					select {
					case <-r.uploaded:
					case <-time.After(5 * time.Second):
						t.Errorf("upload barrier id=%d", id)
						return
					}
					if _, err := conn.Write(wire); err != nil {
						t.Error(err)
						return
					}
				}
			}()
		}
	}()
	var once sync.Once
	stop := func(force bool) {
		once.Do(func() {
			ln.Close()
			obCountsWait(t, acceptDone, "destination accept")
			closeSockets := func() { sockets.Range(func(key, _ any) bool { key.(net.Conn).Close(); return true }) }
			if force {
				closeSockets()
			}
			done := make(chan struct{})
			go func() { workers.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("destination workers survived native shutdown")
				closeSockets()
				obCountsWait(t, done, "forced destination workers")
			}
			if completed.Load() != accepted.Load() {
				t.Errorf("destination accepts/closed=%d/%d", accepted.Load(), completed.Load())
			}
			sockets.Range(func(_, _ any) bool { t.Error("retained destination socket"); return true })
		})
	}
	t.Cleanup(func() { stop(true) })
	return ln, &accepted, stop
}

type chainCountsHandler func(context.Context, net.Conn, M.Socksaddr)

func (h chainCountsHandler) NewConnectionEx(ctx context.Context, conn net.Conn, _, target M.Socksaddr, _ N.CloseHandlerFunc) {
	h(ctx, conn, target)
}

// Each peer counts its own authenticated TLS sessions and protocol SYN/FIN
// lifetime. Inner TLS records travel inside outer AnyTLS streams, not directly
// from a substituted client dialer. Listener accepts are an independent check.
type chainCountsPeer struct {
	listener                                     net.Listener
	accepted, physical, logical, ended, released atomic.Int64
	readBytes, writtenBytes                      atomic.Int64
	streamDone, sessionDone, acceptDone          chan struct{}
	sockets                                      sync.Map
	workers                                      sync.WaitGroup
	stop                                         sync.Once
}

// Wrap the accepted *tls.Conn, not its underlying TCP socket. These counts
// include AnyTLS authentication, padding and frames but exclude this layer's
// TLS handshake/record overhead. Outer framing legitimately carries inner TLS
// records as opaque payload; the inner counter sees only decrypted inner bytes.
type chainCountsPlaintextConn struct {
	net.Conn
	peer *chainCountsPeer
}

func (c *chainCountsPlaintextConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.peer.readBytes.Add(int64(n))
	return n, err
}

func (c *chainCountsPlaintextConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.peer.writtenBytes.Add(int64(n))
	return n, err
}

func chainCountsAssertWire(t *testing.T, ctx context.Context, f *integrationFixture, inner, outer *chainCountsPeer) {
	t.Helper()
	peerBytes := func() [4]int64 {
		return [4]int64{inner.readBytes.Load(), inner.writtenBytes.Load(), outer.readBytes.Load(), outer.writtenBytes.Load()}
	}
	// Dispatch/stream completion does not join the pooled session's read loop:
	// FIN, padding and settings can still be consumed asynchronously. Require
	// exact equality and an unchanged observation across three sampled reads.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var previous, got, want [4]int64
	stable := 0
	for {
		before := peerBytes()
		for i, name := range []string{
			"outbound>>>client>>>traffic>>>uplink", "outbound>>>client>>>traffic>>>downlink",
			"outbound>>>hop>>>traffic>>>uplink", "outbound>>>hop>>>traffic>>>downlink",
		} {
			result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: name})
			if err != nil {
				t.Fatal(err)
			}
			got[i] = result.GetStat().GetValue()
		}
		want = peerBytes()
		if before == want && got == want && got == previous && got[0] > 0 && got[1] > 0 && got[2] > 0 && got[3] > 0 {
			stable++
		} else {
			stable = 0
		}
		if stable == 3 {
			t.Logf("exact post-TLS framing bytes: client up/down=%d/%d, hop up/down=%d/%d", got[0], got[1], got[2], got[3])
			return
		}
		previous = got
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("post-TLS wire mismatch [client up/down, hop up/down]: gRPC=%v peer=%v", got, want)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func newChainCountsPeer(t *testing.T, pair tls.Certificate, password string, handler chainCountsHandler) *chainCountsPeer {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	p := &chainCountsPeer{listener: listener, streamDone: make(chan struct{}, 64), sessionDone: make(chan struct{}, 64), acceptDone: make(chan struct{})}
	service, err := engine.NewService(password, engine.ServiceOptions{
		Handler:       handler,
		SessionReady:  func(context.Context) { p.physical.Add(1) },
		SessionClosed: func(context.Context) { p.ended.Add(1); p.sessionDone <- struct{}{} },
		StreamOpen:    func(context.Context) bool { p.logical.Add(1); return true },
		StreamClose:   func(context.Context) { p.released.Add(1); p.streamDone <- struct{}{} },
	})
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	go func() {
		defer close(p.acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			p.accepted.Add(1)
			p.sockets.Store(conn, struct{}{})
			p.workers.Add(1)
			go func() {
				defer p.workers.Done()
				defer p.sockets.Delete(conn)
				defer conn.Close()
				measured := &chainCountsPlaintextConn{Conn: conn, peer: p}
				if err := service.NewConnection(context.Background(), measured, M.Socksaddr{}, nil); err != nil {
					t.Errorf("counted TLS peer: %v", err)
				}
			}()
		}
	}()
	t.Cleanup(func() { p.close(t, true) })
	return p
}

func (p *chainCountsPeer) close(t *testing.T, force bool) {
	t.Helper()
	p.stop.Do(func() {
		p.listener.Close()
		obCountsWait(t, p.acceptDone, "layer accept loop")
		if force {
			p.sockets.Range(func(key, _ any) bool { key.(net.Conn).Close(); return true })
		}
		joined := make(chan struct{})
		go func() { p.workers.Wait(); close(joined) }()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("layer retained peer workers after native pool shutdown")
			p.sockets.Range(func(key, _ any) bool { key.(net.Conn).Close(); return true })
			obCountsWait(t, joined, "forced peer worker cleanup")
		}
		p.sockets.Range(func(_, _ any) bool { t.Error("layer retained an owned TLS socket"); return true })
	})
}

func TestAnyTLSOutboundChainLayerCounts(t *testing.T) {
	for _, mode := range []string{"proxySettings", "dialerProxy"} {
		t.Run(mode, func(t *testing.T) {
			var diagnostic *chainCountsDiagnosticDialer
			if os.Getenv("ANYTLS_REUSE_DIAGNOSTIC") == "1" {
				diagnostic = &chainCountsDiagnosticDialer{t: t}
				internet.UseAlternativeSystemDialer(diagnostic)
				t.Cleanup(func() { internet.UseAlternativeSystemDialer(nil) })
			}
			f, config := outboundFixture(t, mode+"/anytls", func(config map[string]any) {
				// Real authenticated ingress uses the same completion-only alias as
				// the dispatcher-driven workload; its native handler is unchanged.
				rules := config["routing"].(map[string]any)["rules"].([]any)
				rules[0].(map[string]any)["outboundTag"] = "counts-client"
			})
			cert := f.inbound["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)["certificates"].([]any)[0].(map[string]any)
			pair, err := tls.X509KeyPair([]byte(strings.Join(cert["certificate"].([]string), "\n")), []byte(strings.Join(cert["key"].([]string), "\n")))
			if err != nil {
				t.Fatal(err)
			}
			ledger := &chainCountsLedger{requests: make(map[uint64]*chainCountsReceipt)}
			backend, destinations, stopBackend := chainCountsDestination(t, ledger)
			inner := newChainCountsPeer(t, pair, "remote-secret", func(ctx context.Context, conn net.Conn, target M.Socksaddr) {
				defer conn.Close()
				if target.Fqdn != "counts.test" || target.Port != 80 {
					t.Errorf("inner destination changed: %v", target)
					return
				}
				if err := N.ReportHandshakeSuccess(conn); err != nil {
					t.Error(err)
					return
				}
				upstream, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", backend.Addr().String())
				if err != nil {
					t.Error(err)
					return
				}
				defer upstream.Close()
				done := make(chan struct{}, 2)
				copyAndClose := func(dst, src net.Conn) {
					io.Copy(dst, src)
					conn.Close()
					upstream.Close()
					done <- struct{}{}
				}
				go copyAndClose(upstream, conn)
				go copyAndClose(conn, upstream)
				<-done
				<-done
			})
			innerPort := inner.listener.Addr().(*net.TCPAddr).Port
			outer := newChainCountsPeer(t, pair, "hop-secret", func(ctx context.Context, conn net.Conn, target M.Socksaddr) {
				defer conn.Close()
				if target.String() != inner.listener.Addr().String() {
					t.Errorf("outer destination changed: %v", target)
					return
				}
				upstream, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", target.String())
				if err != nil {
					t.Error(err)
					return
				}
				defer upstream.Close()
				if err := N.ReportHandshakeSuccess(conn); err != nil {
					t.Error(err)
					return
				}
				// The peer's relay handler owns and joins both copy directions.
				done := make(chan struct{}, 2)
				copyAndClose := func(dst, src net.Conn) {
					io.Copy(dst, src)
					conn.Close()
					upstream.Close()
					done <- struct{}{}
				}
				go copyAndClose(upstream, conn)
				go copyAndClose(conn, upstream)
				<-done
				<-done
			})
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), core.XrayKey(1), f.instance), 30*time.Second)
			defer cancel()
			manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
			replace := func(c *core.OutboundHandlerConfig) {
				t.Helper()
				if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: c.Tag}); err != nil {
					t.Fatal(err)
				}
				if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: c}); err != nil {
					t.Fatal(err)
				}
			}
			setPort := func(c *core.OutboundHandlerConfig, port int) {
				t.Helper()
				value, err := c.ProxySettings.GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				value.(*anytls.ClientConfig).Server.Port = uint32(port)
				c.ProxySettings = serial.ToTypedMessage(value)
			}
			hop := manager.GetHandler("hop")
			hopConfig := &core.OutboundHandlerConfig{Tag: "hop", ProxySettings: hop.ProxySettings(), SenderSettings: hop.SenderSettings()}
			setPort(hopConfig, outer.listener.Addr().(*net.TCPAddr).Port)
			replace(hopConfig)
			alias := func(tag, original string) *chainCountsAlias {
				t.Helper()
				h := &chainCountsAlias{Handler: manager.GetHandler(original), tag: tag, done: make(chan struct{}, 64)}
				if original == "client" {
					h.ledger = ledger
				}
				if err := manager.AddHandler(ctx, h); err != nil {
					t.Fatal(err)
				}
				return h
			}
			outerAlias := alias("counts-hop", "hop")
			setPort(config, innerPort)
			value, err := config.SenderSettings.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			sender := value.(*proxyman.SenderConfig)
			if mode == "proxySettings" {
				sender.ProxySettings.Tag = outerAlias.Tag()
			} else {
				sender.StreamSettings.SocketSettings.DialerProxy = outerAlias.Tag()
			}
			config.SenderSettings = serial.ToTypedMessage(sender)
			replace(config)
			top := alias("counts-client", "client")
			dispatcher := f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher)
			dial := func() (net.Conn, error) {
				return tagged.Dialer(ctx, dispatcher, xnet.TCPDestination(xnet.DomainAddress("counts.test"), 80), top.Tag())
			}
			exchange := func(conn net.Conn, payload string) error {
				requestCtx, cancelRequest := context.WithTimeout(ctx, 5*time.Second)
				defer cancelRequest()
				canceled := make(chan struct{})
				stop := context.AfterFunc(requestCtx, func() { conn.Close(); close(canceled) })
				defer func() {
					if !stop() {
						<-canceled
					}
				}()
				wire := ledger.register(payload)
				if _, err := conn.Write(wire); err != nil {
					return err
				}
				got := make([]byte, len(wire))
				if _, err := io.ReadFull(conn, got); err != nil {
					return err
				}
				if !bytes.Equal(got, wire) {
					return fmt.Errorf("echo=%q want=%q", got, payload)
				}
				return nil
			}
			joinBusiness := func() {
				obCountsWait(t, top.done, "inner native Dispatch")
				obCountsWait(t, inner.streamDone, "inner peer logical stream")
			}
			sequential := func(n int) {
				t.Helper()
				for i := range n {
					conn, err := dial()
					if err != nil {
						t.Fatal(err)
					}
					err = exchange(conn, fmt.Sprintf("sequential-%d", i))
					conn.Close()
					if err != nil {
						t.Fatal(err)
					}
					joinBusiness()
				}
			}
			assertLayer := func(name string, p *chainCountsPeer, physical, logical, ended, released int64) {
				t.Helper()
				got := [5]int64{p.accepted.Load(), p.physical.Load(), p.logical.Load(), p.ended.Load(), p.released.Load()}
				want := [5]int64{physical, physical, logical, ended, released}
				if got != want {
					t.Fatalf("%s accepts/TLS/logical/closed-TLS/closed-streams=%v want=%v", name, got, want)
				}
			}
			sequential(3)
			assertLayer("inner sequential", inner, 1, 3, 0, 3)
			assertLayer("outer sequential", outer, 1, 1, 0, 0)
			chainCountsAssertWire(t, ctx, f, inner, outer)

			// Authenticate at the real Core inbound, not by injecting a user into
			// the outbound context. Cancel Alice before Bob opens his new stream.
			userPayloads := []struct{ user, payload string }{
				{"alice", "alice-cancel"}, {"bob", "bob-after-alice-cancel"},
			}
			assertUser := func(user, payload string) {
				t.Helper()
				for _, direction := range []string{"uplink", "downlink"} {
					result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>" + user + ">>>traffic>>>" + direction})
					if err != nil || result.GetStat().GetValue() != int64(len(payload)+12) {
						t.Fatalf("%s %s user bytes=%v want=%d: %v", user, direction, result, len(payload)+12, err)
					}
				}
			}
			for _, sample := range userPayloads {
				if err := f.alter(sample.user, sample.user+"-secret", false); err != nil {
					t.Fatal(err)
				}
				client := f.client(t, sample.user+"-secret")
				requestCtx, cancelRequest := context.WithCancel(ctx)
				defer cancelRequest()
				conn, err := client.DialContext(requestCtx, M.Socksaddr{Fqdn: "counts.test", Port: 80})
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				canceled := make(chan struct{})
				stop := context.AfterFunc(requestCtx, func() { conn.Close(); close(canceled) })
				defer stop()
				if err := exchange(conn, sample.payload); err != nil {
					t.Fatal(err)
				}
				cancelRequest()
				obCountsWait(t, canceled, "authenticated request cancellation")
				joinBusiness()
				assertUser(sample.user, sample.payload)
			}
			for _, sample := range userPayloads {
				assertUser(sample.user, sample.payload)
			}
			assertLayer("inner Alice canceled then Bob reused", inner, 1, 5, 0, 5)
			assertLayer("outer Alice canceled then Bob reused", outer, 1, 1, 0, 0)
			chainCountsAssertWire(t, ctx, f, inner, outer)

			// Healthy reuse keeps the inner pool and its established outer tunnel.
			// Handler retirement is a separate C-04 contract, not an idle reuse oracle.
			sequential(3)
			assertLayer("inner healthy reuse", inner, 1, 8, 0, 8)
			assertLayer("outer established tunnel", outer, 1, 1, 0, 0)
			chainCountsAssertWire(t, ctx, f, inner, outer)

			const concurrent = 4
			ready := make(chan error, concurrent)
			done := make(chan error, concurrent)
			release := make(chan struct{})
			var releaseOnce sync.Once
			var workers sync.WaitGroup
			for i := range concurrent {
				workers.Add(1)
				go func() {
					defer workers.Done()
					conn, err := dial()
					if err != nil {
						ready <- err
						done <- err
						return
					}
					defer conn.Close()
					err = exchange(conn, fmt.Sprintf("held-%d", i))
					ready <- err
					if err == nil {
						<-release
						err = exchange(conn, fmt.Sprintf("released-%d", i))
					}
					done <- err
				}()
			}
			joined := make(chan struct{})
			go func() { workers.Wait(); close(joined) }()
			defer func() {
				releaseOnce.Do(func() { close(release) })
				cancel()
				obCountsWait(t, joined, "owned concurrent business workers")
			}()
			for range concurrent {
				select {
				case err := <-ready:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("concurrent business readiness barrier timed out")
				}
			}
			// The native pool reserves idle sessions rather than multiplexing new
			// work onto busy ones. Four held streams require four live sessions at
			// each layer, without a retired pool in this healthy workload.
			assertLayer("inner concurrent", inner, 4, 12, 0, 8)
			assertLayer("outer concurrent", outer, 4, 4, 0, 0)
			releaseOnce.Do(func() { close(release) })
			obCountsWait(t, joined, "concurrent business completion")
			for range concurrent {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				joinBusiness()
			}
			sequential(1)
			assertLayer("inner drained", inner, 4, 13, 0, 13)
			assertLayer("outer held tunnels", outer, 4, 4, 0, 0)
			chainCountsAssertWire(t, ctx, f, inner, outer)
			opened, completed := top.opened.Load(), top.closed.Load()
			if opened != 13 || completed != 13 || outerAlias.opened.Load() != 4 || outerAlias.closed.Load() != 0 {
				t.Fatalf("native dispatch counts inner=%d/%d outer=%d/%d", completed, opened, outerAlias.closed.Load(), outerAlias.opened.Load())
			}
			closed := make(chan struct{})
			go func() {
				defer close(closed)
				if err := f.instance.Close(); err != nil {
					t.Error(err)
				}
			}()
			obCountsWait(t, closed, "Core shutdown")
			for range concurrent {
				obCountsWait(t, outerAlias.done, "outer native Dispatch shutdown")
			}
			// Do not force-close peer sockets on the success path: native shutdown
			// must release both layers before their owned workers can join.
			outer.close(t, false)
			inner.close(t, false)
			stopBackend(false)
			if destinations.Load() != 13 {
				t.Fatalf("real destination TCP accepts=%d want=13", destinations.Load())
			}
			ledger.mu.Lock()
			if len(ledger.requests) != 17 {
				t.Errorf("unique business requests=%d want=17", len(ledger.requests))
			}
			for id, receipt := range ledger.requests {
				if receipt.count != 1 {
					t.Errorf("destination receipt id=%d count=%d want=1", id, receipt.count)
				}
			}
			ledger.mu.Unlock()
			assertLayer("inner shutdown", inner, 4, 13, 4, 13)
			assertLayer("outer shutdown", outer, 4, 4, 4, 4)
			if outerAlias.closed.Load() != 4 {
				t.Fatal("outer native dispatch survived shutdown")
			}
			t.Log("healthy workload: sequential 3 + Alice/Bob 2 + sequential 3 + held concurrent 4 (two messages each) + sequential 1; inner TLS/logical=4/13; outer TLS/logical=4/4; inner pool reused, outer established tunnels persisted (not idle outer reuse); real destination sockets=13, exact unique receipts=17; Alice/Bob bytes isolated; all layer sockets, relay copies, destination and dispatch workers joined")
		})
	}
}
