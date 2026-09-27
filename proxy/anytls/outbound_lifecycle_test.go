package anytls_test

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy/anytls"
	"google.golang.org/protobuf/proto"
)

func TestAnyTLSOutboundChainedReplacementDrains(t *testing.T) {
	for _, mode := range []string{"proxySettings/anytls", "dialerProxy/anytls"} {
		t.Run(mode, func(t *testing.T) {
			f, good := outboundFixture(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			remove := func(tag string) {
				t.Helper()
				if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: tag}); err != nil {
					t.Fatal(err)
				}
			}
			add := func(config *core.OutboundHandlerConfig) {
				t.Helper()
				if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
					t.Fatal(err)
				}
			}
			list, err := f.api.ListOutbounds(ctx, &handler.ListOutboundsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			var hop *core.OutboundHandlerConfig
			for _, config := range list.Outbounds {
				if config.Tag == "hop" {
					hop = config
				}
			}
			if hop == nil {
				t.Fatal("missing hop fixture")
			}
			if err := f.alter("alice", "alice-secret", false); err != nil {
				t.Fatal(err)
			}
			client := f.client(t, "alice-secret")
			old, err := client.DialContext(ctx, M.Socksaddr{Fqdn: "alpha.test", Port: 80})
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			old.SetDeadline(time.Now().Add(20 * time.Second))
			checkOld := func(step int) {
				t.Helper()
				payload := fmt.Sprintf("old-%d", step)
				if _, err := old.Write([]byte(payload)); err != nil {
					t.Fatal("old flow interrupted", err)
				}
				want := payload
				if step == 0 {
					want = "A" + want
				}
				got := make([]byte, len(want))
				if _, err := io.ReadFull(old, got); err != nil || string(got) != want {
					t.Fatalf("old flow step %d: %q %v", step, got, err)
				}
			}
			checkOld(0)
			manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
			retirement := manager.GetHandler("client").(outbound.RetiringHandler).Retirement()
			remove("client")
			drained := retirement.Retire()
			select {
			case <-drained:
				t.Fatal("active old flow reported drained")
			default:
			}
			bad := proto.Clone(good).(*core.OutboundHandlerConfig)
			settings, _ := bad.ProxySettings.GetInstance()
			settings.(*anytls.ClientConfig).Server.User.Account = serial.ToTypedMessage(&anytls.Account{Password: "wrong-replacement"})
			bad.ProxySettings = serial.ToTypedMessage(settings)
			add(bad)
			if _, err := exchange(client, "alpha.test", "new-denied"); err == nil {
				t.Fatal("replacement reused old authenticated pool")
			}
			checkOld(1)
			remove("client")
			add(good)
			if got, err := exchange(client, "alpha.test", "new"); err != nil || got != "Anew" {
				t.Fatal("replacement did not recover", got, err)
			}
			remove("hop")
			remove("client")
			add(good)
			if _, err := exchange(client, "alpha.test", "no-hop"); err == nil {
				t.Fatal("missing hop silently bypassed")
			}
			checkOld(2)
			add(hop)
			if got, err := exchange(client, "alpha.test", "restored"); err != nil || got != "Arestored" {
				t.Fatal("restored hop failed", got, err)
			}
			checkOld(3)
			// Keep a replacement flow active while the removed handler finishes;
			// whole-instance shutdown cannot prove old-only cleanup isolation.
			replacement, err := client.DialContext(ctx, M.Socksaddr{Fqdn: "beta.test", Port: 80})
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Close()
			replacement.SetDeadline(time.Now().Add(5 * time.Second))
			checkReplacement := func(payload, want string) {
				t.Helper()
				if _, err := io.WriteString(replacement, payload); err != nil {
					t.Fatal(err)
				}
				got := make([]byte, len(want))
				if _, err := io.ReadFull(replacement, got); err != nil || string(got) != want {
					t.Fatalf("replacement flow: got=%q want=%q err=%v", got, want, err)
				}
			}
			checkReplacement("before-drain", "Bbefore-drain")
			old.Close()
			select {
			case <-drained:
			case <-time.After(3 * time.Second):
				t.Fatal("retired top handler did not finish after old flow closed")
			}
			checkReplacement("after-drain", "after-drain")
			if got, err := exchange(client, "alpha.test", "new-after-drain"); err != nil || got != "Anew-after-drain" {
				t.Fatal("old cleanup damaged replacement admission", got, err)
			}
		})
	}
}
