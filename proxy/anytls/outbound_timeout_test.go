package anytls_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/app/observatory/burst"
	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/proxy/anytls"
	"github.com/xtls/xray-core/transport/internet"
	xrayTLS "github.com/xtls/xray-core/transport/internet/tls"
)

func TestAnyTLSOutboundProbeTimeoutCancelsTLS(t *testing.T) {
	f, config := outboundFixture(t, "")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	accepted := make(chan net.Conn, 1)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		accepted <- conn
		io.Copy(io.Discard, conn) // Receive ClientHello but never answer TLS.
	}()
	t.Cleanup(func() {
		listener.Close()
		select {
		case conn := <-accepted:
			conn.Close()
		default:
		}
		select {
		case <-closed:
		case <-time.After(3 * time.Second):
			t.Error("silent TLS fixture did not exit")
		}
	})
	value, err := config.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	value.(*anytls.ClientConfig).Server.Port = uint32(listener.Addr().(*net.TCPAddr).Port)
	config.ProxySettings = serial.ToTypedMessage(value)
	ctx := context.WithValue(context.Background(), core.XrayKey(1), f.instance)
	if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
		t.Fatal(err)
	}
	ping := burst.NewHealthPing(ctx, f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher), &burst.HealthPingConfig{
		Destination: "http://unresolvable.invalid:8080/generate_204", Timeout: int64(200 * time.Millisecond), SamplingCount: 1,
	})
	if err := ping.Check([]string{"client"}); err != nil {
		t.Fatal(err)
	}
	if ping.Results["client"].Get().Alive {
		t.Fatal("silent TLS peer was marked healthy")
	}
	if len(accepted) != 1 {
		t.Fatal("probe never reached the TLS fixture")
	}
	select {
	case <-closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("HTTP probe ended but its TLS handshake is still running")
	}
}

type probeBlockedAuthConn struct {
	net.Conn
	entered, closed, exited chan struct{}
	enter, close, exit      sync.Once
}

