package anytls_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy/anytls"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	xrayTLS "github.com/xtls/xray-core/transport/internet/tls"
)

type retirementAuthConn struct {
	*probeBlockedAuthConn
	socketClosed chan struct{}
	socketOnce   sync.Once
}

func (c *retirementAuthConn) Close() error {
	err := c.probeBlockedAuthConn.Close()
	c.socketOnce.Do(func() { close(c.socketClosed) })
	return err
}

type retirementAuthDialer struct {
	internet.DefaultSystemDialer
	port                    xnet.Port
	calls                   atomic.Int32
	first                   atomic.Pointer[retirementAuthConn]
	entered, closed, exited chan struct{}
}

func (d *retirementAuthDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, options *internet.SocketConfig) (net.Conn, error) {
	conn, err := d.DefaultSystemDialer.Dial(ctx, source, dest, options)
	if err != nil || dest.Port != d.port {
		return conn, err
	}
	if d.calls.Add(1) != 1 {
		return conn, nil
	}
	wrapped := &retirementAuthConn{probeBlockedAuthConn: &probeBlockedAuthConn{Conn: conn, entered: d.entered, closed: d.closed, exited: d.exited}, socketClosed: make(chan struct{})}
	d.first.Store(wrapped)
	return wrapped, nil
}

func TestAnyTLSOutboundRetirementDuringAuthWrite(t *testing.T) {
	f, config := outboundFixture(t, "")
	value, err := config.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	dialer := &retirementAuthDialer{port: xnet.Port(value.(*anytls.ClientConfig).Server.Port), entered: make(chan struct{}), closed: make(chan struct{}), exited: make(chan struct{})}
	internet.UseAlternativeSystemDialer(dialer)
	t.Cleanup(func() {
		if c := dialer.first.Load(); c != nil {
			c.Close()
		}
		internet.UseAlternativeSystemDialer(nil)
	})
	value, err = config.SenderSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	sender := value.(*proxyman.SenderConfig)
	security, err := sender.StreamSettings.SecuritySettings[0].GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	security.(*xrayTLS.Config).MaxVersion = "1.2"
	sender.StreamSettings.SecuritySettings[0] = serial.ToTypedMessage(security)
	config.SenderSettings = serial.ToTypedMessage(sender)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), core.XrayKey(1), f.instance), 10*time.Second)
	defer cancel()
	remove := func() {
		t.Helper()
		if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
			t.Fatal(err)
		}
	}
	add := func() {
		t.Helper()
		if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
			t.Fatal(err)
		}
	}
	remove()
	add()
	manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	old := manager.GetHandler("client")
	if old == nil {
		t.Fatal("missing native handler")
	}
	await := func(name string, done <-chan struct{}) {
		t.Helper()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s did not join within 1s", name)
		}
	}
	// Retain the real native handler while control-plane removal unregisters it.
	// Dispatch completion is observable independently from the engine write worker.
	dispatch := func(h outbound.Handler) (<-chan struct{}, *bytes.Buffer) {
		result := new(bytes.Buffer)
		done := make(chan struct{})
		requestCtx := session.ContextWithOutbounds(ctx, []*session.Outbound{{Tag: "client", Target: xnet.TCPDestination(xnet.DomainAddress("alpha.test"), 80)}})
		go func() {
			defer close(done)
			h.Dispatch(requestCtx, &transport.Link{Reader: buf.NewReader(bytes.NewReader([]byte("must-not-admit"))), Writer: buf.NewWriter(result)})
		}()
		return done, result
	}
	pending, result := dispatch(old)
	await("TLS 1.2 authentication write barrier", dialer.entered)
	select {
	case <-pending:
		t.Fatal("pending dispatch ended before retirement")
	default:
	}
	remove()
	if manager.GetHandler("client") != nil {
		t.Fatal("RemoveOutbound retained tag")
	}
	await("authentication write worker", dialer.exited)
	await("physical socket Close", dialer.first.Load().socketClosed)
	await("pending dispatch", pending)
	await("retirement drain", old.(outbound.RetiringHandler).Retirement().Retire())
	if result.Len() != 0 {
		t.Fatal("pending authentication admitted business payload")
	}
	if ctx.Err() != nil {
		t.Fatal("parent timeout rather than RemoveOutbound canceled admission")
	}
	add()
	if manager.GetHandler("client") == old {
		t.Fatal("replacement reused retired handler")
	}
	before := dialer.calls.Load()
	rejected, rejectedResult := dispatch(old)
	await("retired handler rejection", rejected)
	if rejectedResult.Len() != 0 || dialer.calls.Load() != before {
		t.Fatal("retired handler admitted a new dial/request")
	}
	if err := f.alter("alice", "alice-secret", false); err != nil {
		t.Fatal(err)
	}
	client := f.client(t, "alice-secret")
	business, err := client.DialContext(ctx, M.Socksaddr{Fqdn: "alpha.test", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	defer business.Close()
	business.SetDeadline(time.Now().Add(3 * time.Second))
	round := func(payload, want string) {
		t.Helper()
		if _, err := io.WriteString(business, payload); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(want))
		if _, err := io.ReadFull(business, got); err != nil || string(got) != want {
			t.Fatalf("replacement response=%q want=%q error=%v", got, want, err)
		}
	}
	round("before", "Abefore")
	oldClosed := make(chan struct{})
	var closeErr error
	go func() { closeErr = old.Close(); close(oldClosed) }()
	await("old handler Close", oldClosed)
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	round("after", "after")
	if got, err := exchange(client, "alpha.test", "fresh"); err != nil || got != "Afresh" {
		t.Fatalf("fresh replacement stream %q: %v", got, err)
	}
}
