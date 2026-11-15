package router

import (
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
)

// Core spec027 T003: the pure decision, with literal inputs and literal expectations (Q, Y cases at
// the function level).

func ms(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }

func up(tag string, rtt time.Duration) tlpOption {
	return tlpOption{tag: tag, known: true, usable: true, rtt: rtt}
}

func down(tag string) tlpOption { return tlpOption{tag: tag, known: true, reason: reasonProbeDown} }

func waiting(tag string) tlpOption {
	return tlpOption{tag: tag, reason: reasonUnobserved, pending: true}
}

func limits(maxRTT, recoverRTT time.Duration) thresholdPolicy {
	return thresholdPolicy{maxRTT: maxRTT, recoverRTT: recoverRTT}
}

func burstStatus(tag string, alive bool, average time.Duration, all, fail int64) *observatory.OutboundStatus {
	return &observatory.OutboundStatus{
		OutboundTag: tag, Alive: alive, Delay: average.Milliseconds(),
		HealthPing: &observatory.HealthPingMeasurementResult{All: all, Fail: fail, Average: int64(average)},
	}
}

type outcome struct {
	mode     tlpMode
	choice   string
	fallback bool
	terminal tlpTerminal
}

func decided(d tlpDecision) outcome {
	return outcome{mode: d.mode, choice: d.choice, fallback: d.fallback, terminal: d.terminal}
}

func decide50(candidates []tlpOption, fallback *tlpOption, blackhole string) tlpDecision {
	d, _ := decideThreshold(limits(ms(50), ms(50)), candidates, fallback, blackhole, nil)
	return d
}

func fb(o tlpOption) *tlpOption { return &o }

func TestThresholdDecisionPhases(t *testing.T) {
	for _, c := range []struct {
		id         string
		candidates []tlpOption
		fallback   *tlpOption
		blackhole  string
		want       outcome
	}{
		{"Q01", []tlpOption{up("A", ms(10)), up("B", ms(20))}, fb(up("F", ms(5))), "", outcome{modePrimary, "A", false, terminalNone}},
		{"Q02", []tlpOption{up("A", ms(49.999999)), up("B", ms(60))}, nil, "", outcome{modePrimary, "A", false, terminalNone}},
		{"Q03", []tlpOption{up("A", ms(50)), up("B", ms(60))}, fb(up("F", ms(40))), "", outcome{modeFallback, "F", true, terminalNone}},
		{"Q05", []tlpOption{optionFromStatus("A", &observatory.OutboundStatus{Alive: false, Delay: 1}), up("B", ms(30))}, nil, "", outcome{modePrimary, "B", false, terminalNone}},
		{"Q06", []tlpOption{up("A", ms(80)), up("B", ms(70))}, fb(up("F", ms(120))), "", outcome{modeDegraded, "B", false, terminalNone}},
		{"Q07", []tlpOption{up("A", ms(80)), up("B", ms(90))}, fb(up("F", ms(70))), "", outcome{modeDegraded, "F", true, terminalNone}},
		{"Q08", []tlpOption{up("A", ms(80))}, fb(up("F", ms(80))), "", outcome{modeDegraded, "A", false, terminalNone}},
		{"Q09", []tlpOption{up("A", ms(20)), up("B", ms(20))}, nil, "", outcome{modePrimary, "A", false, terminalNone}},
		{"Q09r", []tlpOption{up("B", ms(20)), up("A", ms(20))}, nil, "", outcome{modePrimary, "B", false, terminalNone}},
		{"Q10a", []tlpOption{up("A", ms(30))}, nil, "", outcome{modePrimary, "A", false, terminalNone}},
		{"Q10b", []tlpOption{up("A", ms(80))}, nil, "", outcome{modeDegraded, "A", false, terminalNone}},
		{"Q10c", []tlpOption{down("A")}, nil, "", outcome{modeUnavailable, "", false, terminalDrop}},
		{"Q11", []tlpOption{down("A"), down("B")}, fb(down("F")), "", outcome{modeUnavailable, "", false, terminalDrop}},
		{"Q12", []tlpOption{down("A"), down("B")}, nil, "sink", outcome{modeUnavailable, "sink", true, terminalBlackhole}},
		{"Q13", []tlpOption{down("A")}, fb(down("block")), "", outcome{modeUnavailable, "", false, terminalDrop}},
		{"Q18", []tlpOption{waiting("A"), down("B")}, nil, "", outcome{modePending, "", false, terminalDrop}},
		{"Q18b", []tlpOption{down("A")}, fb(waiting("F")), "sink", outcome{modePending, "sink", true, terminalBlackhole}},
		{"Q20", []tlpOption{up("A", ms(80))}, fb(tlpOption{tag: "F", known: true, reason: reasonBalancerUnavailable}), "", outcome{modeDegraded, "A", false, terminalNone}},
		{"Q21", []tlpOption{optionFromStatus("A", &observatory.OutboundStatus{Alive: false, Delay: 1}), up("B", ms(80))}, fb(up("F", ms(90))), "", outcome{modeDegraded, "B", false, terminalNone}},
		{"Q22", []tlpOption{down("A"), down("B")}, fb(tlpOption{tag: "F", known: true, reason: reasonBalancerUnavailable}), "", outcome{modeUnavailable, "", false, terminalDrop}},
		{"Q24a", []tlpOption{up("A", ms(30.9)), up("B", ms(30.1))}, nil, "", outcome{modePrimary, "A", false, terminalNone}},
		{"Q24b", []tlpOption{up("A", ms(30.9)), up("B", ms(29.9))}, nil, "", outcome{modePrimary, "B", false, terminalNone}},
	} {
		if got := decided(decide50(c.candidates, c.fallback, c.blackhole)); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.id, got, c.want)
		}
	}
}

