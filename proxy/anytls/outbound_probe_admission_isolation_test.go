package anytls_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/app/observatory/burst"
	"github.com/xtls/xray-core/app/proxyman"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/proxy/anytls"
	"github.com/xtls/xray-core/transport/internet"
	xrayTLS "github.com/xtls/xray-core/transport/internet/tls"
)

type admissionIsolationConn struct {
	net.Conn
	d    *admissionIsolationDialer
	once sync.Once
}

func (c *admissionIsolationConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.d.closed) })
	return err
}

func (c *admissionIsolationConn) Read(p []byte) (int, error) {
	if c.d.phase == "tls" {
		close(c.d.entered)
		defer close(c.d.exited)
		<-c.d.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Read(p)
}

func (c *admissionIsolationConn) Write(p []byte) (int, error) {
	if c.d.phase == "auth" && len(p) > 0 && p[0] == 23 {
		close(c.d.entered)
		defer close(c.d.exited)
		<-c.d.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}

type admissionIsolationDialer struct {
	internet.DefaultSystemDialer
	port                    xnet.Port
	phase                   string
	calls                   atomic.Int32
	entered, exited, closed chan struct{}
}

func (d *admissionIsolationDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, options *internet.SocketConfig) (net.Conn, error) {
	if dest.Port != d.port || d.calls.Add(1) != 2 {
		return d.DefaultSystemDialer.Dial(ctx, source, dest, options)
	}
	if d.phase == "dial" {
		close(d.entered)
		defer close(d.exited)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	conn, err := d.DefaultSystemDialer.Dial(ctx, source, dest, options)
	if err != nil {
		return nil, err
	}
	return &admissionIsolationConn{Conn: conn, d: d}, nil
}

func TestAnyTLSOutboundProbeAdmissionPreservesSharedPoolBusiness(t *testing.T) {
	for _, phase := range []string{"dial", "tls", "auth"} {
		t.Run(phase, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
			t.Cleanup(server.Close)
			f, config := outboundFixture(t, "", func(c map[string]any) {
				c["policy"].(map[string]any)["levels"].(map[string]any)["0"].(map[string]any)["handshake"] = 20
				for _, raw := range c["outbounds"].([]any) {
					out := raw.(map[string]any)
					if out["tag"] == "b" {
						out["settings"].(map[string]any)["redirect"] = server.Listener.Addr().String()
					}
				}
			})
			value, err := config.ProxySettings.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			d := &admissionIsolationDialer{port: xnet.Port(value.(*anytls.ClientConfig).Server.Port), phase: phase, entered: make(chan struct{}), exited: make(chan struct{}), closed: make(chan struct{})}
			internet.UseAlternativeSystemDialer(d)
			t.Cleanup(func() { internet.UseAlternativeSystemDialer(nil) })
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
			selectionReplace(t, f, config)
			manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
			observed := &obCountsOutbound{Handler: manager.GetHandler("client"), done: make(chan struct{}, 8)}
			if err := manager.AddHandler(context.Background(), observed); err != nil {
				t.Fatal(err)
			}
			if err := f.alter("alice", "isolation-secret", false); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), core.XrayKey(1), f.instance), 10*time.Second)
			defer cancel()
			client := f.client(t, "isolation-secret")
			business, err := client.DialContext(ctx, M.Socksaddr{Fqdn: "alpha.test", Port: 80})
			if err != nil {
				t.Fatal(err)
			}
			defer business.Close()
			round := func(payload, want string) {
				t.Helper()
				business.SetDeadline(time.Now().Add(2 * time.Second))
				if _, err := io.WriteString(business, payload); err != nil {
					t.Fatal(err)
				}
				got := make([]byte, len(want))
				if _, err := io.ReadFull(business, got); err != nil || string(got) != want {
					t.Fatalf("business got=%q want=%q err=%v", got, want, err)
				}
			}
			round("before", "Abefore")
			ping := burst.NewHealthPing(ctx, f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher), &burst.HealthPingConfig{
				Destination: "http://beta.test:8080/generate_204", Timeout: int64(time.Second), SamplingCount: 1,
			})
			done := make(chan struct{})
			var checkErr error
			// One deadline covers Check, blocked I/O and native Dispatch. It is
			// earlier than both the 20s native handshake and 10s parent context.
			joined, stopJoin := context.WithTimeout(context.Background(), 2*time.Second)
			defer stopJoin()
			await := func(ch <-chan struct{}, name string) {
				t.Helper()
				select {
				case <-ch:
				case <-joined.Done():
					t.Fatalf("%s survived probe cancellation deadline", name)
				}
			}
			go func() { defer close(done); checkErr = ping.Check([]string{observed.Tag()}) }()
			dispatchJoined := false
			defer func() {
				cancel()
				obCountsWait(t, done, "probe cleanup")
				if !dispatchJoined {
					obCountsWait(t, observed.done, "failed probe dispatch cleanup")
				}
			}()
			obCountsWait(t, d.entered, "probe admission stall")
			round("during", "during")
			select {
			case <-done:
				t.Fatal("probe was not concurrent with business")
			default:
			}
			await(done, "probe timeout")
			await(d.exited, "admission worker")
			if phase != "dial" {
				await(d.closed, "admission socket")
			}
			await(observed.done, "native probe Dispatch")
			dispatchJoined = true
			if checkErr != nil || ping.Results[observed.Tag()].Get().Alive {
				t.Fatalf("stalled probe result: %v %v", checkErr, ping.Results[observed.Tag()].Get())
			}
			round("after", "after")
			if err := ping.Check([]string{observed.Tag()}); err != nil {
				t.Fatal(err)
			}
			obCountsWait(t, observed.done, "fresh probe dispatch")
			if !ping.Results[observed.Tag()].Get().Alive {
				t.Fatal("first fresh probe did not recover")
			}
			round("after-recovery", "after-recovery")
			if d.calls.Load() != 3 {
				t.Fatalf("physical dials=%d want business/stalled/fresh=3", d.calls.Load())
			}
		})
	}
}
