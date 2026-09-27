package anytls_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy/anytls"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
)

// Observe framing above TLS without replacing authentication or stream handling.
// The prologue is SHA256(password), uint16 padding length, then that padding;
// subsequent frames have command/uint32 stream/uint16 length headers.
type returnFINReader struct {
	net.Conn
	pending  []byte
	authDone bool
	fin      chan<- uint32
}

func (c *returnFINReader) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.pending = append(c.pending, p[:n]...)
	if !c.authDone {
		if len(c.pending) < 34 {
			return n, err
		}
		end := 34 + int(binary.BigEndian.Uint16(c.pending[32:34]))
		if len(c.pending) < end {
			return n, err
		}
		c.pending = c.pending[end:]
		c.authDone = true
	}
	for len(c.pending) >= 7 {
		end := 7 + int(binary.BigEndian.Uint16(c.pending[5:7]))
		if len(c.pending) < end {
			break
		}
		if c.pending[0] == 3 { // AnyTLS cmdFIN, not a TLS record guess.
			select {
			case c.fin <- binary.BigEndian.Uint32(c.pending[1:5]):
			default:
			}
		}
		c.pending = c.pending[end:]
	}
	return n, err
}

func returnFINPeer(t *testing.T, f *integrationFixture, fin chan<- uint32) *chainCountsPeer {
	t.Helper()
	cert := f.inbound["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)["certificates"].([]any)[0].(map[string]any)
	pair, err := tls.X509KeyPair([]byte(strings.Join(cert["certificate"].([]string), "\n")), []byte(strings.Join(cert["key"].([]string), "\n")))
	if err != nil {
		t.Fatal(err)
	}
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}})
	if err != nil {
		t.Fatal(err)
	}
	p := &chainCountsPeer{listener: l, acceptDone: make(chan struct{}), sessionDone: make(chan struct{}, 8)}
	svc, err := engine.NewService("remote-secret", engine.ServiceOptions{
		Handler: chainCountsHandler(func(_ context.Context, c net.Conn, target M.Socksaddr) {
			defer c.Close()
			if target.Fqdn != "return.test" || target.Port != 80 {
				t.Errorf("unexpected target %v", target)
				return
			}
			if err := N.ReportHandshakeSuccess(c); err != nil {
				t.Error(err)
				return
			}
			io.Copy(c, c)
		}),
		SessionReady:  func(context.Context) { p.physical.Add(1) },
		SessionClosed: func(context.Context) { p.ended.Add(1); p.sessionDone <- struct{}{} },
	})
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	go func() {
		defer close(p.acceptDone)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			first := p.accepted.Add(1) == 1
			p.sockets.Store(c, struct{}{})
			p.workers.Add(1)
			go func() {
				defer p.workers.Done()
				defer p.sockets.Delete(c)
				defer c.Close()
				var input net.Conn = c
				if first {
					input = &returnFINReader{Conn: c, fin: fin}
				}
				if err := svc.NewConnection(context.Background(), input, M.Socksaddr{}, nil); err != nil {
					t.Error(err)
				}
			}()
		}
	}()
	t.Cleanup(func() { p.close(t, true) })
	return p
}

type returnGateConn struct {
	net.Conn
	armed                                     atomic.Bool
	writeHeld, writeRelease, writeReturned    chan struct{}
	socketClosed, closeRelease, closeReturned chan struct{}
	writeOnce, releaseOnce, closeOnce         sync.Once
	closeErr                                  error
}

func (c *returnGateConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if c.armed.CompareAndSwap(true, false) {
		close(c.writeHeld)
		<-c.writeRelease
		close(c.writeReturned)
	}
	return n, err
}

func (c *returnGateConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.Conn.Close()
		close(c.socketClosed)
		<-c.closeRelease
		close(c.closeReturned)
	})
	return c.closeErr
}

func (c *returnGateConn) releaseWrite() { c.writeOnce.Do(func() { close(c.writeRelease) }) }
func (c *returnGateConn) releaseClose() { c.releaseOnce.Do(func() { close(c.closeRelease) }) }

type returnGateDialer struct {
	internet.DefaultSystemDialer
	port  xnet.Port
	calls atomic.Int32
	first atomic.Pointer[returnGateConn]
}

