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

type retirementAdmissionConn struct {
	net.Conn
	syn                     bool
	applications            atomic.Int32
	entered, closed, exited chan struct{}
	once                    sync.Once
}

func (c *retirementAdmissionConn) Write(p []byte) (int, error) {
	// TLS 1.2 leaves record types visible. Authentication is application
	// write 1; the new session buffers settings with SYN/destination in write 2.
	if len(p) > 0 && p[0] == 23 && c.applications.Add(1) == 2 && c.syn {
		close(c.entered)
		defer close(c.exited)
		<-c.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}

func (c *retirementAdmissionConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })
	return err
}

type retirementAdmissionDialer struct {
	internet.DefaultSystemDialer
	port                               xnet.Port
	syn                                bool
	calls                              atomic.Int32
	first                              atomic.Pointer[retirementAdmissionConn]
	entered, canceled, release, exited chan struct{}
}

func (d *retirementAdmissionDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, options *internet.SocketConfig) (net.Conn, error) {
	c, err := d.DefaultSystemDialer.Dial(ctx, source, dest, options)
	if err != nil || dest.Port != d.port {
		return c, err
	}
	if d.calls.Add(1) != 1 {
		return c, nil
	}
	w := &retirementAdmissionConn{Conn: c, syn: d.syn, entered: d.entered, closed: make(chan struct{}), exited: d.exited}
	d.first.Store(w)
	if !d.syn {
		close(d.entered)
		<-ctx.Done()
		close(d.canceled)
		<-d.release // A real connected socket returns successfully after retirement.
		close(d.exited)
	}
	return w, nil
}

func TestAnyTLSOutboundRetirementAdmissionBarriers(t *testing.T) {
	for _, phase := range []string{"late-dial", "open-syn"} {
		t.Run(phase, func(t *testing.T) {
			f, config := outboundFixture(t, "")
			value, err := config.ProxySettings.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			d := &retirementAdmissionDialer{port: xnet.Port(value.(*anytls.ClientConfig).Server.Port), syn: phase == "open-syn", entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{})}
			var release sync.Once
			unblock := func() { release.Do(func() { close(d.release) }) }
			internet.UseAlternativeSystemDialer(d)
			t.Cleanup(func() {
				unblock()
				if c := d.first.Load(); c != nil {
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
			await := func(name string, done <-chan struct{}) {
				t.Helper()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatalf("%s did not join", name)
				}
			}
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
			dispatch := func() (<-chan struct{}, *bytes.Buffer) {
				result := new(bytes.Buffer)
				done := make(chan struct{})
				request := session.ContextWithOutbounds(ctx, []*session.Outbound{{Tag: "client", Target: xnet.TCPDestination(xnet.DomainAddress("alpha.test"), 80)}})
				go func() {
					defer close(done)
					old.Dispatch(request, &transport.Link{Reader: buf.NewReader(bytes.NewReader([]byte("must-not-admit"))), Writer: buf.NewWriter(result)})
				}()
				return done, result
			}
			pending, result := dispatch()
			t.Cleanup(func() {
				cancel()
				unblock()
				if c := d.first.Load(); c != nil {
					c.Close()
				}
				await("pending cleanup", pending)
			})
			await("admission barrier", d.entered)
			select {
			case <-pending:
				t.Fatal("dispatch ended before removal")
			default:
			}
			remove()
			if manager.GetHandler("client") != nil {
				t.Fatal("retired tag still registered")
			}
			if !d.syn {
				await("dial cancellation", d.canceled)
			}
			add()
			replacement := manager.GetHandler("client")
			if replacement == old {
				t.Fatal("replacement reused old handler")
			}
			before := d.calls.Load()
			if before != 1 {
				t.Fatalf("pending physical dials=%d want=1", before)
			}
			rejected, output := dispatch()
			await("post-retire rejection", rejected)
			if output.Len() != 0 || d.calls.Load() != before {
				t.Fatal("retired handler admitted another request")
			}
			business, err := core.Dial(session.SetForcedOutboundTagToContext(ctx, "client"), f.instance, xnet.TCPDestination(xnet.DomainAddress("alpha.test"), 80))
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
					t.Fatalf("response=%q want=%q err=%v", got, want, err)
				}
			}
			round("before", "Abefore")
			// Keep replacement business live while the old dial finally returns.
			unblock()
			await("blocked worker", d.exited)
			await("socket close", d.first.Load().closed)
			await("dispatch", pending)
			await("retirement", old.(outbound.RetiringHandler).Retirement().Retire())
			if result.Len() != 0 || ctx.Err() != nil {
				t.Fatalf("admitted output=%q ctx=%v", result, ctx.Err())
			}
			wantApplications := int32(0)
			if d.syn {
				wantApplications = 2
			}
			if got := d.first.Load().applications.Load(); got != wantApplications {
				t.Fatalf("old application records=%d want=%d", got, wantApplications)
			}
			round("after-return", "after-return")
			closed := make(chan struct{})
			var closeErr error
			go func() { closeErr = old.Close(); close(closed) }()
			await("old Close", closed)
			if closeErr != nil || manager.GetHandler("client") != replacement {
				t.Fatalf("old cleanup changed replacement: %v", closeErr)
			}
			round("after", "after")
			t.Logf("barrier=%s; pending socket/worker/dispatch joined; retired admission rejected; replacement survived old Close", phase)
		})
	}
}
