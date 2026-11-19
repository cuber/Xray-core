package router

import (
	"slices"
	"testing"
	"time"

	"github.com/xtls/xray-core/features/routing"
)

func TestWeightedPolicyReadOnlyAndCopy(t *testing.T) {
	o := observedAs(map[string]time.Duration{"p": 30 * time.Millisecond, "q": 10 * time.Millisecond})
	sequence := func(read bool) []string {
		m := memberMap(t, o, level{tag: "l", strategy: "weightedleastping", selector: []string{"p", "q"}, weights: map[string]float32{"p": 3, "q": 1}})
		s := m["l"].strategy.(*WeightedLeastPingStrategy)
		s.settings.MaxRTT, s.settings.RttTolerance = int64(time.Second), int64(100*time.Millisecond)
		v := balancerView(m)
		var result []string
		for range 40 {
			if read {
				policy, err := v.GetWeightedPolicy("l")
				if err != nil || policy.MaxRTT != time.Second || policy.RTTTolerance != 100*time.Millisecond {
					t.Fatalf("%v %v", policy, err)
				}
				policy.MaxRTT = 0
			}
			tag, err := m["l"].PickOutbound()
			if err != nil {
				t.Fatal(err)
			}
			result = append(result, tag)
		}
		if _, err := v.GetWeightedPolicy("missing"); err == nil {
			t.Fatal("missing tag accepted")
		}
		return result
	}
	if !slices.Equal(sequence(false), sequence(true)) {
		t.Fatal("reading policy changed weighted picks")
	}
}

func TestWeightedPolicyUsesPublishedView(t *testing.T) {
	weighted := memberMap(t, nil, level{tag: "l", strategy: "weightedleastping", selector: []string{"p"}})
	weighted["l"].strategy.(*WeightedLeastPingStrategy).settings.MaxRTT = int64(250 * time.Millisecond)
	random := memberMap(t, nil, level{tag: "l", strategy: "random", selector: []string{"p"}})
	r := &Router{}
	r.balancers.Store(&weighted)
	old := r.Balancers()
	r.balancers.Store(&random)
	policy, err := old.(routing.BalancerWeightedPolicy).GetWeightedPolicy("l")
	if err != nil || policy == nil || policy.MaxRTT != 250*time.Millisecond {
		t.Fatalf("published view lost its policy on reload: %v %v", policy, err)
	}
	strategy, err := old.GetStrategy("l")
	if err != nil || strategy != "weightedleastping" {
		t.Fatalf("published view mixed strategies: %s %v", strategy, err)
	}
	policy, err = r.GetWeightedPolicy("l")
	if err != nil || policy != nil {
		t.Fatalf("current non-weighted strategy retained stale policy: %v %v", policy, err)
	}
}
