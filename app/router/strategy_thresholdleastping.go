package router

import (
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common/errors"
)

// Core spec027: thresholdLeastPing prefers candidates under maxRTT, then a fallback under it, then
// the fastest usable option, and otherwise ends on an explicit blackhole or drops the connection.
// Its decisions are computed when observations change (TL-5, threshold_pass.go) and picks only read
// them. This file holds the policy and the pure decision (TL-2 to TL-4).

// thresholdPolicy is a thresholdLeastPing balancer's loaded limits (TL-1). recoverRTT equals maxRTT
// when no band is configured.
type thresholdPolicy struct {
	maxRTT     time.Duration
	recoverRTT time.Duration
}

// newThresholdPolicy validates the typed settings of a thresholdLeastPing balancer, as they come
// from JSON or from a direct protobuf rule (TL-1): maxRTT is required and positive; recoverRTT is
// zero (omitted: no band) or within (0, maxRTT].
func newThresholdPolicy(c *StrategyThresholdLeastPingConfig) (thresholdPolicy, error) {
	if c == nil {
		return thresholdPolicy{}, errors.New("thresholdLeastPing needs settings with maxRTT")
	}
	if c.MaxRTT <= 0 {
		return thresholdPolicy{}, errors.New("thresholdLeastPing maxRTT must be positive, got ", time.Duration(c.MaxRTT).String())
	}
	policy := thresholdPolicy{maxRTT: time.Duration(c.MaxRTT), recoverRTT: time.Duration(c.RecoverRTT)}
	switch {
	case c.RecoverRTT == 0:
		policy.recoverRTT = policy.maxRTT
	case c.RecoverRTT < 0:
		return thresholdPolicy{}, errors.New("thresholdLeastPing recoverRTT must be positive, got ", policy.recoverRTT.String())
	case c.RecoverRTT > c.MaxRTT:
		return thresholdPolicy{}, errors.New("thresholdLeastPing recoverRTT ", policy.recoverRTT.String(),
			" is above maxRTT ", policy.maxRTT.String())
	}
	return policy, nil
}

// ThresholdLeastPingStrategy holds a thresholdLeastPing balancer's policy. It is not a picking
// strategy: Balancer.pick reads the published decision instead of calling PickOutbound.
type ThresholdLeastPingStrategy struct {
	policy thresholdPolicy
}

// PickOutbound satisfies BalancingStrategy; Balancer.pick never calls it for this strategy.
func (s *ThresholdLeastPingStrategy) PickOutbound([]string) string {
	return ""
}

// Selection reasons (spec027 Control Plane); a per-level reason, not an observatory error string.
const (
	reasonEligible               = "eligible"
	reasonUnobserved             = "unobserved"              // observed tag, no result yet
	reasonAbsent                 = "absent"                  // the outbound no longer exists
	reasonProbeDown              = "probe_down"              // dead as the observation reports it
	reasonRTTUnavailable         = "rtt_unavailable"         // alive, no successful sample
	reasonInvalidMeasurement     = "invalid_measurement"     // negative or inconsistent values
	reasonMaxRTTExceeded         = "max_rtt_exceeded"        // usable, latched, at or over maxRTT
	reasonRecovering             = "recovering"              // usable, latched, under maxRTT, not yet under recoverRTT
	reasonBalancerUnavailable    = "balancer_unavailable"    // a child balancer with no usable option
	reasonObservationUnavailable = "observation_unavailable" // the observatory gave no usable observation
)

// memberMean is how a roundRobin/random child's representative RTT was formed (TL-6).
type memberMean struct {
	measured, dispatchable int
}

// tlpOption is one option of a level as the decision sees it (TL-2): a candidate outbound, a member
// balancer, or the fallback result.
type tlpOption struct {
	tag     string
	known   bool // an observation or a child result exists
	usable  bool // known, alive and validly measured
	pending bool // exists but has had no first result since start, publication or its addition
	rtt     time.Duration
	reason  string // why it is not usable
	mean    *memberMean
}

// optionFromStatus reads one observation (TL-2): a burst window average in nanoseconds, a classic
// Delay in milliseconds; never the latest, minimum or maximum sample, never a fabricated zero.
func optionFromStatus(tag string, s *observatory.OutboundStatus) tlpOption {
	o := tlpOption{tag: tag}
	if s == nil {
		o.reason = reasonUnobserved
		return o
	}
	o.known = true
	switch hp := s.GetHealthPing(); {
	case !s.Alive:
		o.reason = reasonProbeDown
	case hp != nil:
		switch {
		case hp.All < 0 || hp.Fail < 0 || hp.Fail > hp.All || hp.Average < 0:
			o.reason = reasonInvalidMeasurement
		case hp.All-hp.Fail == 0:
			o.reason = reasonRTTUnavailable
		default:
			o.usable, o.rtt = true, time.Duration(hp.Average)
		}
	case s.Delay < 0:
		o.reason = reasonInvalidMeasurement
	default:
		o.usable, o.rtt = true, time.Duration(s.Delay)*time.Millisecond
	}
	return o
}

type tlpMode string

