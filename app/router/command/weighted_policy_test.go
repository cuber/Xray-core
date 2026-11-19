package command_test

import (
	"testing"
	"time"

	"github.com/xtls/xray-core/app/router"
	. "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/serial"
)

func TestWeightedPolicyRuntimeRPC(t *testing.T) {
	for _, tc := range []struct {
		name              string
		settings          map[string]any
		maxRTT, tolerance time.Duration
	}{
		{"explicit", map[string]any{"maxRTT": "250.125ms", "rttTolerance": "100ms"}, 250*time.Millisecond + 125*time.Microsecond, 100 * time.Millisecond},
		{"default", map[string]any{}, 0, 0},
		{"clamped", map[string]any{"maxRTT": "-1ms", "rttTolerance": "-2ms"}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRPCFixture(t, "1s", []any{
				map[string]any{"tag": "t", "selector": []string{"a-"}, "strategy": map[string]any{"type": "weightedLeastPing", "settings": tc.settings}},
				map[string]any{"tag": "other", "selector": []string{"a-"}, "strategy": map[string]any{"type": "random"}},
			})
			f.releaseAll()
			check := func(msg *BalancerMsg) {
				t.Helper()
				p := msg.GetWeightedPolicy()
				if p == nil || p.MaxRttNs != int64(tc.maxRTT) || p.RttToleranceNs != int64(tc.tolerance) {
					t.Fatalf("not the loaded weighted policy: %v", p)
				}
				if msg.ThresholdPolicy != nil || msg.ThresholdDecision != nil {
					t.Fatal("weighted strategy acquired threshold state")
				}
			}
			check(f.info(t, "t"))
			list, err := f.routing.ListBalancers(call(t), &ListBalancersRequest{})
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range list.Balancers {
				if item.Tag == "t" {
					check(item.Balancer)
				} else if item.Balancer.WeightedPolicy != nil {
					t.Fatal("non-weighted policy")
				}
			}
			_, err = f.routing.AddRule(call(t), &AddRuleRequest{ShouldAppend: false, Config: serial.ToTypedMessage(&router.Config{
				BalancingRule: []*router.BalancingRule{{
					Tag: "t", OutboundSelector: []string{"a-"}, Strategy: "weightedleastping",
					StrategySettings: serial.ToTypedMessage(&router.StrategyWeightedLeastPingConfig{MaxRTT: int64(500 * time.Millisecond), RttTolerance: int64(200 * time.Millisecond)}),
				}},
			})})
			if err != nil {
				t.Fatal(err)
			}
			p := f.info(t, "t").GetWeightedPolicy()
			if p == nil || p.MaxRttNs != int64(500*time.Millisecond) || p.RttToleranceNs != int64(200*time.Millisecond) {
				t.Fatalf("policy stale after reload: %v", p)
			}
		})
	}
	if field := (&BalancerMsg{}).ProtoReflect().Descriptor().Fields().ByName("weighted_policy"); field == nil || field.Number() != 13 {
		t.Fatal("weighted policy must be additive field13")
	}
}
