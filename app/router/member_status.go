package router

import (
	"context"
	"slices"

	"google.golang.org/protobuf/proto"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
)

// Core spec019 M4, M5: a member balancer is a node to the level above. Its availability and delay
// bubble up from its own level, and the level above reads them as an outbound's observation.

// memberWrap lets a balancer with members hand its strategy a wrapper for the observatory the
// strategy reads. Every strategy embeds it and applies it where it takes the observatory.
type memberWrap struct {
	wrap func(extension.Observatory) extension.Observatory
}

func (m *memberWrap) observeMembers(wrap func(extension.Observatory) extension.Observatory) {
	m.wrap = wrap
}

// observes says whether a strategy that reads the observatory only for its fallback must read it
// anyway: its level has members, whose status it reads there.
func (m *memberWrap) observes() bool {
	return m.wrap != nil
}

func (m *memberWrap) wrapped(o extension.Observatory) extension.Observatory {
	if m.wrap == nil || o == nil {
		return o
	}
	return m.wrap(o)
}

type memberObserving interface {
	observeMembers(func(extension.Observatory) extension.Observatory)
}

// memberObservatory is the observatory as one balancer's level reads it: the observation with one
// status per member balancer added under the member's tag, none for a member that is unknown.
type memberObservatory struct {
	extension.Observatory
	balancer *Balancer
}

func (o *memberObservatory) GetObservation(ctx context.Context) (proto.Message, error) {
	result, err := o.Observatory.GetObservation(ctx)
	if err != nil {
		return nil, err
	}
	base, ok := result.(*observatory.ObservationResult)
	m := o.balancer.peers.Load()
	if !ok || m == nil {
		return result, nil
	}
	reader := newStatusReader(*m, base.Status, o.balancer.ohm)
	out := &observatory.ObservationResult{Status: slices.Clone(base.Status)}
	for _, member := range o.balancer.members {
		if status := reader.balancer(member); status != nil {
			out.Status = append(out.Status, status)
		}
	}
	return out, nil
}

// statusReader computes balancer statuses over one map and one observation, each balancer once.
type statusReader struct {
	m        map[string]*Balancer
	observed map[string]*observatory.OutboundStatus
	ohm      outbound.Manager
	memo     map[string]*observatory.OutboundStatus
	done     map[string]bool
}

func newStatusReader(m map[string]*Balancer, statuses []*observatory.OutboundStatus, ohm outbound.Manager) *statusReader {
	observed := make(map[string]*observatory.OutboundStatus, len(statuses))
	for _, status := range statuses {
		observed[status.OutboundTag] = status
	}
	return &statusReader{
		m: m, observed: observed, ohm: ohm,
		memo: map[string]*observatory.OutboundStatus{}, done: map[string]bool{},
	}
}

// balancer is a balancer's status as the level above reads it (M4), nil when unknown: its override
// target's, else its best available candidate's, else unknown while a candidate has no observation,
// else its fallback's. The status keeps the measurement it bubbles up from, under the balancer's tag.
func (r *statusReader) balancer(tag string) *observatory.OutboundStatus {
	if r.done[tag] {
		return r.memo[tag]
	}
	r.done[tag] = true // a cycle, which M7 refuses, would read unknown
	b, ok := r.m[tag]
	if !ok {
		return nil
	}
	var status *observatory.OutboundStatus
	if target := b.override.Get(); target != "" {
		status = r.observed[target]
	} else {
		candidates, _ := b.SelectOutbounds()
		var best, dead *observatory.OutboundStatus
		unknown := false
		for _, candidate := range candidates {
			var s *observatory.OutboundStatus
			if slices.Contains(b.members, candidate) {
				s = r.balancer(candidate)
			} else {
				s = r.observed[candidate]
			}
			switch {
			case s == nil:
				unknown = true
			case s.Alive:
				if best == nil || s.Delay < best.Delay {
					best = s
				}
			case dead == nil:
				dead = s
			}
		}
		switch {
		case best != nil:
			status = best
		case unknown:
			status = nil
		default:
			status = r.fallback(b, dead)
		}
	}
	if status != nil {
		status = proto.Clone(status).(*observatory.OutboundStatus)
		status.OutboundTag, status.TrackedFrom = tag, ""
	}
	r.memo[tag] = status
	return status
}

// fallback is a level's status once every candidate is down: its fallback's, a blackhole's and a
// missing fallback's being down, measured as the level's first down candidate was.
func (r *statusReader) fallback(b *Balancer, dead *observatory.OutboundStatus) *observatory.OutboundStatus {
	switch fallback := b.fallbackTag; {
	case fallback == "" || r.blackhole(fallback):
	case r.m[fallback] != nil:
		return r.balancer(fallback)
	default:
		return r.observed[fallback]
	}
	if dead == nil {
		return &observatory.OutboundStatus{}
	}
	down := proto.Clone(dead).(*observatory.OutboundStatus)
	down.Alive, down.Delay = false, 0
	return down
}

func (r *statusReader) blackhole(tag string) bool {
	if r.ohm == nil {
		return false
	}
	h := r.ohm.GetHandler(tag)
	return h != nil && h.ProxySettings().GetType() == "xray.proxy.blackhole.Config"
}

// GetMembers implements routing.BalancerMembers (M10): the level's candidates in order.
func (v balancerView) GetMembers(tag string) ([]routing.BalancerMember, error) {
	b, err := v.get(tag)
	if err != nil {
		return nil, err
	}
	candidates, err := b.SelectOutbounds()
	if err != nil {
		return nil, err
	}
	members := make([]routing.BalancerMember, 0, len(candidates))
	for _, candidate := range candidates {
		members = append(members, routing.BalancerMember{Tag: candidate, Balancer: slices.Contains(b.members, candidate)})
	}
	return members, nil
}

// GetStatus implements routing.BalancerMembers (M4, M10), computed on this view's map and the current
// observation; without an observatory it is unknown.
func (v balancerView) GetStatus(tag string) (routing.BalancerStatus, error) {
	b, err := v.get(tag)
	if err != nil {
		return routing.BalancerStatus{}, err
	}
	if b.ctx == nil {
		return routing.BalancerStatus{}, nil
	}
	instance := core.FromContext(b.ctx)
	if instance == nil {
		return routing.BalancerStatus{}, nil
	}
	o, ok := instance.GetFeature(extension.ObservatoryType()).(extension.Observatory)
	if !ok || o == nil {
		return routing.BalancerStatus{}, nil
	}
	result, err := o.GetObservation(b.ctx)
	if err != nil {
		return routing.BalancerStatus{}, nil
	}
	observation, ok := result.(*observatory.ObservationResult)
	if !ok {
		return routing.BalancerStatus{}, nil
	}
	status := newStatusReader(map[string]*Balancer(v), observation.Status, b.ohm).balancer(tag)
	if status == nil {
		return routing.BalancerStatus{}, nil
	}
	return routing.BalancerStatus{Known: true, Alive: status.Alive, Delay: status.Delay}, nil
}

// GetMembers implements routing.BalancerMembers on the current map.
func (r *Router) GetMembers(tag string) ([]routing.BalancerMember, error) {
	return balancerView(*r.balancers.Load()).GetMembers(tag)
}

// GetStatus implements routing.BalancerMembers on the current map.
func (r *Router) GetStatus(tag string) (routing.BalancerStatus, error) {
	return balancerView(*r.balancers.Load()).GetStatus(tag)
}