const (
	modePrimary     tlpMode = "primary"
	modeFallback    tlpMode = "fallback"
	modeDegraded    tlpMode = "degraded"
	modePending     tlpMode = "pending"
	modeUnavailable tlpMode = "unavailable"
	modeOverride    tlpMode = "override"
)

type tlpTerminal string

const (
	terminalNone      tlpTerminal = ""
	terminalBlackhole tlpTerminal = "blackhole"
	terminalDrop      tlpTerminal = "drop"
)

// tlpSelection is one option's eligibility at this level (spec027 Control Plane).
type tlpSelection struct {
	tag         string
	usable      bool
	withinLimit bool
	latched     bool
	rttKnown    bool
	rtt         time.Duration
	reason      string
	mean        *memberMean
}

// tlpDecision is one level's automatic decision (TL-3); an override is applied by the caller.
type tlpDecision struct {
	mode tlpMode
	// choice is the chosen candidate, the fallback tag, or the blackhole tag; empty on a drop.
	choice string
	// fallback: the choice is this level's fallback (Phase 2, a degraded fallback or the blackhole).
	fallback bool
	terminal tlpTerminal
	// reason says why nothing was usable (unavailable and pending only).
	reason     string
	candidates []tlpSelection
	fallbackOf *tlpSelection
	// chosen is the option the choice came from; nil on a terminal outcome.
	chosen *tlpOption
}

// rankMS is an option's ranking key (D13): whole milliseconds, truncated like Delay.
func rankMS(o tlpOption) int64 {
	return o.rtt.Milliseconds()
}

// decideThreshold is the pure decision of one level (TL-3, TL-4). latches are the level's options
// latched over the limit before this computation; the returned set holds them after it. blackhole is
// the fallbackTag when it names an actual blackhole outbound, which is a terminal action rather than
// an option.
func decideThreshold(policy thresholdPolicy, candidates []tlpOption, fallback *tlpOption, blackhole string,
	latches map[string]bool,
) (tlpDecision, map[string]bool) {
	next := map[string]bool{}
	transition := func(o tlpOption) {
		latched := latches[o.tag]
		if o.usable {
			switch {
			case !latched && o.rtt >= policy.maxRTT:
				latched = true
			case latched && o.rtt < policy.recoverRTT:
				latched = false
			}
		}
		if latched {
			next[o.tag] = true
		}
	}
	for _, o := range candidates {
		transition(o)
	}
	if fallback != nil {
		transition(*fallback)
	}
	within := func(o tlpOption) bool { return o.usable && !next[o.tag] }
	selection := func(o tlpOption) tlpSelection {
		s := tlpSelection{
			tag: o.tag, usable: o.usable, withinLimit: within(o), latched: next[o.tag],
			rttKnown: o.usable, rtt: o.rtt, mean: o.mean,
		}
		switch {
		case !o.usable:
			s.reason, s.rtt = o.reason, 0
		case s.withinLimit:
			s.reason = reasonEligible
		case o.rtt < policy.maxRTT:
			s.reason = reasonRecovering
		default:
			s.reason = reasonMaxRTTExceeded
		}
		return s
	}
	d := tlpDecision{}
	for _, o := range candidates {
		d.candidates = append(d.candidates, selection(o))
	}
	if fallback != nil {
		s := selection(*fallback)
		d.fallbackOf = &s
	}
	choose := func(mode tlpMode, o tlpOption, isFallback bool) {
		chosen := o
		d.mode, d.choice, d.fallback, d.chosen = mode, o.tag, isFallback, &chosen
	}
	best := func(eligible func(tlpOption) bool) int {
		at := -1
		for i, o := range candidates {
			if eligible(o) && (at < 0 || rankMS(o) < rankMS(candidates[at])) {
				at = i
			}
		}
		return at
	}

	// Phase 1: the fastest candidate under the limit, even when the fallback is faster.
	if i := best(within); i >= 0 {
		choose(modePrimary, candidates[i], false)
		return d, next
	}
	// Phase 2: the fallback under this level's limit.
	if fallback != nil && within(*fallback) {
		choose(modeFallback, *fallback, true)
		return d, next
	}
	// Phase 3: the fastest usable option; a candidate wins a tie with the fallback.
	i := best(func(o tlpOption) bool { return o.usable })
	switch {
	case fallback != nil && fallback.usable && (i < 0 || rankMS(*fallback) < rankMS(candidates[i])):
		choose(modeDegraded, *fallback, true)
		return d, next
	case i >= 0:
		choose(modeDegraded, candidates[i], false)
		return d, next
	}
	// Terminal: nothing usable.
	d.mode, d.reason = modeUnavailable, "no usable option"
	for _, o := range candidates {
		if o.pending {
			d.mode, d.reason = modePending, "waiting for first results"
		}
	}
	if fallback != nil && fallback.pending {
		d.mode, d.reason = modePending, "waiting for first results"
	}
	if blackhole != "" {
		d.choice, d.fallback, d.terminal = blackhole, true, terminalBlackhole
	} else {
		d.terminal = terminalDrop
	}
	return d, next
}
