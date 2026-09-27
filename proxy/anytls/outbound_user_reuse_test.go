package anytls_test

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	stats "github.com/xtls/xray-core/app/stats/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy/anytls"
	"github.com/xtls/xray-core/transport/internet"
)

type userReuseDialer struct {
	internet.SystemDialer
	port     xnet.Port
	physical atomic.Int64
}

func (d *userReuseDialer) Dial(ctx context.Context, source xnet.Address, destination xnet.Destination, settings *internet.SocketConfig) (net.Conn, error) {
	conn, err := d.SystemDialer.Dial(ctx, source, destination, settings)
	if err == nil && destination.Port == d.port {
		d.physical.Add(1)
	}
	return conn, err
}

func TestAnyTLSOutboundCanceledUserThenReusedPool(t *testing.T) {
	for _, mode := range []string{"", "proxySettings/anytls", "dialerProxy/anytls"} {
		t.Run("chain="+mode, func(t *testing.T) {
			f, config := outboundFixture(t, mode, func(config map[string]any) {
				for _, raw := range config["routing"].(map[string]any)["rules"].([]any) {
					rule := raw.(map[string]any)
					if tags, ok := rule["inboundTag"].([]string); ok && len(tags) == 1 && tags[0] == "anytls-test" {
						rule["outboundTag"] = "counted-client"
					}
				}
			})
			value, err := config.ProxySettings.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			dialer := &userReuseDialer{SystemDialer: &internet.DefaultSystemDialer{}, port: xnet.Port(value.(*anytls.ClientConfig).Server.Port)}
			internet.UseAlternativeSystemDialer(dialer)
			t.Cleanup(func() { f.instance.Close(); internet.UseAlternativeSystemDialer(nil) })
			manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
			observed := &obCountsOutbound{Handler: manager.GetHandler("client"), done: make(chan struct{}, 8)}
			if err := manager.AddHandler(context.Background(), observed); err != nil {
				t.Fatal(err)
			}
			for _, user := range []string{"alice", "bob"} {
				if err := f.alter(user, user+"-reuse", false); err != nil {
					t.Fatal(err)
				}
			}
			alice := f.client(t, "alice-reuse")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			first, err := alice.DialContext(ctx, M.Socksaddr{Fqdn: "alpha.test", Port: 80})
			if err != nil {
				t.Fatal(err)
			}
			first.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := io.WriteString(first, "alice-before-cancel"); err != nil {
				t.Fatal(err)
			}
			response := make([]byte, len("Aalice-before-cancel"))
			if _, err := io.ReadFull(first, response); err != nil || string(response) != "Aalice-before-cancel" {
				t.Fatal(string(response), err)
			}
			first.Close()
			alice.Close()
			obCountsWait(t, observed.done, "Alice canceled native Dispatch")
			if dialer.physical.Load() != 1 {
				t.Fatal("first user did not use one inner TLS transport", dialer.physical.Load())
			}
			bob := f.client(t, "bob-reuse")
			if got, err := exchange(bob, "alpha.test", "bob-after-cancel"); err != nil || got != "Abob-after-cancel" {
				t.Fatal(got, err)
			}
			obCountsWait(t, observed.done, "Bob native Dispatch")
			if dialer.physical.Load() != 1 {
				t.Fatal("second user did not reuse inner TLS transport", dialer.physical.Load())
			}
			for user, payload := range map[string]string{"alice": "alice-before-cancel", "bob": "bob-after-cancel"} {
				for direction, want := range map[string]int64{"uplink": int64(len(payload)), "downlink": int64(len(payload) + 1)} {
					got, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>" + user + ">>>traffic>>>" + direction})
					if err != nil || got.GetStat().GetValue() != want {
						t.Fatalf("%s/%s got=%v want=%d err=%v", user, direction, got, want, err)
					}
				}
			}
		})
	}
}
