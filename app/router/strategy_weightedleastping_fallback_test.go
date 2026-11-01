package router

import (
	"context"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common/serial"
)

// spec016: a weightedLeastPing balancer with a fallbackTag.

func newFallbackStrategy(minSamples int32) *WeightedLeastPingStrategy {
	s := NewWeightedLeastPingStrategy(&StrategyWeightedLeastPingConfig{
		Weights:      []*StrategyWeight{{Match: "a", Value: 1}, {Match: "b", Value: 0.1}},
		MaxRTT:       int64(250 * time.Millisecond),
		RttTolerance: int64(100 * time.Millisecond),
		Tolerance:    0.2,
		MinSamples:   minSamples,
	})
	s.FallbackTag = "fb"
	return s
}

// FR-001 (W1): every candidate measured and none alive yields no node.
func TestWeightedLeastPingFallbackWhenEveryCandidateIsMeasuredDown(t *testing.T) {
	s := newFallbackStrategy(1)
	statuses := []*observatory.OutboundStatus{
		weightedStatus("a", 100*time.Millisecond, 20, 20),
		weightedStatus("b", 0, 1, 1),
	}
	if nodes := s.filterNodes([]string{"a", "b"}, statuses); len(nodes) != 0 {
		t.Fatalf("filterNodes() = %#v, want none", nodes)
	}
	// A classic observatory status carries no health ping and is a verdict as it stands.
	classic := []*observatory.OutboundStatus{{OutboundTag: "a"}, {OutboundTag: "b"}}
	if nodes := s.filterNodes([]string{"a", "b"}, classic); len(nodes) != 0 {
		t.Fatalf("classic filterNodes() = %#v, want none", nodes)
	}
}

// FR-002 and FR-003 (W2, W3): any other empty healthy pool keeps every positive-weight candidate.
func TestWeightedLeastPingFallbackKeepsTheCandidatesOtherwise(t *testing.T) {
	dead := weightedStatus("a", 100*time.Millisecond, 20, 20)
	cases := []struct {
		name       string
		fallback   string
		minSamples int32
		statuses   []*observatory.OutboundStatus
	}{
		{"no fallbackTag", "", 1, []*observatory.OutboundStatus{dead, weightedStatus("b", 0, 3, 3)}},
		{"alive but slow", "fb", 1, []*observatory.OutboundStatus{dead, weightedStatus("b", 300*time.Millisecond, 20, 0)}},
		{"alive but failing", "fb", 1, []*observatory.OutboundStatus{dead, weightedStatus("b", 100*time.Millisecond, 20, 10)}},
		{"below minSamples", "fb", 5, []*observatory.OutboundStatus{dead, weightedStatus("b", 0, 4, 4)}},
		{"no samples yet", "fb", 0, []*observatory.OutboundStatus{dead, weightedStatus("b", 0, 0, 0)}},
		{"missing status", "fb", 1, []*observatory.OutboundStatus{dead}},
		{"no observation", "fb", 1, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newFallbackStrategy(c.minSamples)
			s.FallbackTag = c.fallback
			nodes := s.filterNodes([]string{"b", "a"}, c.statuses)
			if len(nodes) != 2 || nodes[0].Tag != "a" || nodes[1].Tag != "b" {
				t.Fatalf("filterNodes() = %#v, want [a b]", nodes)
			}
		})
	}
}

// FR-001 at the balancer, and the control plane's view of it (FR-005).
func TestWeightedLeastPingBalancerPicksItsFallback(t *testing.T) {
	rule := &BalancingRule{
		Tag: "route", OutboundSelector: []string{"a", "b"}, Strategy: "weightedleastping", FallbackTag: "fb",
		StrategySettings: serial.ToTypedMessage(&StrategyWeightedLeastPingConfig{MinSamples: 1}),
	}
	balancer, err := rule.Build(&luaRouteOutboundManager{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := balancer.strategy.(*WeightedLeastPingStrategy)
	if s.FallbackTag != "fb" {
		t.Fatalf("strategy FallbackTag = %q, want the balancer's", s.FallbackTag)
	}
	observer := &contractObserver{result: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{
		weightedStatus("a", 10*time.Millisecond, 5, 0), weightedStatus("b", 10*time.Millisecond, 5, 5),
	}}}
	s.ctx, s.observer = context.Background(), observer
	router := &Router{}
	router.balancers.Store(&map[string]*Balancer{"route": balancer})

	pick := func() string {
		tag, err := balancer.PickOutbound()
		if err != nil {
			t.Fatal(err)
		}
		return tag
	}
	fallback := func() (string, bool, bool) {
		tag, active, reported, err := router.GetFallback("route")
		if err != nil {
			t.Fatal(err)
		}
		return tag, active, reported
	}
	if got := pick(); got != "a" {
		t.Fatalf("healthy pick = %q, want a", got)
	}
	if tag, active, reported := fallback(); tag != "fb" || active || !reported {
		t.Fatalf("healthy GetFallback = %q %v %v", tag, active, reported)
	}

	observer.result = &observatory.ObservationResult{Status: []*observatory.OutboundStatus{
		weightedStatus("a", 10*time.Millisecond, 5, 5), weightedStatus("b", 10*time.Millisecond, 5, 5),
	}}
	if got := pick(); got != "fb" {
		t.Fatalf("all-down pick = %q, want fb", got)
	}
	if tag, active, reported := fallback(); tag != "fb" || !active || !reported {
		t.Fatalf("all-down GetFallback = %q %v %v", tag, active, reported)
	}
	if targets, err := router.GetPrincipleTarget("route"); err != nil || len(targets) != 0 {
		t.Fatalf("all-down principle targets = %v %v, want none", targets, err)
	}
	balancer.override.Put("b") // an override still wins
	if got := pick(); got != "b" {
		t.Fatalf("override pick = %q, want b", got)
	}
}

// Strategies without the state report it as unknown, not inactive.
func TestGetFallbackIsUnreportedForOtherStrategies(t *testing.T) {
	balancer, err := (&BalancingRule{OutboundSelector: []string{"a"}, Strategy: "roundrobin", FallbackTag: "fb"}).Build(&luaRouteOutboundManager{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := &Router{}
	router.balancers.Store(&map[string]*Balancer{"rr": balancer})
	tag, active, reported, err := router.GetFallback("rr")
	if err != nil || tag != "fb" || active || reported {
		t.Fatalf("roundrobin GetFallback = %q %v %v %v", tag, active, reported, err)
	}
	if _, _, _, err := router.GetFallback("nope"); err == nil {
		t.Fatal("GetFallback on an unknown balancer succeeded")
	}
}
