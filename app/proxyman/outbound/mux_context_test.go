package outbound

import (
	"context"
	"testing"

	"github.com/xtls/xray-core/app/policy"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/app/stats"
	"github.com/xtls/xray-core/common/mux"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/transport/internet"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
)

func TestHandlerMuxFactoriesKeepOwningInstance(t *testing.T) {
	instances := make([]*core.Instance, 2)
	handlers := make([]*Handler, 2)
	for i := range instances {
		instance, err := core.New(&core.Config{App: []*serial.TypedMessage{
			serial.ToTypedMessage(&stats.Config{}),
			serial.ToTypedMessage(&policy.Config{}),
		}})
		if err != nil {
			t.Fatal(err)
		}
		instances[i] = instance
		t.Cleanup(func() { _ = instance.Close() })
		manager, err := New(context.Background(), &proxyman.OutboundConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if err := instance.AddFeature(manager); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), core.XrayKey(1), instance))
		cancel()
		raw, err := NewHandler(ctx, &core.OutboundHandlerConfig{
			Tag: "same-tag-in-independent-owners",
			ProxySettings: serial.ToTypedMessage(&freedom.Config{FinalRules: []*freedom.FinalRuleConfig{{
				Action: freedom.RuleAction_Allow,
			}}}),
			SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{
				MultiplexSettings: &proxyman.MultiplexingConfig{Enabled: true, Concurrency: 8, XudpConcurrency: 8},
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		handlers[i] = raw.(*Handler)
		t.Cleanup(func() { _ = raw.Close() })
	}
	for i, handler := range handlers {
		for name, client := range map[string]*mux.ClientManager{"tcp": handler.mux, "xudp": handler.xudp} {
			t.Run(name, func(t *testing.T) {
				if client == nil || client.Picker == nil {
					t.Fatal("Mux factory not constructed")
				}
				factory := client.Picker.(*mux.IncrementalWorkerPicker).Factory.(*mux.DialingWorkerFactory)
				if factory.Context == nil {
					t.Fatal("Mux factory lost owning context")
				}
				if core.FromContext(factory.Context) != instances[i] {
					t.Fatal("Mux factory resolves a different Core owner")
				}
				want := instances[i].GetFeature(outbound.ManagerType()).(outbound.Manager)
				if internet.ResolveOutboundManager(factory.Context) != want {
					t.Fatal("Mux factory resolves a different outbound manager")
				}
				if factory.Context.Err() != nil {
					t.Fatal("Mux factory inherited constructor cancellation")
				}
			})
		}
	}
}