func (c *probeBlockedAuthConn) Write(p []byte) (int, error) {
	// TLS 1.2 retains the record type outside encryption: handshake records
	// pass normally; the first application record carries AnyTLS authentication.
	if len(p) > 0 && p[0] == 23 {
		defer c.exit.Do(func() { close(c.exited) })
		c.enter.Do(func() { close(c.entered) })
		<-c.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}

func (c *probeBlockedAuthConn) Close() error {
	c.close.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type probeAuthDialer struct {
	internet.DefaultSystemDialer
	port                    xnet.Port
	entered, closed, exited chan struct{}
}

func (d *probeAuthDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, options *internet.SocketConfig) (net.Conn, error) {
	conn, err := d.DefaultSystemDialer.Dial(ctx, source, dest, options)
	if err != nil || dest.Port != d.port {
		return conn, err
	}
	return &probeBlockedAuthConn{Conn: conn, entered: d.entered, closed: d.closed, exited: d.exited}, nil
}

func TestAnyTLSOutboundProbeTimeoutCancelsAuthWrite(t *testing.T) {
	f, config := outboundFixture(t, "")
	value, err := config.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	dialer := &probeAuthDialer{port: xnet.Port(value.(*anytls.ClientConfig).Server.Port), entered: make(chan struct{}), closed: make(chan struct{}), exited: make(chan struct{})}
	internet.UseAlternativeSystemDialer(dialer)
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
	ctx := context.WithValue(context.Background(), core.XrayKey(1), f.instance)
	if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
		t.Fatal(err)
	}
	ping := burst.NewHealthPing(ctx, f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher), &burst.HealthPingConfig{
		Destination: "http://unresolvable.invalid:8080/generate_204", Timeout: int64(200 * time.Millisecond), SamplingCount: 1,
	})
	if err := ping.Check([]string{"client"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dialer.entered:
	default:
		t.Fatal("probe did not finish TLS and enter authentication write")
	}
	select {
	case <-dialer.exited:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("probe timeout left authentication write blocked")
	}
	if ping.Results["client"].Get().Alive {
		t.Fatal("blocked authentication write marked healthy")
	}
}

type probeStalledDialer struct {
	internet.DefaultSystemDialer
	port    xnet.Port
	entered chan struct{}
	exited  chan struct{}
}

func (d *probeStalledDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, options *internet.SocketConfig) (net.Conn, error) {
	if dest.Port != d.port {
		return d.DefaultSystemDialer.Dial(ctx, source, dest, options)
	}
	close(d.entered)
	defer close(d.exited)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestAnyTLSOutboundProbeTimeoutCancelsDial(t *testing.T) {
	f, config := outboundFixture(t, "")
	dialer := &probeStalledDialer{port: xnet.Port(unusedPort(t)), entered: make(chan struct{}), exited: make(chan struct{})}
	internet.UseAlternativeSystemDialer(dialer)
	t.Cleanup(func() { internet.UseAlternativeSystemDialer(nil) })
	config.Tag = "stalled-dial"
	value, err := config.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	value.(*anytls.ClientConfig).Server.Port = uint32(dialer.port)
	config.ProxySettings = serial.ToTypedMessage(value)
	ctx := context.WithValue(context.Background(), core.XrayKey(1), f.instance)
	if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
		t.Fatal(err)
	}
	ping := burst.NewHealthPing(ctx, f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher), &burst.HealthPingConfig{
		Destination: "http://unresolvable.invalid:8080/generate_204", Timeout: int64(200 * time.Millisecond), SamplingCount: 1,
	})
	if err := ping.Check([]string{"stalled-dial"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dialer.entered:
	default:
		t.Fatal("probe did not enter physical dial")
	}
	select {
	case <-dialer.exited:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("probe timeout left physical dial running")
	}
	if ping.Results["stalled-dial"].Get().Alive {
		t.Fatal("canceled dial marked healthy")
	}
}

func TestAnyTLSOutboundProbeTimeoutPreservesBusiness(t *testing.T) {
	for _, mode := range []string{"", "proxySettings/anytls", "dialerProxy/anytls"} {
		t.Run(mode, func(t *testing.T) {
			seen := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- struct{}{}
				<-r.Context().Done()
			}))
			t.Cleanup(server.Close)
			f, _ := outboundFixture(t, mode, func(config map[string]any) {
				for _, value := range config["outbounds"].([]any) {
					out := value.(map[string]any)
					if out["tag"] == "b" {
						out["settings"].(map[string]any)["redirect"] = server.Listener.Addr().String()
					}
				}
			})
			if err := f.alter("alice", "alice-secret", false); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), core.XrayKey(1), f.instance), 10*time.Second)
			defer cancel()
			client := f.client(t, "alice-secret")
			business, err := client.DialContext(ctx, M.Socksaddr{Fqdn: "alpha.test", Port: 80})
			if err != nil {
				t.Fatal(err)
			}
			defer business.Close()
			business.SetDeadline(time.Now().Add(5 * time.Second))
			round := func(payload, want string) {
				t.Helper()
				if _, err := io.WriteString(business, payload); err != nil {
					t.Fatal(err)
				}
				got := make([]byte, len(want))
				if _, err := io.ReadFull(business, got); err != nil || string(got) != want {
					t.Fatalf("business response %q want %q: %v", got, want, err)
				}
			}
			round("before", "Abefore")
			ping := burst.NewHealthPing(ctx, f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher), &burst.HealthPingConfig{
				Destination: "http://beta.test:8080/generate_204?ob=timeout", Timeout: int64(200 * time.Millisecond), SamplingCount: 1,
			})
			if err := ping.Check([]string{"client"}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-seen:
			default:
				t.Fatal("probe did not reach its remote HTTP target")
			}
			if ping.Results["client"].Get().Alive {
				t.Fatal("stalled HTTP response marked healthy")
			}
			round("after", "after")
			for direction, want := range map[string]int64{"uplink": 11, "downlink": 12} {
				result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>alice>>>traffic>>>" + direction})
				if err != nil || result.Stat.Value != want {
					t.Fatalf("probe inherited business user accounting: %s %v %v", direction, result, err)
				}
			}
			if got, err := exchange(client, "alpha.test", "fresh"); err != nil || got != "Afresh" {
				t.Fatalf("new business stream failed after probe timeout: %q %v", got, err)
			}
		})
	}
}
