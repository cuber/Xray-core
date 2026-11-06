package router

import (
	"context"
	"slices"
	sync "sync"
	"sync/atomic"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
)

type BalancingStrategy interface {
	PickOutbound([]string) string
}

type BalancingPrincipleTarget interface {
	GetPrincipleTarget([]string) []string
}

// BalancingFallbackState is a strategy that knows when it yields no candidate,
// so that the balancer falls back (spec016).
type BalancingFallbackState interface {
	FallbackActive([]string) bool
}

type RoundRobinStrategy struct {
	FallbackTag string
	memberWrap

	ctx         context.Context
	observatory extension.Observatory
	mu          sync.Mutex
	index       int
}

func (s *RoundRobinStrategy) InjectContext(ctx context.Context) {
	s.ctx = ctx
	if len(s.FallbackTag) > 0 || s.observes() {
		common.Must(core.RequireFeatures(s.ctx, func(observatory extension.Observatory) error {
			s.observatory = s.wrapped(observatory)
			return nil
		}))
	}
}

func (s *RoundRobinStrategy) GetPrincipleTarget(strings []string) []string {
	return strings
}

// aliveCandidates keeps the candidates the observatory does not report down; without an observatory
// or an observation it keeps them all. roundrobin and random pick from it and fall back when it is
// empty (spec017 B2).
func aliveCandidates(ctx context.Context, o extension.Observatory, tags []string) []string {
	if o == nil {
		return tags
	}
	observeReport, err := o.GetObservation(ctx)
	if err != nil {
		return tags
	}
	result, ok := observeReport.(*observatory.ObservationResult)
	if !ok {
		return tags
	}
	statusMap := make(map[string]*observatory.OutboundStatus)
	for _, outboundStatus := range result.Status {
		statusMap[outboundStatus.OutboundTag] = outboundStatus
	}
	aliveTags := make([]string, 0)
	for _, candidate := range tags {
		if outboundStatus, found := statusMap[candidate]; found {
			if outboundStatus.Alive {
				aliveTags = append(aliveTags, candidate)
			}
		} else {
			// unfound candidate is considered alive
			aliveTags = append(aliveTags, candidate)
		}
	}
	return aliveTags
}

// FallbackActive implements BalancingFallbackState (spec017): no candidate is alive. It never
// advances the index.
func (s *RoundRobinStrategy) FallbackActive(tags []string) bool {
	return len(aliveCandidates(s.ctx, s.observatory, tags)) == 0
}

