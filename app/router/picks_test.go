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
	"google.golang.org/protobuf/proto"
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

// P2: a weight is reported at the float32 precision the configuration has, not widened.
func TestWeightsAreReportedAsConfigured(t *testing.T) {
	o := observedAs(map[string]time.Duration{"p": 30 * time.Millisecond, "q": 10 * time.Millisecond})
	m := memberMap(t, o, level{
		tag: "l", strategy: "weightedleastping", fallback: "block", selector: []string{"p", "q"},
		weights: map[string]float32{"p": 0.1, "q": 0.8},
	})
	members, err := balancerView(m).members(m["l"], o.result.(*observatory.ObservationResult).Status)
	if err != nil {
		t.Fatal(err)
	}
	if members[0].Weight != 0.1 || members[1].Weight != 0.8 {
		t.Fatalf("weights %v %v", members[0].Weight, members[1].Weight)
	}
	if math.Abs(members[0].Share-1.0/9) > 1e-6 || math.Abs(members[1].Share-8.0/9) > 1e-6 {
		t.Fatalf("shares %v %v", members[0].Share, members[1].Share)
	}
}

// Core spec020 2026-11-08 amendment (P6-P8): where a status comes from.

// provenanceObservation is an observation as the burst observatory's createResult leaves it, with a classic
// observatory's failed probe added: p and s are probed (s alive with a stale error); t tracks p; r tracks p
// and is a relay the server denies (spec024 R6); f is forced down (spec015) and ft tracks it; d is a burst
// probe dead by its measurement, keeping its recent average; c failed a classic probe. u is not observed.
func provenanceObservation() *contractObserver {
	health := func(all, fail int64) *observatory.HealthPingMeasurementResult {
		return &observatory.HealthPingMeasurementResult{All: all, Fail: fail, Average: int64(30 * time.Millisecond)}
	}
	forced := func(s *observatory.OutboundStatus) *observatory.OutboundStatus {
		s.ForceDown, s.ForceDownReason, s.ForceDownSince = true, "cdt", 1
		s.Alive, s.LastErrorReason = false, "force down: cdt"
		return s
	}
	return &contractObserver{result: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{
		{OutboundTag: "p", Alive: true, Delay: 30, HealthPing: health(5, 0)},
		{OutboundTag: "s", Alive: true, Delay: 20, LastErrorReason: "stale", HealthPing: health(5, 0)},
		forced(&observatory.OutboundStatus{OutboundTag: "f", Delay: 40, HealthPing: health(5, 0)}),
		{OutboundTag: "d", Alive: false, Delay: 25, HealthPing: health(5, 4)},
		{OutboundTag: "c", Alive: false, LastErrorReason: "probe failed: connection refused"},
		{OutboundTag: "t", Alive: true, Delay: 30, TrackedFrom: "p", HealthPing: health(5, 0)},
		{OutboundTag: "r", Alive: false, Delay: 30, TrackedFrom: "p", LastErrorReason: "hysteria relay denied", HealthPing: health(5, 0)},
		forced(&observatory.OutboundStatus{OutboundTag: "ft", Delay: 40, TrackedFrom: "f", HealthPing: health(5, 0)}),
	}}}
}

func statusesOf(o *contractObserver) []*observatory.OutboundStatus {
	return o.result.(*observatory.ObservationResult).Status
}

// provenanceOf renders a status with its delay, source and reason.
func provenanceOf(s routing.BalancerStatus) string {
	state := "unknown"
	if s.Known {
		state = "down"
		if s.Alive {
			state = "alive"
		}
	}
	out := fmt.Sprintf("%s %dms", state, s.Delay)
	if s.TrackedFrom != "" {
		out += " <" + s.TrackedFrom
	}
	if s.Reason != "" {
		out += " (" + s.Reason + ")"
	}
	if !s.Provenance {
		out += " no-provenance"
	}
	return out
}

// FR-006 (P6), FR-008 (P7): an outbound candidate's provenance and reason are its observation's, and its
// delay is as observed, a dead one's included.
func TestOutboundCandidatesCarryProvenance(t *testing.T) {
	o := provenanceObservation()
	m := memberMap(t, o, level{
		tag: "l", strategy: "leastping", fallback: "block",
		selector: []string{"p", "s", "t", "r", "f", "ft", "d", "c", "u"},
	})
	members, err := balancerView(m).members(m["l"], statusesOf(o))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"p":  "alive 30ms",
		"s":  "alive 20ms",
		"t":  "alive 30ms <p",
		"r":  "down 30ms <p (hysteria relay denied)",
		"f":  "down 40ms (force down: cdt)",
		"ft": "down 40ms <f (force down: cdt)",
		"d":  "down 25ms",
		"c":  "down 0ms (probe failed: connection refused)",
		"u":  "unknown 0ms",
	}
	if len(members) != len(want) {
		t.Fatalf("%d candidates", len(members))
	}
	for _, member := range members {
		if got := provenanceOf(member.Status); got != want[member.Tag] {
			t.Errorf("%s: %s, want %s", member.Tag, got, want[member.Tag])
		}
	}

	// Without an observatory (no context here), every status is unknown, still with its provenance.
	view := balancerView(m)
	if got, err := view.GetStatus("l"); err != nil || provenanceOf(got) != "unknown 0ms" {
		t.Errorf("level without an observatory: %s, %v", provenanceOf(got), err)
	}
	all, err := view.GetMembers("l")
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range all {
		if got := provenanceOf(member.Status); got != "unknown 0ms" {
			t.Errorf("%s without an observatory: %s", member.Tag, got)
		}
	}
}

