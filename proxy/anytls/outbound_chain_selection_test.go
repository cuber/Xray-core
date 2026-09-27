package anytls_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/anytls"
	"github.com/xtls/xray-core/proxy/socks"
	"github.com/xtls/xray-core/transport/internet"
	"google.golang.org/protobuf/proto"
)

func selectionReplace(t *testing.T, f *integrationFixture, config *core.OutboundHandlerConfig) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: config.Tag}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
		t.Fatal(err)
	}
}

func TestAnyTLSOutboundChainPrecedence(t *testing.T) {
	// The configured server endpoint is an armed trap. Only the two real
	// redirecting relays reach verified-TLS AnyTLS listeners with A/B routes.
	trap, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var direct atomic.Int64
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		for {
			conn, err := trap.Accept()
			if err != nil {
				return
			}
			direct.Add(1)
			conn.Close()
		}
	}()
	t.Cleanup(func() { trap.Close(); addressJoin(t, joined) })
	f, config := outboundFixture(t, "", func(c map[string]any) {
		r := c["routing"].(map[string]any)
		r["rules"] = append([]any{
			map[string]any{"type": "field", "inboundTag": []string{"anytls-remote"}, "outboundTag": "a"},
			map[string]any{"type": "field", "inboundTag": []string{"selection-alternate"}, "outboundTag": "b"},
		}, r["rules"].([]any)...)
	})
	value, err := config.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	portA := value.(*anytls.ClientConfig).Server.Port
	portB := unusedPort(t)
	alternate := map[string]any{}
	for k, v := range f.inbound {
		alternate[k] = v
	}
	alternate["tag"], alternate["port"] = "selection-alternate", portB
	alternate["settings"] = map[string]any{"clients": []any{map[string]any{"email": "remote", "password": "remote-secret"}}}
	if err := f.addInbound(t, alternate); err != nil {
		t.Fatal(err)
	}
	for tag, port := range map[string]uint32{"selection-proxy": portA, "selection-dialer": uint32(portB)} {
		raw, err := json.Marshal(map[string]any{"tag": tag, "protocol": "freedom", "settings": map[string]any{"redirect": fmt.Sprintf("127.0.0.1:%d", port), "ipsBlocked": []string{}}})
		if err != nil {
			t.Fatal(err)
		}
		var parsed conf.OutboundDetourConfig
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatal(err)
		}
		built, err := parsed.Build()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err = f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: built})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	set := func(proxyTag string) {
		addressReplace(t, f, config, func(c *anytls.ClientConfig, s *proxyman.SenderConfig) {
			c.Server.Port = uint32(trap.Addr().(*net.TCPAddr).Port)
			s.ProxySettings = nil
			if proxyTag != "" {
				s.ProxySettings = &internet.ProxyConfig{Tag: proxyTag}
			}
			s.StreamSettings.SocketSettings = &internet.SocketConfig{DialerProxy: "selection-dialer"}
		})
	}
	if err := f.alter("alice", "selection-secret", false); err != nil {
		t.Fatal(err)
	}
	client := f.client(t, "selection-secret")
	set("selection-proxy")
	if got, err := exchange(client, "alpha.test", "both-configured"); err != nil || got != "Aboth-configured" {
		t.Fatalf("proxySettings did not win: %q %v", got, err)
	}
	// A missing higher-priority handler must fail, not use the viable dialer
	// relay or the direct endpoint. Recreate top each time to discard its pool.
	set("selection-missing")
	if got, err := exchange(client, "alpha.test", "must-not-fallback"); err == nil {
		t.Fatalf("missing proxy fell back: %q", got)
	}
	set("")
	if got, err := exchange(client, "alpha.test", "dialer-only"); err != nil || got != "Bdialer-only" {
		t.Fatalf("dialer control did not reach alternate marker: %q %v", got, err)
	}
	set("selection-proxy")
	if got, err := exchange(client, "alpha.test", "restored"); err != nil || got != "Arestored" {
		t.Fatalf("fresh proxy recovery: %q %v", got, err)
	}
	client.Close()
	f.instance.Close()
	trap.Close()
	addressJoin(t, joined)
	if direct.Load() != 0 {
		t.Fatalf("direct trap received %d connections", direct.Load())
	}
	t.Log("proxySettings A wins over viable dialerProxy B; missing proxy fails; dialer-only B and restored A succeed; direct trap zero")
}