func (s *RoundRobinStrategy) PickOutbound(tags []string) string {
	tags = aliveCandidates(s.ctx, s.observatory, tags)

	n := len(tags)
	if n == 0 {
		// goes to fallbackTag
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tag := tags[s.index%n]
	s.index = (s.index + 1) % n
	return tag
}

type Balancer struct {
	tag         string
	selectors   []string
	strategy    BalancingStrategy
	ohm         outbound.Manager
	fallbackTag string
	// members are member balancers by tag, candidates after the selector's outbounds (Core spec019 M1, M2).
	members []string
	// ctx is the instance context InjectContext received; the control plane reads the observatory through it.
	ctx context.Context

	override override
	// peers is the newest published balancer map that contains this balancer (spec017 L6).
	peers atomic.Pointer[map[string]*Balancer]
}

// PickOutbound picks the tag of a outbound at this level only; a fallback that names a balancer is
// returned as it is. Routing resolves the whole chain (spec017, resolveChain).
func (b *Balancer) PickOutbound() (string, error) {
	tag, _, err := b.pick()
	return tag, err
}

// pick is one level of a pick: the tag, and whether it is the fallbackTag (spec017 B3: only a
// fallback result goes on to another balancer).
func (b *Balancer) pick() (string, bool, error) {
	candidates, err := b.SelectOutbounds()
	if err != nil {
		if b.fallbackTag != "" {
			errors.LogInfo(context.Background(), "fallback to [", b.fallbackTag, "], due to error: ", err)
			return b.fallbackTag, true, nil
		}
		return "", false, err
	}
	var tag string
	if o := b.override.Get(); o != "" {
		tag = o
	} else {
		tag = b.strategy.PickOutbound(candidates)
	}
	if tag == "" {
		if b.fallbackTag != "" {
			errors.LogInfo(context.Background(), "fallback to [", b.fallbackTag, "], due to empty tag returned")
			return b.fallbackTag, true, nil
		}
		// will use default handler
		return "", false, errors.New("balancing strategy returns empty tag")
	}
	return tag, false, nil
}

// fallbackState says whether this level's strategy yields no candidate now, so that its picks use
// its fallback, and whether the strategy can tell (spec016, spec017). It ignores an override, which
// still wins in pick and is reported on its own.
func (b *Balancer) fallbackState() (active bool, reported bool) {
	s, ok := b.strategy.(BalancingFallbackState)
	if !ok {
		return false, false
	}
	candidates, err := b.SelectOutbounds()
	if err != nil {
		return b.fallbackTag != "", true // as pick
	}
	return b.fallbackTag != "" && s.FallbackActive(candidates), true
}

func (b *Balancer) InjectContext(ctx context.Context) {
	b.ctx = ctx
	// Core spec019 M5: a level with members reads their status in its observation.
	if receiver, ok := b.strategy.(memberObserving); ok && len(b.members) > 0 {
		receiver.observeMembers(func(o extension.Observatory) extension.Observatory {
			return &memberObservatory{Observatory: o, balancer: b}
		})
	}
	if contextReceiver, ok := b.strategy.(extension.ContextReceiver); ok {
		contextReceiver.InjectContext(ctx)
	}
}

// SelectOutbounds is the level's candidates: the outbounds its selectors match, then its member
// balancers (Core spec019 M2).
func (b *Balancer) SelectOutbounds() ([]string, error) {
	var tags []string
	if len(b.selectors) > 0 {
		hs, ok := b.ohm.(outbound.HandlerSelector)
		if !ok {
			return nil, errors.New("outbound.Manager is not a HandlerSelector")
		}
		tags = hs.Select(b.selectors)
	}
	return append(slices.Clone(tags), b.members...), nil
}

// GetPrincipleTarget implements routing.BalancerPrincipleTarget
func (r *Router) GetPrincipleTarget(tag string) ([]string, error) {
	return balancerView(*r.balancers.Load()).GetPrincipleTarget(tag)
}

// GetFallback implements routing.BalancerFallback
func (r *Router) GetFallback(tag string) (string, bool, bool, error) {
	return balancerView(*r.balancers.Load()).GetFallback(tag)
}

// GetFallbackChain implements routing.BalancerFallbackChain (spec017).
func (r *Router) GetFallbackChain(tag string) ([]routing.FallbackHop, error) {
	return balancerView(*r.balancers.Load()).GetFallbackChain(tag)
}

// SetOverrideTarget implements routing.BalancerOverrider
func (r *Router) SetOverrideTarget(tag, target string) error {
	defer r.lockGraph()()
	if b, ok := (*r.balancers.Load())[tag]; ok {
		// spec017 B5: a target is an outbound; a balancer's tag would be read as a member (Core spec019).
		if _, isBalancer := (*r.balancers.Load())[target]; isBalancer {
			return errors.New("override target ", target, " is a balancer")
		}
		// Core spec018 S3.2: a target may name any outbound, a loopback included; clearing one
		// only removes an edge.
		if target != "" {
			if err := r.guardRouting(*r.rules.Load(), *r.balancers.Load(), map[string]string{tag: target}); err != nil {
				return err
			}
		}
		b.override.Put(target)
		return nil
	}
	return errors.New("cannot find tag")
}

// GetOverrideTarget implements routing.BalancerOverrider
func (r *Router) GetOverrideTarget(tag string) (string, error) {
	return balancerView(*r.balancers.Load()).GetOverrideTarget(tag)
}