func (d *returnGateDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, options *internet.SocketConfig) (net.Conn, error) {
	c, err := d.DefaultSystemDialer.Dial(ctx, source, dest, options)
	if err != nil || dest.Port != d.port {
		return c, err
	}
	if d.calls.Add(1) != 1 {
		return c, nil
	}
	w := &returnGateConn{Conn: c, writeHeld: make(chan struct{}), writeRelease: make(chan struct{}), writeReturned: make(chan struct{}), socketClosed: make(chan struct{}), closeRelease: make(chan struct{}), closeReturned: make(chan struct{})}
	d.first.Store(w)
	return w, nil
}

type returnUploadReader struct {
	buf.Reader
	remaining int32
	done      chan struct{}
	once      sync.Once
}

func (r *returnUploadReader) Interrupt() { common.Interrupt(r.Reader) }
func (r *returnUploadReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	if r.remaining == 0 {
		r.once.Do(func() { close(r.done) })
	}
	mb, err := r.Reader.ReadMultiBuffer()
	r.remaining -= mb.Len()
	return mb, err
}

type returnAlias struct {
	*chainCountsAlias
	uploaded chan struct{}
	finished chan struct{}
}

func (h *returnAlias) Dispatch(ctx context.Context, link *transport.Link) {
	defer close(h.finished)
	h.chainCountsAlias.Dispatch(ctx, &transport.Link{Reader: &returnUploadReader{Reader: link.Reader, remaining: 6, done: h.uploaded}, Writer: link.Writer})
}

func TestAnyTLSOutboundRetirementFINReturnAndClose(t *testing.T) {
	runNativeReturnClose(t)
}

