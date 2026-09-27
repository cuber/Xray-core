package router

import (
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common/serial"
)

func weightedStatus(tag string, average time.Duration, all, fail int64) *observatory.OutboundStatus {
	return &observatory.OutboundStatus{
		Alive:       all > fail,
		Delay:       average.Milliseconds(),
		OutboundTag: tag,
		HealthPing: &observatory.HealthPingMeasurementResult{
			All:     all,
			Fail:    fail,
			Average: int64(average),
		},
	}
}

func newWeightedTestStrategy(settings *StrategyWeightedLeastPingConfig) *WeightedLeastPingStrategy {
	return NewWeightedLeastPingStrategy(settings)
}

func TestWeightedLeastPingBalancerBuild(t *testing.T) {
	rule := &BalancingRule{
		Strategy: "weightedleastping",
		StrategySettings: serial.ToTypedMessage(&StrategyWeightedLeastPingConfig{
			MaxRTT: int64(250 * time.Millisecond),
		}),
	}
	balancer, err := rule.Build(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := balancer.strategy.(*WeightedLeastPingStrategy); !ok {
		t.Fatalf("strategy type = %T, want *WeightedLeastPingStrategy", balancer.strategy)
	}
}

func TestWeightedLeastPingFiltersHealthPool(t *testing.T) {
	strategy := newWeightedTestStrategy(&StrategyWeightedLeastPingConfig{
		MaxRTT:       int64(500 * time.Millisecond),
		RttTolerance: int64(100 * time.Millisecond),
		Tolerance:    0.2,
		MinSamples:   5,
	})
	candidates := []string{"best", "boundary", "relative-slow", "hard-slow", "failing", "warming", "dead"}
	statuses := []*observatory.OutboundStatus{
		weightedStatus("best", 100*time.Millisecond, 20, 0),
		weightedStatus("boundary", 200*time.Millisecond, 20, 4),
		weightedStatus("relative-slow", 201*time.Millisecond, 20, 0),
		weightedStatus("hard-slow", 500*time.Millisecond, 20, 0),
		weightedStatus("failing", 120*time.Millisecond, 20, 5),
		weightedStatus("warming", 110*time.Millisecond, 4, 0),
		weightedStatus("dead", 90*time.Millisecond, 20, 20),
		weightedStatus("not-a-candidate", 80*time.Millisecond, 20, 0),
	}

	nodes := strategy.filterNodes(candidates, statuses)
	if len(nodes) != 2 {
		t.Fatalf("filterNodes() returned %d nodes, want 2", len(nodes))
	}
	if nodes[0].Tag != "best" || nodes[1].Tag != "boundary" {
		t.Fatalf("filterNodes() tags = [%s %s], want [best boundary]", nodes[0].Tag, nodes[1].Tag)
	}
}

func TestWeightedLeastPingWithoutBurstStatsUsesDelay(t *testing.T) {
	strategy := newWeightedTestStrategy(&StrategyWeightedLeastPingConfig{
		MaxRTT:       int64(300 * time.Millisecond),
		RttTolerance: int64(50 * time.Millisecond),
	})
	statuses := []*observatory.OutboundStatus{
		{Alive: true, Delay: 100, OutboundTag: "a"},
		{Alive: true, Delay: 150, OutboundTag: "b"},
		{Alive: true, Delay: 151, OutboundTag: "c"},
	}

	nodes := strategy.filterNodes([]string{"a", "b", "c"}, statuses)
	if len(nodes) != 2 || nodes[0].Tag != "a" || nodes[1].Tag != "b" {
		t.Fatalf("unexpected fallback-delay nodes: %#v", nodes)
	}
}

func TestWeightedLeastPingFallsBackToAllCandidatesWhenAllUnhealthy(t *testing.T) {
	strategy := newWeightedTestStrategy(&StrategyWeightedLeastPingConfig{
		Weights: []*StrategyWeight{
			{Match: "a", Value: 5},
			{Match: "b", Value: 3},
			{Match: "c", Value: 2},
		},
		MaxRTT:     int64(250 * time.Millisecond),
		Tolerance:  0.2,
		MinSamples: 5,
	})
	statuses := []*observatory.OutboundStatus{
		weightedStatus("a", 300*time.Millisecond, 20, 0),
		weightedStatus("b", 100*time.Millisecond, 20, 20),
	}

	nodes := strategy.filterNodes([]string{"c", "a", "b"}, statuses)
	if len(nodes) != 3 {
		t.Fatalf("fallback pool returned %d nodes, want 3", len(nodes))
	}
	if nodes[0].Tag != "a" || nodes[1].Tag != "b" || nodes[2].Tag != "c" {
		t.Fatalf("fallback pool tags = [%s %s %s], want [a b c]", nodes[0].Tag, nodes[1].Tag, nodes[2].Tag)
	}
	if nodes[0].Weight != 5 || nodes[1].Weight != 3 || nodes[2].Weight != 2 {
		t.Fatalf("fallback pool lost weights: %#v", nodes)
	}
}

func TestWeightedLeastPingFallsBackToAllCandidatesWithoutObservations(t *testing.T) {
	strategy := newWeightedTestStrategy(&StrategyWeightedLeastPingConfig{})
	nodes := strategy.filterNodes([]string{"b", "a"}, nil)
	if len(nodes) != 2 || nodes[0].Tag != "a" || nodes[1].Tag != "b" {
		t.Fatalf("unobserved fallback pool = %#v, want [a b]", nodes)
	}
}

func TestWeightedLeastPingDoesNotMixUnhealthyNodesIntoHealthyPool(t *testing.T) {
	strategy := newWeightedTestStrategy(&StrategyWeightedLeastPingConfig{
		MaxRTT:    int64(250 * time.Millisecond),
		Tolerance: 0.2,
	})
	statuses := []*observatory.OutboundStatus{
		weightedStatus("healthy", 100*time.Millisecond, 20, 0),
		weightedStatus("slow", 300*time.Millisecond, 20, 0),
		weightedStatus("failing", 120*time.Millisecond, 20, 10),
	}

	nodes := strategy.filterNodes([]string{"healthy", "slow", "failing", "unobserved"}, statuses)
	if len(nodes) != 1 || nodes[0].Tag != "healthy" {
		t.Fatalf("healthy pool = %#v, want only healthy", nodes)
	}
}

func TestWeightedLeastPingSmoothWeightedRoundRobin(t *testing.T) {
	strategy := newWeightedTestStrategy(&StrategyWeightedLeastPingConfig{
		Weights: []*StrategyWeight{
			{Match: "node-a", Value: 5},
			{Match: "node-b", Value: 3},
			{Match: "node-c", Value: 2},
		},
	})
	nodes := strategy.filterNodes(
		[]string{"node-c", "node-a", "node-b"},
		[]*observatory.OutboundStatus{
			weightedStatus("node-a", 100*time.Millisecond, 20, 0),
			weightedStatus("node-b", 100*time.Millisecond, 20, 0),
			weightedStatus("node-c", 100*time.Millisecond, 20, 0),
		},
	)

	counts := map[string]int{}
	for range 100 {
		counts[strategy.pickWeighted(nodes)]++
	}
	if counts["node-a"] != 50 || counts["node-b"] != 30 || counts["node-c"] != 20 {
		t.Fatalf("weighted counts = %#v, want node-a=50 node-b=30 node-c=20", counts)
	}
}

func TestWeightedLeastPingConcurrentSelectionPreservesRatio(t *testing.T) {
	strategy := newWeightedTestStrategy(&StrategyWeightedLeastPingConfig{
		Weights: []*StrategyWeight{
			{Match: "a", Value: 3},
			{Match: "b", Value: 1},
		},
	})
	nodes := []*weightedLeastPingNode{
		{Tag: "a", Weight: 3},
		{Tag: "b", Weight: 1},
	}
	counts := map[string]int{}
	var countsMu sync.Mutex
	var wg sync.WaitGroup
	for range 400 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tag := strategy.pickWeighted(nodes)
			countsMu.Lock()
			counts[tag]++
			countsMu.Unlock()
		}()
	}
	wg.Wait()
	if counts["a"] != 300 || counts["b"] != 100 {
		t.Fatalf("concurrent weighted counts = %#v, want a=300 b=100", counts)
	}
}

