package router

import (
	"slices"

	"github.com/xtls/xray-core/features/routing"
)

// Core spec027 Control Plane: a thresholdLeastPing level reported from one pinned pass. A read never
// computes, picks, advances a strategy or reads the observatory.

// thresholdReader pins the newest pass among a view's thresholdLeastPing balancers.
type thresholdReader struct {
	view balancerView
	pass *tlpPass
}

// ThresholdReader implements routing.BalancerThreshold.
func (v balancerView) ThresholdReader() routing.ThresholdReader {
	r := &thresholdReader{view: v}
	for _, b := range v {
		if p := b.pass.Load(); isThreshold(b) && p != nil && (r.pass == nil || p.gen > r.pass.gen) {
			r.pass = p
		}
	}
	return r
}

func thresholdSelection(s tlpSelection) *routing.ThresholdSelection {
	out := &routing.ThresholdSelection{
		Usable: s.usable, WithinLimit: s.withinLimit, Latched: s.latched, RTTKnown: s.rttKnown,
		RTT: s.rtt, Reason: s.reason,
	}
	if s.mean != nil {
		out.Aggregation, out.Measured, out.Dispatchable = "mean", s.mean.measured, s.mean.dispatchable
	}
	return out
}

// GetThreshold implements routing.ThresholdReader.
func (r *thresholdReader) GetThreshold(tag string) (routing.ThresholdReport, bool, error) {
	b, err := r.view.get(tag)
	if err != nil {
		return routing.ThresholdReport{}, false, err
	}
	s, ok := b.strategy.(*ThresholdLeastPingStrategy)
	if !ok {
		return routing.ThresholdReport{}, false, nil
	}
	report := routing.ThresholdReport{
		MaxRTT: s.policy.maxRTT, RecoverRTT: s.policy.recoverRTT,
		Mode: string(modePending), Reason: "no decision yet", Status: routing.BalancerStatus{Provenance: true},
		Dropped: b.drops.Load(),
	}
	if last := b.lastDrop.Load(); last != nil {
		report.LastDropReason = *last
	}
	st := r.pass.state(tag)
	if st == nil {
		return report, true, nil
	}
	d := st.decision
	report.Generation, report.ObservedAt = r.pass.gen, r.pass.observedAt
	report.Mode, report.Choice, report.Terminal, report.Reason = string(d.mode), d.choice, string(d.terminal), d.reason
	if st.override != "" {
		report.Mode, report.Choice = string(modeOverride), st.override
	}
	report.Status = balancerStatus(st.bubbled)
	report.FallbackActive = d.fallback
	if d.chosen != nil && !d.fallback {
		report.PrincipleTarget = []string{d.choice}
	}
	for i, sel := range d.candidates {
		member := routing.BalancerMember{
			Tag: sel.tag, Balancer: slices.Contains(b.members, sel.tag),
			Status: balancerStatus(st.candidateStatus[i]), Selection: thresholdSelection(sel),
		}
		switch {
		case st.override != "":
			if sel.tag == st.override {
				member.Share = 1 // an override takes every pick (Balancer.pick)
			}
		case d.chosen != nil && !d.fallback && sel.tag == d.choice:
			member.Share = 1
		}
		report.Members = append(report.Members, member)
	}
	if d.fallbackOf != nil {
		status := balancerStatus(st.fallbackStatus)
		report.FallbackStatus, report.FallbackSelection = &status, thresholdSelection(*d.fallbackOf)
	}
	return report, true, nil
}