func TestAnyTLSOutboundChainedSocketBinding(t *testing.T) {
	// One actual SOCKS relay through both native entrances is sufficient to
	// observe the physical relay socket without multiplying all transports.
	for _, entrance := range []string{"proxySettings", "dialerProxy"} {
		for _, binding := range []string{"source", "interface"} {
			t.Run(entrance+"/"+binding, func(t *testing.T) {
				iface := ""
				if binding == "interface" {
					interfaces, err := net.Interfaces()
					addressCapability(t, err)
					for _, i := range interfaces {
						if i.Flags&net.FlagLoopback != 0 && i.Flags&net.FlagUp != 0 {
							iface = i.Name
							break
						}
					}
					if iface == "" {
						addressCapability(t, fmt.Errorf("no loopback interface"))
					}
				}
				probe := addressEcho(t, "tcp4", "127.0.0.1")
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				conn, err := (&internet.DefaultSystemDialer{}).Dial(ctx, xnet.LocalHostIP, xnet.TCPDestination(xnet.LocalHostIP, xnet.Port(M.ParseSocksaddr(probe).Port)), &internet.SocketConfig{Interface: iface})
				cancel()
				addressCapability(t, err)
				if iface != "" {
					got, err := addressBoundInterface(conn)
					conn.Close()
					addressCapability(t, err)
					if got != iface {
						addressCapability(t, fmt.Errorf("kernel binding=%q want=%q", got, iface))
					}
				} else {
					conn.Close()
				}
				d := &addressSystemDialer{observed: make(chan addressSocketObservation, 8)}
				internet.UseAlternativeSystemDialer(d)
				t.Cleanup(func() { internet.UseAlternativeSystemDialer(nil) })
				f, config := outboundFixture(t, entrance+"/socks")
				manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
				hop := manager.GetHandler("hop")
				proxy, err := hop.ProxySettings().GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				d.port = xnet.Port(proxy.(*socks.ClientConfig).Server.Port)
				value, err := hop.SenderSettings().GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				sender := proto.Clone(value.(*proxyman.SenderConfig)).(*proxyman.SenderConfig)
				sender.Via = xnet.NewIPOrDomain(xnet.LocalHostIP)
				if sender.StreamSettings == nil {
					sender.StreamSettings = &internet.StreamConfig{}
				}
				sender.StreamSettings.SocketSettings = &internet.SocketConfig{Interface: iface}
				bound := &core.OutboundHandlerConfig{Tag: "hop", SenderSettings: serial.ToTypedMessage(sender), ProxySettings: hop.ProxySettings()}
				selectionReplace(t, f, bound)
				if err := f.alter("alice", "chain-binding-secret", false); err != nil {
					t.Fatal(err)
				}
				client := f.client(t, "chain-binding-secret")
				if binding == "source" {
					sender.Via = xnet.NewIPOrDomain(xnet.ParseAddress("::1"))
					bound.SenderSettings = serial.ToTypedMessage(sender)
					selectionReplace(t, f, bound)
					if got, err := exchange(client, "alpha.test", "bad-family"); err == nil {
						t.Fatalf("relay source binding ignored: %q", got)
					}
					sender.Via = xnet.NewIPOrDomain(xnet.LocalHostIP)
					bound.SenderSettings = serial.ToTypedMessage(sender)
					selectionReplace(t, f, bound)
					selectionReplace(t, f, config)
				}
				if got, err := exchange(client, "alpha.test", "bound-relay"); err != nil || got != "Abound-relay" {
					t.Fatalf("bound relay: %q %v", got, err)
				}
				select {
				case got := <-d.observed:
					if got.err != nil || got.source != "127.0.0.1" || got.local != "127.0.0.1" || got.iface != iface {
						t.Fatalf("actual SOCKS socket=%+v want source=127.0.0.1 interface=%q", got, iface)
					}
					t.Logf("native AnyTLS -> SOCKS port=%d physical local=%s source=%s kernel interface=%q", d.port, got.local, got.source, got.iface)
				case <-time.After(5 * time.Second):
					t.Fatal("missing physical SOCKS socket observation")
				}
			})
		}
	}
}