// Q01-Q04, Q15-Q17, Q23: eligibility and reasons per option.
func TestThresholdSelections(t *testing.T) {
	d := decide50([]tlpOption{up("A", ms(49.999999)), up("B", ms(60))}, fb(up("F", ms(5))), "")
	if a, b := d.candidates[0], d.candidates[1]; !a.withinLimit || a.reason != reasonEligible || b.withinLimit ||
		b.reason != reasonMaxRTTExceeded || !b.usable || !b.rttKnown || b.rtt != ms(60) {
		t.Fatalf("Q02 selections %+v %+v", a, b)
	}
	if f := d.fallbackOf; f == nil || !f.withinLimit {
		t.Fatalf("Q01 fallback selection %+v", f)
	}
	// Q04: the exact burst average decides, not the truncated Delay.
	exact := ms(50) + time.Nanosecond
	o := optionFromStatus("A", burstStatus("A", true, exact, 5, 0))
	d, _ = decideThreshold(limits(exact, exact), []tlpOption{o}, nil, "", nil)
	if d.candidates[0].withinLimit || o.rtt != exact {
		t.Fatalf("Q04 rtt %v selection %+v", o.rtt, d.candidates[0])
	}
	for _, c := range []struct {
		id     string
		status *observatory.OutboundStatus
		usable bool
		rtt    time.Duration
		reason string
	}{
		{"Q14", burstStatus("A", true, 0, 3, 3), false, 0, reasonRTTUnavailable},
		{"Q15", burstStatus("A", true, 300*time.Microsecond, 3, 0), true, 300 * time.Microsecond, ""},
		{"Q16-49", &observatory.OutboundStatus{Alive: true, Delay: 49}, true, ms(49), ""},
		{"Q17-negative", burstStatus("A", true, -time.Millisecond, 3, 0), false, 0, reasonInvalidMeasurement},
		{"Q17-fail", burstStatus("A", true, ms(20), 2, 3), false, 0, reasonInvalidMeasurement},
		{"Q17-delay", &observatory.OutboundStatus{Alive: true, Delay: -1}, false, 0, reasonInvalidMeasurement},
		{"Q05", &observatory.OutboundStatus{Alive: false, Delay: 1}, false, 0, reasonProbeDown},
		{"unobserved", nil, false, 0, reasonUnobserved},
	} {
		got := optionFromStatus("A", c.status)
		if got.usable != c.usable || got.rtt != c.rtt || got.reason != c.reason {
			t.Errorf("%s: option %+v", c.id, got)
		}
	}
	// Q16: classic 49/50/51 against 50ms.
	for delay, within := range map[int64]bool{49: true, 50: false, 51: false} {
		o := optionFromStatus("A", &observatory.OutboundStatus{Alive: true, Delay: delay})
		if d := decide50([]tlpOption{o}, nil, ""); d.candidates[0].withinLimit != within {
			t.Errorf("Q16 classic %dms within %v", delay, d.candidates[0].withinLimit)
		}
	}
	// Q23: only the window average; latest and minimum samples are not read.
	s := burstStatus("A", true, ms(60), 25, 0)
	s.HealthPing.Min, s.HealthPing.Max = int64(ms(15)), int64(ms(90))
	if d := decide50([]tlpOption{optionFromStatus("A", s)}, nil, ""); d.candidates[0].withinLimit {
		t.Fatal("Q23: a 60ms average is over a 50ms limit whatever the minimum")
	}
	// Q24: fractional limits and means compare exactly.
	d, _ = decideThreshold(limits(ms(30.5), ms(30.5)), []tlpOption{up("A", ms(30.4))}, nil, "", nil)
	if !d.candidates[0].withinLimit {
		t.Fatal("Q24: 30.4ms is under 30.5ms")
	}
	mean := tlpOption{tag: "C", known: true, usable: true, rtt: ms(40.5), mean: &memberMean{measured: 2, dispatchable: 2}}
	d, _ = decideThreshold(limits(ms(40.5), ms(40.5)), []tlpOption{mean}, nil, "", nil)
	if d.candidates[0].withinLimit || d.candidates[0].mean == nil {
		t.Fatalf("Q24: a mean equal to the limit is over it: %+v", d.candidates[0])
	}
}