func runNativeReturnClose(t *testing.T) {
	t.Helper()
	f, config := outboundFixture(t, "")
	fin := make(chan uint32, 8)
	p := returnFINPeer(t, f, fin)
	value, err := config.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	value.(*anytls.ClientConfig).Server.Port = uint32(p.listener.Addr().(*net.TCPAddr).Port)
	config.ProxySettings = serial.ToTypedMessage(value)
	d := &returnGateDialer{port: xnet.Port(p.listener.Addr().(*net.TCPAddr).Port)}
	internet.UseAlternativeSystemDialer(d)
	t.Cleanup(func() {
		if c := d.first.Load(); c != nil {
			c.releaseWrite()
			c.releaseClose()
		}
		closed := make(chan struct{})
		go func() { f.instance.Close(); close(closed) }()
		obCountsWait(t, closed, "return fixture Core cleanup")
		internet.UseAlternativeSystemDialer(nil)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	remove := func(ctx context.Context) {
		t.Helper()
		if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
			t.Fatal(err)
		}
	}
	add := func(ctx context.Context) {
		t.Helper()
		if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
			t.Fatal(err)
		}
	}
	remove(ctx)
	add(ctx)
	m := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	old := m.GetHandler("client")
	a := &returnAlias{chainCountsAlias: &chainCountsAlias{Handler: old, tag: "return-old", done: make(chan struct{}, 8)}, uploaded: make(chan struct{}), finished: make(chan struct{})}
	if err := m.AddHandler(ctx, a); err != nil {
		t.Fatal(err)
	}
	target := xnet.TCPDestination(xnet.DomainAddress("return.test"), 80)
	request, abort := context.WithCancel(ctx)
	defer abort()
	oldBusiness, err := core.Dial(session.SetForcedOutboundTagToContext(request, a.Tag()), f.instance, target)
	if err != nil {
		t.Fatal(err)
	}
	defer oldBusiness.Close()
	t.Cleanup(func() {
		abort()
		if c := d.first.Load(); c != nil {
			c.releaseWrite()
			c.releaseClose()
		}
		oldBusiness.Close()
		obCountsWait(t, a.finished, "old Dispatch cleanup")
	})
	round := func(c net.Conn, data string, deadline time.Time) {
		t.Helper()
		c.SetDeadline(deadline)
		if _, err := io.WriteString(c, data); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(data))
		if _, err := io.ReadFull(c, got); err != nil || string(got) != data {
			t.Fatalf("echo=%q want=%q err=%v", got, data, err)
		}
	}
	round(oldBusiness, "before", time.Now().Add(3*time.Second))
	obCountsWait(t, a.uploaded, "old native upload completion")
	gate := d.first.Load()
	if gate == nil {
		t.Fatal("old physical socket missing")
	}
	// This entire overlap must finish before the engine's 5s FIN write watchdog.
	phase, stopPhase := context.WithTimeout(ctx, 3*time.Second)
	defer stopPhase()
	deadline, _ := phase.Deadline()
	await := func(name string, done <-chan struct{}) {
		t.Helper()
		select {
		case <-done:
		case <-phase.Done():
			t.Fatalf("%s exceeded shared FIN/Close budget", name)
		}
	}
	stillPending := func(name string, done <-chan struct{}) {
		t.Helper()
		select {
		case <-done:
			t.Fatalf("%s completed across held barrier", name)
		default:
		}
	}
	gate.armed.Store(true)
	abort()
	await("physical FIN write return gate", gate.writeHeld)
	select {
	case id := <-fin:
		if id != 1 {
			t.Fatalf("actual FIN stream=%d want=1", id)
		}
	case <-phase.Done():
		t.Fatal("held transport write did not contain a real AnyTLS FIN")
	}
	stillPending("old Dispatch", a.finished)
	stillPending("old socket Close", gate.socketClosed)
	remove(phase)
	if m.GetHandler("client") != nil {
		t.Fatal("removed tag still registered")
	}
	drained := old.(outbound.RetiringHandler).Retirement().Retire()
	stillPending("retirement during FIN return", drained)
	add(phase)
	replacement := m.GetHandler("client")
	if replacement == old {
		t.Fatal("replacement reused old handler")
	}
	replacementCtx, stopReplacement := context.WithCancel(ctx)
	defer stopReplacement()
	business, err := core.Dial(session.SetForcedOutboundTagToContext(replacementCtx, "client"), f.instance, target)
	if err != nil {
		t.Fatal(err)
	}
	defer business.Close()
	round(business, "fin-held", deadline)
	gate.releaseWrite()
	await("FIN Write return", gate.writeReturned)
	await("old raw socket closed", gate.socketClosed)
	stillPending("retirement during Close return", drained)
	stillPending("old Dispatch during Close return", a.finished)
	oldClosed := make(chan struct{})
	var oldCloseErr error
	go func() { oldCloseErr = old.Close(); close(oldClosed) }()
	t.Cleanup(func() {
		gate.releaseWrite()
		gate.releaseClose()
		obCountsWait(t, oldClosed, "old handler Close cleanup")
	})
	round(business, "close-held", deadline)
	stillPending("old handler Close", oldClosed)
	// A retained native handler must reject admission even during its cleanup.
	rejected := make(chan struct{})
	output := new(bytes.Buffer)
	go func() {
		defer close(rejected)
		request := session.ContextWithOutbounds(context.WithValue(phase, core.XrayKey(1), f.instance), []*session.Outbound{{Tag: "client", Target: target}})
		old.Dispatch(request, &transport.Link{Reader: buf.NewReader(bytes.NewReader([]byte("forbidden"))), Writer: buf.NewWriter(output)})
	}()
	t.Cleanup(func() { gate.releaseWrite(); gate.releaseClose(); obCountsWait(t, rejected, "stale Dispatch cleanup") })
	await("stale handler rejection", rejected)
	if output.Len() != 0 || d.calls.Load() != 2 {
		t.Fatalf("stale admission output=%q physical dials=%d", output, d.calls.Load())
	}
	gate.releaseClose()
	await("raw Close return", gate.closeReturned)
	await("old native Dispatch", a.finished)
	await("retirement drain", drained)
	await("old handler Close return", oldClosed)
	if oldCloseErr != nil || phase.Err() != nil || m.GetHandler("client") != replacement {
		t.Fatalf("old cleanup ownership: close=%v phase=%v", oldCloseErr, phase.Err())
	}
	round(business, "after-cleanup", deadline)
	stopReplacement()
	business.Close()
	remove(ctx)
	obCountsWait(t, replacement.(outbound.RetiringHandler).Retirement().Retire(), "replacement pool drain")
	for range 2 {
		obCountsWait(t, p.sessionDone, "peer session shutdown")
	}
	p.close(t, false)
	if p.accepted.Load() != 2 || p.physical.Load() != 2 || p.ended.Load() != 2 {
		t.Fatal(fmt.Sprintf("peer accept/ready/ended=%d/%d/%d", p.accepted.Load(), p.physical.Load(), p.ended.Load()))
	}
	t.Log("real FIN stream=1 held before Write return; old physical Close held after socket shutdown; replacement served at both barriers and after cleanup; stale admission rejected; both pools joined")
}
