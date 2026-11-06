package router

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/features/routing"
)

// spec017: a fallbackTag that names a balancer of the map continues the pick there (B1), and a
// published map has no fallback cycle (L1).

// checkFallbackCycles rejects a balancer map whose fallback graph has a cycle, a self-reference
// included (L1, L4). Each balancer has at most one edge, so a walk follows it until a finished
// balancer, a tag that is no balancer, or a balancer on the current walk: a cycle.
func checkFallbackCycles(m map[string]*Balancer) error {
	const (
		unvisited = iota
		walking
		done
	)
	state := make(map[string]int, len(m))
	var cycles []string
	for _, start := range slices.Sorted(maps.Keys(m)) {
		var walk []string
		for tag := start; ; {
			b, ok := m[tag]
			if !ok || state[tag] == done {
				break
			}
			if state[tag] == walking {
				cycles = append(cycles, formatCycle(walk[slices.Index(walk, tag):]))
				break
			}
			state[tag] = walking
			walk = append(walk, tag)
			if b.fallbackTag == "" {
				break
			}
			tag = b.fallbackTag
		}
		for _, tag := range walk {
			state[tag] = done
		}
	}
	if len(cycles) == 0 {
		return nil
	}
	slices.Sort(cycles)
	return errors.New("balancer fallback cycle: ", strings.Join(cycles, "; "))
}

// formatCycle prints a cycle from its smallest tag back to it: "a -> b -> a".
func formatCycle(cycle []string) string {
	first := slices.Index(cycle, slices.Min(cycle))
	tags := append(slices.Clone(cycle[first:]), cycle[:first]...)
	return strings.Join(append(tags, tags[0]), " -> ")
}

// pickTag is a rule's outbound tag: its own, or its balancer's pick with the fallback chain resolved
// on one balancer map (B1, L6): the newest published map that contains that balancer. A rule read
// just before a replacing reload thus finishes on the map it was published with, never on a mix.
func (r *Router) pickTag(rule *Rule) (string, error) {
	if rule.Balancer == nil {
		return rule.Tag, nil
	}
	m := rule.Balancer.peers.Load()
	if m == nil { // a balancer that was never published
		m = r.balancers.Load()
	}
	return r.resolveChain(rule.Balancer, *m)
}

// resolveChain picks through first and then, while a level's result is its fallbackTag naming a
// balancer of m, through that balancer (B1-B4). Every published map passed L1, so more than len(m)+1
// levels is a Core defect: the pick is counted, logged and dropped, never sent to the default
// outbound (L6).
func (r *Router) resolveChain(first *Balancer, m map[string]*Balancer) (string, error) {
	b := first
	for range len(m) + 1 {
		tag, fallback, err := b.pick()
		if err != nil || !fallback {
			return tag, err
		}
		next, ok := m[tag]
		if !ok {
			return tag, nil
		}
		b = next
	}
	r.fallbackChainGuard.Add(1)
	errors.LogError(context.Background(), "balancer fallback chain exceeds ", len(m)+1, " levels: ",
		strings.Join(staticChain(first, m), " -> "))
	return "", routing.ErrRouteDropped
}

// staticChain is first's tag and its fallbackTags, followed through m for the guard's log.
func staticChain(first *Balancer, m map[string]*Balancer) []string {
	path := []string{first.tag}
	for b := first; len(path) <= len(m)+2; {
		path = append(path, b.fallbackTag)
		next, ok := m[b.fallbackTag]
		if !ok {
			break
		}
		b = next
	}
	return path
}

// RecordLoopbackReentry implements routing.LoopGuard (L7).
func (r *Router) RecordLoopbackReentry() {
	r.loopbackReentryGuard.Add(1)
}

// LoopGuards implements routing.LoopGuard (L8).
func (r *Router) LoopGuards() (uint64, uint64) {
	return r.fallbackChainGuard.Load(), r.loopbackReentryGuard.Load()
}

// balancerView is one published balancer map: every read through it, a whole chain included, sees
// the same balancers (L-E).
type balancerView map[string]*Balancer

// Balancers implements routing.BalancerLister.
func (r *Router) Balancers() routing.BalancerView {
	return balancerView(*r.balancers.Load())
}

func (v balancerView) Tags() []string {
	return slices.Sorted(maps.Keys(v))
}

func (v balancerView) get(tag string) (*Balancer, error) {
	if b, ok := v[tag]; ok {
		return b, nil
	}
	return nil, errors.New("cannot find tag")
}

func (v balancerView) GetOverrideTarget(tag string) (string, error) {
	b, err := v.get(tag)
	if err != nil {
		return "", err
	}
	return b.override.Get(), nil
}

func (v balancerView) GetPrincipleTarget(tag string) ([]string, error) {
	b, err := v.get(tag)
	if err != nil {
		return nil, err
	}
	s, ok := b.strategy.(BalancingPrincipleTarget)
	if !ok {
		return nil, errors.New("unsupported GetPrincipleTarget")
	}
	candidates, err := b.SelectOutbounds()
	if err != nil {
		return nil, errors.New("unable to select outbounds").Base(err)
	}
	return s.GetPrincipleTarget(candidates), nil
}

func (v balancerView) GetFallback(tag string) (string, bool, bool, error) {
	b, err := v.get(tag)
	if err != nil {
		return "", false, false, err
	}
	if _, err := b.SelectOutbounds(); err != nil && b.fallbackTag == "" {
		return "", false, false, errors.New("unable to select outbounds").Base(err)
	}
	active, reported := b.fallbackState()
	return b.fallbackTag, active, reported, nil
}

// GetFallbackChain resolves the chain after tag on this map. L1 keeps it acyclic; the bound only
// keeps a defect from looping here.
func (v balancerView) GetFallbackChain(tag string) ([]routing.FallbackHop, error) {
	b, err := v.get(tag)
	if err != nil {
		return nil, err
	}
	var chain []routing.FallbackHop
	for next := b.fallbackTag; next != "" && len(chain) <= len(v); {
		hop, ok := v[next]
		if !ok {
			chain = append(chain, routing.FallbackHop{Tag: next})
			break
		}
		active, reported := hop.fallbackState()
		chain = append(chain, routing.FallbackHop{Tag: next, Balancer: true, Active: active, Reported: reported})
		next = hop.fallbackTag
	}
	return chain, nil
}