// Q19 at the function level: thresholds are per balancer.
func TestThresholdLimitsAreLocal(t *testing.T) {
	a := []tlpOption{up("A", ms(30))}
	strict, _ := decideThreshold(limits(ms(20), ms(20)), a, nil, "", nil)
	loose, _ := decideThreshold(limits(ms(100), ms(100)), a, nil, "", nil)
	if strict.candidates[0].withinLimit || !loose.candidates[0].withinLimit {
		t.Fatalf("Q19: 30ms within 20ms %v, within 100ms %v", strict.candidates[0].withinLimit, loose.candidates[0].withinLimit)
	}
}

// Y cases: each step is one observation and one computation.
func TestThresholdHysteresis(t *testing.T) {
	run := func(policy thresholdPolicy, steps []tlpOption, fallback tlpOption) []string {
		var latches map[string]bool
		var picks []string
		for _, a := range steps {
			var d tlpDecision
			d, latches = decideThreshold(policy, []tlpOption{a}, &fallback, "", latches)
			picks = append(picks, d.choice)
		}
		return picks
	}
	band := limits(ms(50), ms(40))
	f := up("F", ms(30))
	series := func(values ...float64) []tlpOption {
		var out []tlpOption
		for _, v := range values {
			out = append(out, up("A", ms(v)))
		}
		return out
	}
	for _, c := range []struct {
		id     string
		policy thresholdPolicy
		steps  []tlpOption
		want   string
	}{
		{"Y01", band, series(45, 52, 45, 41, 39), "A F F F A"},
		{"Y02", band, series(49.999999, 50, 40, 39.999999), "A F F A"},
		{"Y03", limits(ms(50), ms(50)), series(45, 52, 45, 41, 39), "A F A A A"},
		{"Y04", band, []tlpOption{up("A", ms(60)), down("A"), up("A", ms(45))}, "F F F"},
		{"Y05", band, series(45), "A"},
	} {
		if got := joined(run(c.policy, c.steps, f)); got != c.want {
			t.Errorf("%s: %s, want %s", c.id, got, c.want)
		}
	}
	// Y08: a latched fallback is refused by Phase 2 but still ranked by Phase 3.
	var latches map[string]bool
	var d tlpDecision
	var modes []string
	for _, v := range []float64{55, 45} {
		d, latches = decideThreshold(band, []tlpOption{up("A", ms(70))}, fb(up("F", ms(v))), "", latches)
		modes = append(modes, string(d.mode)+":"+d.choice)
	}
	if got := joined(modes); got != "degraded:F degraded:F" || !d.fallbackOf.latched || d.fallbackOf.reason != reasonRecovering {
		t.Fatalf("Y08: %s, fallback %+v", got, d.fallbackOf)
	}
	// Y09: a parent latches a child's reported result as one option.
	child := func(v float64) tlpOption { return tlpOption{tag: "C", known: true, usable: true, rtt: ms(v)} }
	latches = nil
	var parent []string
	for _, v := range []float64{55, 45} {
		d, latches = decideThreshold(band, []tlpOption{child(v)}, fb(up("F", ms(30))), "", latches)
		parent = append(parent, d.choice)
	}
	if got := joined(parent); got != "F F" {
		t.Fatalf("Y09: %s", got)
	}
	// Y10: the same outbound latched in one balancer only.
	_, strict := decideThreshold(limits(ms(40), ms(30)), series(45), &f, "", nil)
	_, loose := decideThreshold(limits(ms(100), ms(80)), series(45), &f, "", nil)
	if !strict["A"] || loose["A"] {
		t.Fatalf("Y10: latched strict %v loose %v", strict["A"], loose["A"])
	}
}

func joined(values []string) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out
}