// FR-007 (P6): a member balancer's and a level's own status carry no tracked_from and, while dead, the
// reason of the status they bubble up: the override target's, the fallback's, the first down candidate's.
func TestBalancerStatusesCarryProvenance(t *testing.T) {
	o := provenanceObservation()
	m := memberMap(t, o,
		level{tag: "ov", strategy: "leastping", fallback: "block", selector: []string{"p", "ft"}},
		level{tag: "to-outbound", strategy: "leastping", fallback: "c", selector: []string{"r", "d"}},
		level{tag: "first", strategy: "leastping", fallback: "block", selector: []string{"r", "f"}},
		level{tag: "to-balancer", strategy: "leastping", fallback: "first", selector: []string{"d"}},
		level{tag: "up", strategy: "leastping", fallback: "block", selector: []string{"t", "d"}},
		level{tag: "route", strategy: "leastping", fallback: "block", selector: []string{"p"}, members: []string{"first", "up", "ov"}},
		level{tag: "all-down", strategy: "roundrobin", fallback: "block", members: []string{"first", "ov"}},
	)
	m["ov"].override.Put("ft")
	view := balancerView(m)
	for tag, want := range map[string]string{
		"ov":          "down 40ms (force down: cdt)",
		"to-outbound": "down 0ms (probe failed: connection refused)",
		"first":       "down 0ms (hysteria relay denied)",
		"to-balancer": "down 0ms (hysteria relay denied)",
		"up":          "alive 30ms",
		"route":       "alive 30ms",
		"all-down":    "down 0ms (hysteria relay denied)",
	} {
		if got := provenanceOf(view.status(m[tag], statusesOf(o))); got != want {
			t.Errorf("level %s: %s, want %s", tag, got, want)
		}
	}
	members, err := view.members(m["route"], statusesOf(o))
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, member := range members {
		rows = append(rows, member.Tag+": "+provenanceOf(member.Status))
	}
	if got, want := strings.Join(rows, "; "),
		"p: alive 30ms; first: down 0ms (hysteria relay denied); up: alive 30ms; ov: down 40ms (force down: cdt)"; got != want {
		t.Errorf("route's members:\n  %s\n  want %s", got, want)
	}
}

// FR-008 (P8): reading provenance changes no observation, no share and no pick.
func TestReadingProvenanceChangesNoPick(t *testing.T) {
	for _, strategy := range []string{"roundrobin", "weightedleastping"} {
		t.Run(strategy, func(t *testing.T) {
			sequence := func(read bool) ([]string, []float64) {
				o := provenanceObservation()
				before := proto.Clone(o.result)
				m := memberMap(t, o,
					level{
						tag: "l", strategy: strategy, fallback: "block", selector: []string{"p", "s", "t", "r", "ft", "d"},
						weights: map[string]float32{"p": 3, "s": 2, "t": 1},
					},
					level{tag: "top", strategy: "leastping", fallback: "block", members: []string{"l"}},
				)
				view := balancerView(m)
				var picks []string
				var shares []float64
				for range 24 {
					if read {
						for _, tag := range []string{"l", "top"} {
							members, err := view.members(m[tag], statusesOf(o))
							if err != nil {
								t.Fatal(err)
							}
							if tag == "l" {
								shares = shares[:0]
								for _, member := range members {
									shares = append(shares, member.Share)
								}
							}
							view.status(m[tag], statusesOf(o))
						}
					}
					tag, _ := m["l"].PickOutbound()
					picks = append(picks, tag)
				}
				if !proto.Equal(before, o.result) {
					t.Fatalf("the observation changed by reading")
				}
				return picks, shares
			}
			plain, _ := sequence(false)
			read, shares := sequence(true)
			if !slices.Equal(plain, read) {
				t.Fatalf("picks changed by reading:\n  %v\n  %v", plain, read)
			}
			if total := shares[0] + shares[1] + shares[2]; math.Abs(total-1) > 1e-9 || shares[3] != 0 || shares[4] != 0 || shares[5] != 0 {
				t.Fatalf("shares %v", shares)
			}
		})
	}
}