func TestWeightedLeastPingNodeRemovalAndRecovery(t *testing.T) {
	strategy := newWeightedTestStrategy(&StrategyWeightedLeastPingConfig{})
	all := []*weightedLeastPingNode{
		{Tag: "a", Weight: 5},
		{Tag: "b", Weight: 3},
		{Tag: "c", Weight: 2},
	}
	remaining := []*weightedLeastPingNode{
		{Tag: "b", Weight: 3},
		{Tag: "c", Weight: 2},
	}

	for range 10 {
		strategy.pickWeighted(all)
	}
	counts := map[string]int{}
	for range 5 {
		counts[strategy.pickWeighted(remaining)]++
	}
	if counts["b"] != 3 || counts["c"] != 2 {
		t.Fatalf("remaining-node counts = %#v, want b=3 c=2", counts)
	}

	counts = map[string]int{}
	for range 10 {
		counts[strategy.pickWeighted(all)]++
	}
	if counts["a"] != 5 || counts["b"] != 3 || counts["c"] != 2 {
		t.Fatalf("recovered-node counts = %#v, want a=5 b=3 c=2", counts)
	}
}

func TestWeightedLeastPingEmptyPool(t *testing.T) {
	strategy := newWeightedTestStrategy(&StrategyWeightedLeastPingConfig{})
	if got := strategy.pickWeighted(nil); got != "" {
		t.Fatalf("pickWeighted(nil) = %q, want empty", got)
	}
}
