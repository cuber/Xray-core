package router

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/features/routing"
)

// Core spec020: balancer picks on the control plane.

// rows renders a level's candidates as GetMembers reports them over o's observation.
func rows(t *testing.T, m map[string]*Balancer, o *contractObserver, tag string) string {
	t.Helper()
	members, err := balancerView(m).members(m[tag], o.result.(*observatory.ObservationResult).Status)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, member := range members {
		out = append(out, row(member))
	}
	return strings.Join(out, " ")
}

func row(m routing.BalancerMember) string {
	status := "unknown"
	if m.Status.Known {
		status = "down"
		if m.Status.Alive {
			status = fmt.Sprintf("%dms", m.Status.Delay)
		}
	}
	kind := ""
	if m.Balancer {
		kind = "[B]"
	}
	weight := ""
	if m.Weight != 0 {
		weight = fmt.Sprintf(" w%g", m.Weight)
	}
	return fmt.Sprintf("%s%s:%s%s:%.3f", m.Tag, kind, status, weight, m.Share)
}

// hqShape is spec021's shape: a weightedLeastPing route over an outbound and two leastPing nodes.
func hqShape(t *testing.T, o *contractObserver) map[string]*Balancer {
	return memberMap(t, o,
		level{
			tag: "route", strategy: "weightedleastping", fallback: "block", selector: []string{"x"},
			members: []string{"a", "b"}, weights: map[string]float32{"a": 2, "b": 1, "x": 1},
		},
		level{tag: "a", strategy: "leastping", fallback: "a-hy2", selector: []string{"a-hy2", "a-anytls"}},
		level{tag: "b", strategy: "leastping", fallback: "b-hy2", selector: []string{"b-hy2", "b-anytls"}},
	)
}

// FR-001 (P1), FR-002 (P2): every candidate's status as its level reads it, and its share of the
// level's picks; x is not observed, b's Hy2 is down.
func TestCandidatesCarryStatusAndShare(t *testing.T) {
	o := observedAs(map[string]time.Duration{
		"a-hy2": 50 * time.Millisecond, "a-anytls": 80 * time.Millisecond, "b-anytls": 30 * time.Millisecond,
	}, "b-hy2")
	m := hqShape(t, o)
	for tag, want := range map[string]string{
		"route": "x:unknown w1:0.000 a[B]:50ms w2:0.667 b[B]:30ms w1:0.333",
		"a":     "a-hy2:50ms:1.000 a-anytls:80ms:0.000",
		"b":     "b-hy2:down:0.000 b-anytls:30ms:1.000",
	} {
		if got := rows(t, m, o, tag); got != want {
			t.Errorf("%s: %s\n  want %s", tag, got, want)
		}
	}
	if got, err := balancerView(m).GetStrategy("route"); err != nil || got != "weightedleastping" {
		t.Errorf("strategy = %q, %v", got, err)
	}
}

// FR-002: weightedLeastPing's shares are the fractions its picks take.
func TestWeightedSharesMatchThePicks(t *testing.T) {
	o := observedAs(map[string]time.Duration{
		"a-hy2": 50 * time.Millisecond, "b-anytls": 30 * time.Millisecond, "x": 60 * time.Millisecond,
	})
	m := hqShape(t, o)
	members, err := balancerView(m).members(m["route"], o.result.(*observatory.ObservationResult).Status)
	if err != nil {
		t.Fatal(err)
	}
	const n = 800
	count := map[string]int{}
	for range n {
		tag, err := m["route"].PickOutbound()
		if err != nil {
			t.Fatal(err)
		}
		count[tag]++
	}
	for _, member := range members {
		if got := float64(count[member.Tag]) / n; math.Abs(got-member.Share) > 1e-9 {
			t.Errorf("%s: picked %.3f of the time, share %.3f", member.Tag, got, member.Share)
		}
	}
}

// FR-002: shares for every strategy, an override and an active fallback.
func TestSharesForEveryStrategy(t *testing.T) {
	o := observedAs(map[string]time.Duration{"p": 30 * time.Millisecond, "q": 10 * time.Millisecond}, "d")
	for strategy, want := range map[string]string{
		"leastping":  "p:30ms:0.000 q:10ms:1.000 d:down:0.000",
		"roundrobin": "p:30ms:0.500 q:10ms:0.500 d:down:0.000",
		"random":     "p:30ms:0.500 q:10ms:0.500 d:down:0.000",
	} {
		t.Run(strategy, func(t *testing.T) {
			m := memberMap(t, o, level{tag: "l", strategy: strategy, fallback: "block", selector: []string{"p", "q", "d"}})
			if got := rows(t, m, o, "l"); got != want {
				t.Fatalf("%s\n  want %s", got, want)
			}
			m["l"].override.Put("d")
			if got := rows(t, m, o, "l"); got != "p:30ms:0.000 q:10ms:0.000 d:down:1.000" {
				t.Errorf("override on a candidate: %s", got)
			}
			m["l"].override.Put("elsewhere")
			if got := rows(t, m, o, "l"); got != "p:30ms:0.000 q:10ms:0.000 d:down:0.000" {
				t.Errorf("override elsewhere: %s", got)
			}
		})
	}
	t.Run("leastload", func(t *testing.T) {
		m := memberMap(t, o, level{tag: "l", strategy: "leastload", fallback: "block", selector: []string{"p", "q", "d"}})
		members, err := balancerView(m).members(m["l"], o.result.(*observatory.ObservationResult).Status)
		if err != nil {
			t.Fatal(err)
		}
		total, picked := 0.0, map[string]bool{}
		for _, member := range members {
			total += member.Share
			picked[member.Tag] = member.Share > 0
		}
		if math.Abs(total-1) > 1e-9 {
			t.Fatalf("shares sum to %v", total)
		}
		for range 50 {
			if tag, _ := m["l"].PickOutbound(); !picked[tag] {
				t.Fatalf("picked %s, outside its shares %v", tag, picked)
			}
		}
	})
	t.Run("fallback", func(t *testing.T) {
		down := observedAs(nil, "p", "q")
		for _, strategy := range []string{"leastping", "weightedleastping", "roundrobin"} {
			m := memberMap(t, down, level{tag: "l", strategy: strategy, fallback: "block", selector: []string{"p", "q"}})
			got := rows(t, m, down, "l")
			if strings.Contains(got, "1.000") || strings.Contains(got, "0.500") {
				t.Errorf("%s with every candidate down: %s", strategy, got)
			}
			if tag, _ := m["l"].PickOutbound(); tag != "block" {
				t.Errorf("%s picks %s", strategy, tag)
			}
		}
	})
}

// FR-003 (P4): reading the shares changes no pick.
func TestReadingSharesChangesNoPick(t *testing.T) {
	o := observedAs(map[string]time.Duration{"p": 30 * time.Millisecond, "q": 10 * time.Millisecond, "r": 20 * time.Millisecond})
	for _, strategy := range []string{"roundrobin", "weightedleastping"} {
		t.Run(strategy, func(t *testing.T) {
			sequence := func(read bool) []string {
				m := memberMap(t, o, level{
					tag: "l", strategy: strategy, fallback: "block", selector: []string{"p", "q", "r"},
					weights: map[string]float32{"p": 3, "q": 2, "r": 1},
				})
				var out []string
				for range 24 {
					if read {
						rows(t, m, o, "l")
					}
					tag, _ := m["l"].PickOutbound()
					out = append(out, tag)
				}
				return out
			}
			if plain, read := sequence(false), sequence(true); !slices.Equal(plain, read) {
				t.Fatalf("picks changed by reading:\n  %v\n  %v", plain, read)
			}
		})
	}
}
