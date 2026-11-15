package router

import (
	"context"
	goerrors "errors"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/app/proxyman"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	routing_session "github.com/xtls/xray-core/features/routing/session"
	"github.com/xtls/xray-core/proxy/blackhole"
	"github.com/xtls/xray-core/proxy/freedom"
)

func thresholdRule(tag, fallback string, maxRTT, recoverRTT time.Duration) *BalancingRule {
	return &BalancingRule{
		Tag: tag, OutboundSelector: []string{"a-"}, Strategy: "thresholdleastping", FallbackTag: fallback,
		StrategySettings: serial.ToTypedMessage(&StrategyThresholdLeastPingConfig{
			MaxRTT: int64(maxRTT), RecoverRTT: int64(recoverRTT),
		}),
	}
}

// Core spec027 C08 and C04 on the direct protobuf path: the typed settings are validated again,
// recoverRTT 0 means maxRTT.
func TestThresholdLeastPingFromProtobuf(t *testing.T) {
	ms := time.Millisecond
	for _, c := range []struct {
		name string
		rule *BalancingRule
		want string
	}{
		{"no settings", &BalancingRule{Tag: "t", OutboundSelector: []string{"a-"}, Strategy: "thresholdleastping"}, "needs settings with maxRTT"},
		{"wrong message", &BalancingRule{
			Tag: "t", OutboundSelector: []string{"a-"}, Strategy: "thresholdleastping",
			StrategySettings: serial.ToTypedMessage(&StrategyLeastLoadConfig{MaxRTT: int64(50 * ms)}),
		}, "not a StrategyThresholdLeastPingConfig"},
		{"zero maxRTT", thresholdRule("t", "", 0, 0), "maxRTT must be positive"},
		{"negative maxRTT", thresholdRule("t", "", -ms, 0), "maxRTT must be positive"},
		{"negative recoverRTT", thresholdRule("t", "", 50*ms, -ms), "recoverRTT must be positive"},
		{"recoverRTT above maxRTT", thresholdRule("t", "", 50*ms, 60*ms), "recoverRTT 60ms is above maxRTT 50ms"},
	} {
		if _, err := c.rule.Build(nil, nil); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: error %v, want %q", c.name, err, c.want)
		}
	}
	for _, c := range []struct{ maxRTT, recoverRTT, want time.Duration }{
		{50 * ms, 0, 50 * ms}, {50 * ms, 40 * ms, 40 * ms}, {50 * ms, 50 * ms, 50 * ms},
	} {
		b, err := thresholdRule("t", "", c.maxRTT, c.recoverRTT).Build(nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		policy := b.strategy.(*ThresholdLeastPingStrategy).policy
		if policy.maxRTT != c.maxRTT || policy.recoverRTT != c.want {
			t.Fatalf("loaded %v/%v, want %v/%v", policy.maxRTT, policy.recoverRTT, c.maxRTT, c.want)
		}
	}
}

// startThresholdCore runs a Core with observed a-hy2, a-vless and f-backup, an unobserved direct,
// a blackhole block, and the given routing.
func startThresholdCore(t *testing.T, config *Config) *Router {
	t.Helper()
	outbounds := []*core.OutboundHandlerConfig{
		{Tag: "block", ProxySettings: serial.ToTypedMessage(&blackhole.Config{})},
		{Tag: "direct", ProxySettings: serial.ToTypedMessage(&freedom.Config{})},
	}
	for _, tag := range []string{"a-hy2", "a-vless", "f-backup"} {
		outbounds = append(outbounds, &core.OutboundHandlerConfig{Tag: tag, ProxySettings: serial.ToTypedMessage(&freedom.Config{})})
	}
	instance, err := core.New(&core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(config),
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			// Probes go to a closed local port: no test reaches the network.
			serial.ToTypedMessage(&observatory.Config{
				SubjectSelector: []string{"a-", "f-"}, ProbeUrl: "http://127.0.0.1:9/", ProbeInterval: int64(time.Hour),
			}),
		},
		Outbound: outbounds,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { instance.Close() })
	return instance.GetFeature(routing.RouterType()).(*Router)
}

