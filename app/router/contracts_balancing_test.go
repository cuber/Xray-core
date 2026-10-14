package router

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/routing"
	"google.golang.org/protobuf/proto"
)

type contractObserver struct {
	result proto.Message
	err    error
}

func (*contractObserver) Type() interface{} { return extension.ObservatoryType() }
func (*contractObserver) Start() error      { return nil }
func (*contractObserver) Close() error      { return nil }
func (o *contractObserver) GetObservation(context.Context) (proto.Message, error) {
	return o.result, o.err
}

func TestContractWeighted600(t *testing.T) {
	for _, workers := range []int{1, 12} {
		t.Run(fmt.Sprintf("workers%d", workers), func(t *testing.T) {
			s := NewWeightedLeastPingStrategy(&StrategyWeightedLeastPingConfig{Weights: []*StrategyWeight{{Match: "a", Value: 1.5}, {Match: "b", Value: 1}, {Match: "c", Value: 0.5}}})
			s.ctx = context.Background()
			s.observer = &contractObserver{result: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{weightedStatus("a", time.Millisecond, 20, 0), weightedStatus("b", time.Millisecond, 20, 0), weightedStatus("c", time.Millisecond, 20, 0)}}}
			counts := map[string]int{}
			var mu sync.Mutex
			var wg sync.WaitGroup
			for range workers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range 600 / workers {
						tag := s.PickOutbound([]string{"c", "b", "a"})
						mu.Lock()
						counts[tag]++
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			if !reflect.DeepEqual(counts, map[string]int{"a": 300, "b": 200, "c": 100}) {
				t.Fatalf("600 selections=%v", counts)
			}
			t.Logf("weights=1.5:1:0.5 selections=%v total=600", counts)
		})
	}
}

func TestContractWeightedThresholdMatrix(t *testing.T) {
	cases := []struct {
		name   string
		cfg    *StrategyWeightedLeastPingConfig
		status *observatory.OutboundStatus
		want   bool
	}{
		{"max249", &StrategyWeightedLeastPingConfig{MaxRTT: int64(250 * time.Millisecond)}, weightedStatus("b", 249*time.Millisecond, 20, 0), true},
		{"max250", &StrategyWeightedLeastPingConfig{MaxRTT: int64(250 * time.Millisecond)}, weightedStatus("b", 250*time.Millisecond, 20, 0), false},
		{"max251", &StrategyWeightedLeastPingConfig{MaxRTT: int64(250 * time.Millisecond)}, weightedStatus("b", 251*time.Millisecond, 20, 0), false},
		{"relative149", &StrategyWeightedLeastPingConfig{RttTolerance: int64(50 * time.Millisecond)}, weightedStatus("b", 149*time.Millisecond, 20, 0), true},
		{"relative150", &StrategyWeightedLeastPingConfig{RttTolerance: int64(50 * time.Millisecond)}, weightedStatus("b", 150*time.Millisecond, 20, 0), true},
		{"relative151", &StrategyWeightedLeastPingConfig{RttTolerance: int64(50 * time.Millisecond)}, weightedStatus("b", 151*time.Millisecond, 20, 0), false},
		{"relativeDisabled", &StrategyWeightedLeastPingConfig{}, weightedStatus("b", time.Second, 20, 0), true},
		{"failureBelow", &StrategyWeightedLeastPingConfig{Tolerance: 0.2}, weightedStatus("b", 100*time.Millisecond, 20, 3), true},
		{"failureEqual", &StrategyWeightedLeastPingConfig{Tolerance: 0.2}, weightedStatus("b", 100*time.Millisecond, 20, 4), true},
		{"failureAbove", &StrategyWeightedLeastPingConfig{Tolerance: 0.2}, weightedStatus("b", 100*time.Millisecond, 20, 5), false},
		{"failureDisabled", &StrategyWeightedLeastPingConfig{}, weightedStatus("b", 100*time.Millisecond, 20, 19), true},
		{"samplesBelow", &StrategyWeightedLeastPingConfig{MinSamples: 5}, weightedStatus("b", 100*time.Millisecond, 4, 0), false},
		{"samplesEqual", &StrategyWeightedLeastPingConfig{MinSamples: 5}, weightedStatus("b", 100*time.Millisecond, 5, 0), true},
		{"samplesAbove", &StrategyWeightedLeastPingConfig{MinSamples: 5}, weightedStatus("b", 100*time.Millisecond, 6, 0), true},
		{"missing", &StrategyWeightedLeastPingConfig{}, nil, false},
		{"dead", &StrategyWeightedLeastPingConfig{}, weightedStatus("b", 100*time.Millisecond, 20, 20), false},
		{"delayFallback", &StrategyWeightedLeastPingConfig{MaxRTT: int64(250 * time.Millisecond)}, &observatory.OutboundStatus{OutboundTag: "b", Alive: true, Delay: 249}, true},
		{"delayBoundary", &StrategyWeightedLeastPingConfig{MaxRTT: int64(250 * time.Millisecond)}, &observatory.OutboundStatus{OutboundTag: "b", Alive: true, Delay: 250}, false},
		{"delayInsufficientSamples", &StrategyWeightedLeastPingConfig{MinSamples: 2}, &observatory.OutboundStatus{OutboundTag: "b", Alive: true, Delay: 100}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewWeightedLeastPingStrategy(tc.cfg)
			statuses := []*observatory.OutboundStatus{weightedStatus("a", 100*time.Millisecond, 20, 0), tc.status}
			if tc.status != nil && tc.status.HealthPing != nil {
				tc.status.Delay = 9999
			} // Burst Average, not Delay, controls qualification.
			s.ctx = context.Background()
			s.observer = &contractObserver{result: &observatory.ObservationResult{Status: statuses}}
			want := []string{"a"}
			if tc.want {
				want = append(want, "b")
			}
			if got := s.GetPrincipleTarget([]string{"b", "a", "b", "a"}); !reflect.DeepEqual(got, want) {
				t.Fatalf("pool=%v want %v", got, want)
			}
			counts := map[string]int{}
			for range 60 {
				counts[s.PickOutbound([]string{"b", "a", "b"})]++
			}
			expected := map[string]int{"a": 60}
			if tc.want {
				expected = map[string]int{"a": 30, "b": 30}
			}
			if !reflect.DeepEqual(counts, expected) {
				t.Fatalf("selections=%v want %v", counts, expected)
			}
			t.Logf("pool=%v selections=%v", want, counts)
		})
	}
}

func TestContractWeightedFallbackAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		observer *contractObserver
	}{
		{"missing", &contractObserver{result: &observatory.ObservationResult{}}},
		{"error", &contractObserver{err: errors.New("observation unavailable")}},
		{"wrongType", &contractObserver{result: &Config{}}},
		{"dead", &contractObserver{result: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{weightedStatus("a", time.Millisecond, 20, 20), weightedStatus("b", time.Millisecond, 20, 20)}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewWeightedLeastPingStrategy(&StrategyWeightedLeastPingConfig{Weights: []*StrategyWeight{{Match: "zero", Value: 0}, {Match: "negative", Value: -1}}})
			s.ctx = context.Background()
			s.observer = tc.observer
			candidates := []string{"b", "zero", "a", "negative", "b"}
			if got := s.GetPrincipleTarget(candidates); !reflect.DeepEqual(got, []string{"a", "b"}) {
				t.Fatalf("fallback=%v", got)
			}
			counts := map[string]int{}
			for range 60 {
				counts[s.PickOutbound(candidates)]++
			}
			if !reflect.DeepEqual(counts, map[string]int{"a": 30, "b": 30}) {
				t.Fatalf("fallback counts=%v", counts)
			}
			s.PickOutbound([]string{"a"})
			if _, exists := s.current["b"]; exists {
				t.Fatal("removed node retained accumulated weight")
			}
			for range 60 {
				if got := s.PickOutbound([]string{"a"}); got != "a" {
					t.Fatalf("removed node selected: %s", got)
				}
			}
			counts = map[string]int{}
			for range 60 {
				counts[s.PickOutbound(candidates)]++
			}
			if !reflect.DeepEqual(counts, map[string]int{"a": 30, "b": 30}) {
				t.Fatalf("recovery counts=%v", counts)
			}
			if got := s.PickOutbound([]string{"zero", "negative"}); got != "" {
				t.Fatalf("nonpositive pool=%s", got)
			}
			if got := s.PickOutbound(nil); got != "" || len(s.current) != 0 {
				t.Fatalf("empty result=%q state=%v", got, s.current)
			}
		})
	}
}

type contractUserContext struct {
	routing.Context
	user string
}

func (c contractUserContext) GetUser() string { return c.user }

