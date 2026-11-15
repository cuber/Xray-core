package router

import (
	"maps"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/routing"
)

// Core spec027 TL-5: thresholdLeastPing decisions are computed when something they depend on changes
// and published as one immutable pass; a pick and a control-plane read only read it.

// tlpPass is one computation over one balancer map and one observation.
type tlpPass struct {
	gen        uint64
	observedAt time.Time
	// obsReason is set when the observatory gave no usable observation.
	obsReason string
	states    map[string]*tlpState
}

// tlpState is one thresholdLeastPing balancer in a pass.
type tlpState struct {
	decision tlpDecision // automatic; an override is reported beside it
	override string      // the override target when the pass was computed
	latches  map[string]bool
	// bubbled is the status a legacy level above reads (TL-3 table); nil is unknown.
	bubbled *observatory.OutboundStatus
	// result is the same status as a thresholdLeastPing level above reads it.
	result tlpOption
	// candidateStatus and fallbackStatus are the raw statuses the decision read, for the report.
	candidateStatus []*observatory.OutboundStatus
	fallbackStatus  *observatory.OutboundStatus
}

func (p *tlpPass) state(tag string) *tlpState {
	if p == nil {
		return nil
	}
	return p.states[tag]
}

func cloneStatus(s *observatory.OutboundStatus) *observatory.OutboundStatus {
	if s == nil {
		return nil
	}
	return proto.Clone(s).(*observatory.OutboundStatus)
}

func isThreshold(b *Balancer) bool {
	_, ok := b.strategy.(*ThresholdLeastPingStrategy)
	return ok
}

func hasThreshold(m map[string]*Balancer) bool {
	for _, b := range m {
		if isThreshold(b) {
			return true
		}
	}
	return false
}

// passComputer evaluates every thresholdLeastPing balancer of m once, children first through
// recursion over the acyclic member/fallback graph (spec017-019).
type passComputer struct {
	m         map[string]*Balancer
	reader    *statusReader
	obsReason string
	exists    func(tag string) bool
	states    map[string]*tlpState
	visiting  map[string]bool
}

// observation reads the instance observatory once for a pass.
func (r *Router) observation() ([]*observatory.OutboundStatus, string) {
	if r.ctx == nil {
		return nil, "no observatory"
	}
	instance := core.FromContext(r.ctx)
	if instance == nil {
		return nil, "no observatory"
	}
	o, ok := instance.GetFeature(extension.ObservatoryType()).(extension.Observatory)
	if !ok || o == nil {
		return nil, "no observatory"
	}
	result, err := o.GetObservation(r.ctx)
	if err != nil {
		return nil, "observation failed: " + err.Error()
	}
	observation, ok := result.(*observatory.ObservationResult)
	if !ok {
		return nil, "unexpected observation type"
	}
	return observation.Status, ""
}

// computePassLocked computes and publishes a pass for m; r.tlpMu is held.
func (r *Router) computePassLocked(m map[string]*Balancer) *tlpPass {
	statuses, obsReason := r.observation()
	return r.computePass(m, statuses, obsReason)
}

// computePass computes and publishes a pass for m from one observation.
func (r *Router) computePass(m map[string]*Balancer, statuses []*observatory.OutboundStatus, obsReason string) *tlpPass {
	c := &passComputer{
		m: m, reader: newStatusReader(m, statuses, r.ohm), obsReason: obsReason,
		exists:   func(tag string) bool { return r.ohm != nil && r.ohm.GetHandler(tag) != nil },
		states:   map[string]*tlpState{},
		visiting: map[string]bool{},
	}
	c.reader.threshold = func(tag string) *observatory.OutboundStatus {
		if st := c.state(tag); st != nil {
			return st.bubbled
		}
		return nil
	}
	for _, tag := range slices.Sorted(maps.Keys(m)) {
		if isThreshold(m[tag]) {
			c.state(tag)
		}
	}
	pass := &tlpPass{gen: r.tlpGen.Add(1), observedAt: time.Now(), obsReason: obsReason, states: c.states}
	for _, b := range m {
		b.pass.Store(pass)
	}
	return pass
}

// outboundOption is a candidate or fallback outbound's option.
func (c *passComputer) outboundOption(tag string) tlpOption {
	if !c.exists(tag) { // removed at run time: a lingering observation cannot make it usable (D15)
		return tlpOption{tag: tag, reason: reasonAbsent}
	}
	if c.obsReason != "" {
		return tlpOption{tag: tag, reason: reasonObservationUnavailable}
	}
	s := c.reader.observed[tag]
	if s == nil {
		return tlpOption{tag: tag, reason: reasonUnobserved, pending: true}
	}
	return optionFromStatus(tag, s)
}