func pickThresholdRoute(r *Router) (string, error) {
	ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{
		Target: net.TCPDestination(net.DomainAddress("example.com"), 80),
	}})
	route, err := r.PickRoute(routing_session.AsRoutingContext(ctx))
	if err != nil {
		return "", err
	}
	return route.GetOutboundTag(), nil
}

func loadedPolicy(t *testing.T, r *Router, tag string) thresholdPolicy {
	t.Helper()
	b, ok := (*r.balancers.Load())[tag]
	if !ok {
		t.Fatalf("no balancer %s", tag)
	}
	return b.strategy.(*ThresholdLeastPingStrategy).policy
}

// Core spec027: until decisions are published (T004) a connection to a thresholdLeastPing balancer
// is dropped, never sent to its fallback or the default outbound; an override still wins (TL-8).
// C12: a publication adopts a new limit with the new map. TL-7 on publication: an unobserved
// fallback and a fallbackTag naming nothing are refused and the previous map stays (O06).
func TestThresholdLeastPingRouterLifecycle(t *testing.T) {
	ms := time.Millisecond
	r := startThresholdCore(t, &Config{
		Rule:          []*RoutingRule{chainRule("t")},
		BalancingRule: []*BalancingRule{thresholdRule("t", "f-backup", 50*ms, 40*ms)},
	})
	if strategy, _ := r.GetStrategy("t"); strategy != "thresholdleastping" {
		t.Fatalf("strategy %q", strategy)
	}
	if tag, err := pickThresholdRoute(r); !goerrors.Is(err, routing.ErrRouteDropped) {
		t.Fatalf("pick before any decision = %q, %v; want a drop", tag, err)
	}
	if err := r.SetOverrideTarget("t", "a-vless"); err != nil {
		t.Fatal(err)
	}
	if tag, err := pickThresholdRoute(r); err != nil || tag != "a-vless" {
		t.Fatalf("pick with an override = %q, %v", tag, err)
	}

	if err := r.ReloadRules(&Config{
		Rule:          []*RoutingRule{chainRule("t")},
		BalancingRule: []*BalancingRule{thresholdRule("t", "f-backup", 30*ms, 0)},
	}, false); err != nil {
		t.Fatal(err)
	}
	if p := loadedPolicy(t, r, "t"); p.maxRTT != 30*ms || p.recoverRTT != 30*ms {
		t.Fatalf("after publication %v/%v", p.maxRTT, p.recoverRTT)
	}

	for _, c := range []struct{ fallback, want string }{
		{"direct", "balancer t2: outbound direct is not observed (thresholdLeastPing needs every outbound it can choose observed)"},
		{"nothing", "balancer t2: fallbackTag nothing names no outbound and no balancer"},
	} {
		err := r.ReloadRules(&Config{BalancingRule: []*BalancingRule{thresholdRule("t2", c.fallback, 50*ms, 0)}}, true)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("append with fallback %s: %v", c.fallback, err)
		}
		if _, ok := (*r.balancers.Load())["t2"]; ok {
			t.Fatalf("refused t2 (fallback %s) was published", c.fallback)
		}
	}
	if p := loadedPolicy(t, r, "t"); p.maxRTT != 30*ms {
		t.Fatalf("the previous map changed: %v", p.maxRTT)
	}
	// An actual blackhole needs no observation; another thresholdLeastPing balancer as the fallback is
	// checked through the reached set.
	if err := r.ReloadRules(&Config{BalancingRule: []*BalancingRule{
		thresholdRule("t3", "block", 50*ms, 0), thresholdRule("t4", "t3", 50*ms, 0),
	}}, true); err != nil {
		t.Fatalf("blackhole and balancer fallbacks: %v", err)
	}
}