func TestContractUserMatchingEdges(t *testing.T) {
	for _, tc := range []struct {
		rule, user string
		want       bool
	}{
		{"admin@la.att", "admin@la.att", true}, {"admin@la.att", "Admin@la.att", false}, {"admin@la.att", "admin@LA.ATT", false},
		{"domain:la.att", "u@la.att", true}, {"domain:la.att", "u@LA.ATT", true}, {"domain:LA.ATT", "u@la.att", true},
		{"domain:la.att", "u@gmail.com@la.att", true}, {"domain:la.att", "u@x.la.att", false}, {"domain:la.att", "la.att", false},
		{"domain:la.att", "u@la.att@other", false}, {"domain:la.att", "u@", false}, {"domain:la.att", "", false},
		{"domain:", "u@", false}, {"domain:", "domain:", false}, {"domain:la.att", "@la.att", true},
		{"regexp:^u@la\\.att$", "u@la.att", true}, {"regexp:^u@la\\.att$", "U@la.att", false},
		{"regexp:[", "u@la.att", false}, {"regexp:[", "regexp:[", false}, {"regexp:", "regexp:", true},
		{"", "", false},
	} {
		t.Run(fmt.Sprintf("%s/%s", tc.rule, tc.user), func(t *testing.T) {
			got := NewUserMatcher([]string{tc.rule}).Apply(contractUserContext{user: tc.user})
			if got != tc.want {
				t.Fatalf("rule=%q user=%q got=%v want=%v", tc.rule, tc.user, got, tc.want)
			}
			t.Logf("rule=%q user=%q match=%v", tc.rule, tc.user, got)
		})
	}
}

func TestContractLeastLoadToleranceBoundary(t *testing.T) {
	for _, tol := range []float32{0, 0.2} {
		for _, fail := range []int64{3, 4, 5} {
			t.Run(fmt.Sprintf("tolerance%g/fail%d", tol, fail), func(t *testing.T) {
				s := &LeastLoadStrategy{settings: &StrategyLeastLoadConfig{Tolerance: tol, MaxRTT: int64(time.Second)}}
				status := weightedStatus("a", 100*time.Millisecond, 20, fail)
				want := tol == 0 || fail <= 4
				if got := s.shouldSelectNode(status, []string{"a"}); got != want {
					t.Fatalf("selected=%v want %v", got, want)
				}
			})
		}
	}
}

func TestContractWeightedLiteralFirstMatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		weights []*StrategyWeight
		want    float64
	}{
		{"plainFirst", []*StrategyWeight{{Match: "node", Value: 1.5}, {Regexp: true, Match: "node-[0-9]+", Value: 5}}, 1.5},
		{"regexFirst", []*StrategyWeight{{Regexp: true, Match: "node-[0-9]+", Value: 2}, {Match: "node", Value: 5}}, 2},
		{"invalidRegexIgnored", []*StrategyWeight{{Regexp: true, Match: "[", Value: 9}, {Match: "node", Value: 3}}, 3},
		{"zeroFirstDoesNotFallThrough", []*StrategyWeight{{Regexp: true, Match: "node-[0-9]+", Value: 0}, {Match: "node", Value: 5}}, 0},
		{"negativeFirstDoesNotFallThrough", []*StrategyWeight{{Match: "node", Value: -1}, {Regexp: true, Match: "node-[0-9]+", Value: 5}}, -1},
		{"default", []*StrategyWeight{{Match: "other", Value: 5}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewWeightedLeastPingStrategy(&StrategyWeightedLeastPingConfig{Weights: tc.weights})
			for range 2 {
				if got := s.weightFor("node-12"); got != tc.want {
					t.Fatalf("literal first match=%v want %v", got, tc.want)
				}
			}
			// Numeric tag suffixes must never resurrect excluded weights.
			nodes := s.filterNodes([]string{"node-12"}, nil)
			if (len(nodes) == 1) != (tc.want > 0) {
				t.Fatalf("pool=%v for weight %v", nodes, tc.want)
			}
		})
	}
	for _, value := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			s := NewWeightedLeastPingStrategy(&StrategyWeightedLeastPingConfig{Weights: []*StrategyWeight{{Match: "bad", Value: float32(value)}}})
			for range 10 {
				pool := s.filterNodes([]string{"bad", "good"}, nil)
				if len(pool) != 1 || pool[0].Tag != "good" || s.pickWeighted(pool) != "good" {
					t.Fatalf("nonfinite/nonpositive weight %v entered pool: %v", value, pool)
				}
			}
			if got := s.pickWeighted(s.filterNodes([]string{"bad"}, nil)); got != "" {
				t.Fatalf("invalid-only selection=%s", got)
			}
		})
	}
}