// childOption is a member or fallback balancer's result as a thresholdLeastPing level reads it (TL-6).
func (c *passComputer) childOption(tag string) tlpOption {
	b := c.m[tag]
	if isThreshold(b) {
		if st := c.state(tag); st != nil {
			return st.result
		}
		return tlpOption{tag: tag, reason: reasonUnobserved}
	}
	if b.override.Get() == "" {
		switch b.strategy.(type) {
		case *RoundRobinStrategy, *RandomStrategy:
			if o, ok := c.memberMean(b); ok {
				return o
			}
		}
	}
	s := c.reader.balancer(tag)
	o := optionFromStatus(tag, s)
	o.pending = s == nil
	return o
}

// memberMean is a roundRobin/random child's representative RTT for a thresholdLeastPing parent: the
// equal mean of its usable measured candidates' RTTs (TL-6, D13 exact durations).
func (c *passComputer) memberMean(b *Balancer) (tlpOption, bool) {
	candidates, err := b.SelectOutbounds()
	if err != nil || len(candidates) == 0 {
		return tlpOption{}, false
	}
	var sum time.Duration
	measured := 0
	for _, candidate := range candidates {
		var o tlpOption
		if slices.Contains(b.members, candidate) {
			o = c.childOption(candidate)
		} else {
			o = c.outboundOption(candidate)
		}
		if o.usable {
			sum += o.rtt
			measured++
		}
	}
	if measured == 0 {
		return tlpOption{}, false
	}
	return tlpOption{
		tag: b.tag, known: true, usable: true, rtt: sum / time.Duration(measured),
		mean: &memberMean{measured: measured, dispatchable: len(candidates)},
	}, true
}

func (c *passComputer) isBlackhole(tag string) bool {
	return c.reader.blackhole(tag)
}

// state computes one thresholdLeastPing balancer, memoised.
func (c *passComputer) state(tag string) *tlpState {
	if st, ok := c.states[tag]; ok {
		return st
	}
	b, ok := c.m[tag]
	if !ok || !isThreshold(b) || c.visiting[tag] {
		return nil // a cycle, which the graph checks refuse, reads unknown
	}
	c.visiting[tag] = true
	defer delete(c.visiting, tag)
	policy := b.strategy.(*ThresholdLeastPingStrategy).policy

	candidateTags, _ := b.SelectOutbounds()
	candidates := make([]tlpOption, 0, len(candidateTags))
	raws := make([]*observatory.OutboundStatus, 0, len(candidateTags))
	for _, candidate := range candidateTags {
		if _, isBalancer := c.m[candidate]; isBalancer && slices.Contains(b.members, candidate) {
			candidates = append(candidates, c.childOption(candidate))
			raws = append(raws, c.reader.balancer(candidate))
		} else {
			candidates = append(candidates, c.outboundOption(candidate))
			raws = append(raws, cloneStatus(c.reader.observed[candidate]))
		}
	}
	var fallback *tlpOption
	var fallbackRaw *observatory.OutboundStatus
	blackhole := ""
	switch ft := b.fallbackTag; {
	case ft == "":
	case c.m[ft] != nil:
		o := c.childOption(ft)
		fallback, fallbackRaw = &o, c.reader.balancer(ft)
	case c.isBlackhole(ft):
		blackhole = ft
	default:
		o := c.outboundOption(ft)
		fallback, fallbackRaw = &o, cloneStatus(c.reader.observed[ft])
	}
	var previous map[string]bool
	if ps := b.pass.Load().state(tag); ps != nil {
		previous = ps.latches
	}
	decision, latches := decideThreshold(policy, candidates, fallback, blackhole, previous)
	if c.obsReason != "" && decision.reason != "" {
		decision.reason = c.obsReason
	}
	st := &tlpState{
		decision: decision, latches: latches, override: b.override.Get(),
		candidateStatus: raws, fallbackStatus: fallbackRaw,
	}
	st.bubbled, st.result = c.reported(tag, st)
	c.states[tag] = st
	return st
}

