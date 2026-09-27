package anytls_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/features/outbound"
)

func TestAnyTLSOutboundShutdownOwnsDrainingNativePool(t *testing.T) {
	for _, mode := range []string{"", "proxySettings/anytls", "dialerProxy/anytls"} {
		t.Run("chain="+mode, func(t *testing.T) {
			f, config := outboundFixture(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := f.alter("alice", "alice-secret", false); err != nil {
				t.Fatal(err)
			}
			client := f.client(t, "alice-secret")
			old, err := client.DialContext(ctx, M.Socksaddr{Fqdn: "alpha.test", Port: 80})
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			old.SetDeadline(time.Now().Add(10 * time.Second))
			if _, err := io.WriteString(old, "before"); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, 7)
			if _, err := io.ReadFull(old, got); err != nil || string(got) != "Abefore" {
				t.Fatal("old flow", string(got), err)
			}
			wire := func(direction string) int64 {
				t.Helper()
				result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "outbound>>>client>>>traffic>>>" + direction})
				if err != nil || result.GetStat().GetValue() <= 0 {
					t.Fatal("wire counter", result, err)
				}
				return result.GetStat().GetValue()
			}
			before := [2]int64{wire("uplink"), wire("downlink")}
			manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
			original := manager.GetHandler("client")
			if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err == nil {
				t.Fatal("duplicate outbound accepted")
			}
			if manager.GetHandler("client") != original {
				t.Fatal("rejected duplicate replaced active handler")
			}
			if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
				t.Fatal(err)
			}
			drained := original.(outbound.RetiringHandler).Retirement().Retire()
			select {
			case <-drained:
				t.Fatal("old flow was not kept draining")
			default:
			}
			if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
				t.Fatal(err)
			}
			if wire("uplink") < before[0] || wire("downlink") < before[1] {
				t.Fatal("same-tag recreation reset cumulative counters")
			}
			if got, err := exchange(client, "alpha.test", "after"); err != nil || got != "Aafter" {
				t.Fatal("new pool", got, err)
			}
			if wire("uplink") <= before[0] || wire("downlink") <= before[1] {
				t.Fatal("replacement stopped contributing to cumulative counters")
			}
			for direction, want := range map[string]int64{"uplink": 11, "downlink": 13} {
				result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>alice>>>traffic>>>" + direction})
				if err != nil || result.GetStat().GetValue() != want {
					t.Fatal("user counter reset/doubled", direction, result, err)
				}
			}
			closed := make(chan error, 1)
			go func() { closed <- f.instance.Close() }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("instance shutdown left removed native pool running")
			}
			select {
			case <-drained:
			default:
				t.Fatal("instance Close returned before old pool drained")
			}
			old.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := old.Read(make([]byte, 1)); err == nil {
				t.Fatal("removed old stream survived instance shutdown")
			} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatal("old stream timed out instead of receiving close", err)
			}
		})
	}
}