// reported is what the level above reads of a thresholdLeastPing balancer (TL-3 table): the chosen
// option's status, the override target's observation, unknown while pending, or dead.
func (c *passComputer) reported(tag string, st *tlpState) (*observatory.OutboundStatus, tlpOption) {
	own := func(s *observatory.OutboundStatus) *observatory.OutboundStatus {
		if s == nil {
			return nil
		}
		s = proto.Clone(s).(*observatory.OutboundStatus)
		s.OutboundTag, s.TrackedFrom = tag, ""
		return s
	}
	asResult := func(o tlpOption) tlpOption {
		o.tag, o.pending = tag, false
		return o
	}
	if st.override != "" {
		s := own(c.reader.observed[st.override])
		o := optionFromStatus(tag, s)
		o.pending = s == nil
		return s, o
	}
	d := st.decision
	switch {
	case d.mode == modePending:
		return nil, tlpOption{tag: tag, reason: reasonUnobserved, pending: true}
	case d.chosen == nil:
		dead := &observatory.OutboundStatus{OutboundTag: tag, LastErrorReason: "thresholdLeastPing: " + d.reason}
		return dead, tlpOption{tag: tag, known: true, reason: reasonBalancerUnavailable}
	}
	chosen := *d.chosen
	var raw *observatory.OutboundStatus
	if b, isBalancer := c.m[chosen.tag]; isBalancer {
		if chosen.mean == nil {
			raw = c.reader.balancer(b.tag)
		}
	} else {
		raw = c.reader.observed[chosen.tag]
	}
	if raw == nil { // a member mean: a status carrying the representative RTT
		raw = &observatory.OutboundStatus{
			Alive: true, Delay: chosen.rtt.Milliseconds(),
			HealthPing: &observatory.HealthPingMeasurementResult{
				All: int64(chosen.mean.measured), Average: int64(chosen.rtt),
				Min: int64(chosen.rtt), Max: int64(chosen.rtt),
			},
		}
	}
	return own(raw), asResult(chosen)
}

// pickThreshold is one level of a pick for a thresholdLeastPing balancer (TL-3, TL-5, TL-8): a
// manual override first, then the decision in pass (the one the chain resolution pinned, else the
// balancer's current one). It reads no observation and computes nothing.
func (b *Balancer) pickThreshold(pass *tlpPass) (string, bool, error) {
	if o := b.override.Get(); o != "" {
		return o, false, nil
	}
	if pass == nil {
		pass = b.pass.Load()
	}
	st := pass.state(b.tag)
	switch {
	case st == nil:
		return "", false, b.drop("no decision yet")
	case st.decision.terminal == terminalDrop:
		return "", false, b.drop(st.decision.reason)
	case st.decision.fallback:
		return b.fallbackTag, true, nil
	}
	return st.decision.choice, false, nil
}

// drop counts a dropped connection (D14) and returns the error the dispatcher closes it with.
func (b *Balancer) drop(reason string) error {
	b.drops.Add(1)
	b.lastDrop.Store(&reason)
	return routing.ErrBalancerUnavailable
}

// thresholdChanged recomputes after a change (TL-5): a trigger during a computation marks it dirty
// and the computation repeats once more; no goroutine, timer or queue.
func (r *Router) thresholdChanged() {
	r.tlpDirty.Store(true)
	for {
		if !r.tlpMu.TryLock() {
			return // the running computation sees dirty
		}
		for r.tlpDirty.Swap(false) {
			if m := *r.balancers.Load(); hasThreshold(m) {
				r.computePassLocked(m)
			}
		}
		r.tlpMu.Unlock()
		if !r.tlpDirty.Load() {
			return
		}
	}
}

// subscribeThreshold subscribes once to the observatory and the outbound manager (TL-5), the first
// time a map with a thresholdLeastPing balancer is published after start.
func (r *Router) subscribeThreshold() {
	r.tlpSubMu.Lock()
	defer r.tlpSubMu.Unlock()
	if r.tlpSubscribed || r.tlpClosed || r.ctx == nil {
		return
	}
	instance := core.FromContext(r.ctx)
	if instance == nil {
		return
	}
	r.tlpSubscribed = true
	if n, ok := instance.GetFeature(extension.ObservatoryType()).(extension.ChangeNotifier); ok {
		r.tlpCancels = append(r.tlpCancels, n.OnChange(r.thresholdChanged))
	}
	if n, ok := r.ohm.(extension.ChangeNotifier); ok {
		r.tlpCancels = append(r.tlpCancels, n.OnChange(r.thresholdChanged))
	}
}

// startThreshold is Router.Start's part: the first pass with every handler in place, and the
// subscriptions.
func (r *Router) startThreshold() {
	r.tlpStarted.Store(true)
	if hasThreshold(*r.balancers.Load()) {
		r.subscribeThreshold()
		r.thresholdChanged()
	}
}

// closeThreshold cancels the subscriptions; no callback runs after it returns.
func (r *Router) closeThreshold() {
	r.tlpSubMu.Lock()
	r.tlpClosed = true
	cancels := r.tlpCancels
	r.tlpCancels = nil
	r.tlpSubMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// publishThreshold publishes a new map with its first pass already computed (TL-5): no pick sees the
// map without a decision. publish stores the map; it runs under r.tlpMu.
func (r *Router) publishThreshold(m map[string]*Balancer, publish func()) {
	if !hasThreshold(m) {
		publish()
		return
	}
	r.tlpMu.Lock()
	r.tlpDirty.Store(false)
	r.computePassLocked(m)
	publish()
	r.tlpMu.Unlock()
	if r.tlpStarted.Load() {
		r.subscribeThreshold()
	}
	if r.tlpDirty.Load() {
		r.thresholdChanged()
	}
}
