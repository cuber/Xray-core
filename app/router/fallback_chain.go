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

// checkBalancerGraph rejects a balancer map whose graph has a cycle (L1, Core spec019 M7): an edge
// leads from a balancer to each member and to its fallbackTag when that names a balancer, a
// self-reference included. It also rejects a member that names no balancer or is listed twice, and a
// member without a fallbackTag (spec019 M2). A cycle of fallback edges alone keeps L4's text; one
// with a member edge names each edge. One cycle is printed per strongly connected set of balancers,
// the shortest through its smallest tag, the sets in tag order.
func checkBalancerGraph(m map[string]*Balancer) error {
	tags := slices.Sorted(maps.Keys(m))
	isMember := map[string]bool{}
	edges := make(map[string][]graphEdge, len(m))
	for _, tag := range tags {
		b := m[tag]
		listed := map[string]bool{}
		for _, member := range b.members {
			if _, ok := m[member]; !ok {
				return errors.New("balancer ", tag, ": member ", member, " is no balancer")
			}
			if listed[member] {
				return errors.New("balancer ", tag, ": member ", member, " is listed twice")
			}
			listed[member], isMember[member] = true, true
			edges[tag] = append(edges[tag], graphEdge{to: member, member: true})
		}
		if _, ok := m[b.fallbackTag]; ok {
			edges[tag] = append(edges[tag], graphEdge{to: b.fallbackTag})
		}
	}
	for _, tag := range slices.Sorted(maps.Keys(isMember)) {
		if m[tag].fallbackTag == "" {
			return errors.New("balancer ", tag, " is a member and has no fallbackTag")
		}
	}
	var cycles []string
	members := false
	for _, component := range components(tags, edges) {
		cycle, withMember := shortestBalancerCycle(component, edges)
		if cycle != "" {
			cycles = append(cycles, cycle)
			members = members || withMember
		}
	}
	if len(cycles) == 0 {
		return nil
	}
	slices.Sort(cycles)
	if !members {
		return errors.New("balancer fallback cycle: ", strings.Join(cycles, "; "))
	}
	return errors.New("balancer cycle: ", strings.Join(cycles, "; "))
}

type graphEdge struct {
	to     string
	member bool
}

// components is Tarjan's strongly connected components of the balancer graph, each sorted.
func components(tags []string, edges map[string][]graphEdge) [][]string {
	index, low, onStack := map[string]int{}, map[string]int{}, map[string]bool{}
	var stack []string
	var out [][]string
	var visit func(string)
	visit = func(v string) {
		index[v], low[v] = len(index), len(index)
		stack = append(stack, v)
		onStack[v] = true
		for _, e := range edges[v] {
			if _, seen := index[e.to]; !seen {
				visit(e.to)
				low[v] = min(low[v], low[e.to])
			} else if onStack[e.to] {
				low[v] = min(low[v], index[e.to])
			}
		}
		if low[v] == index[v] {
			var component []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				component = append(component, w)
				if w == v {
					break
				}
			}
			slices.Sort(component)
			out = append(out, component)
		}
	}
	for _, tag := range tags {
		if _, seen := index[tag]; !seen {
			visit(tag)
		}
	}
	return out
}

// shortestBalancerCycle prints the shortest cycle through a component's smallest balancer, "" for a
// single balancer without an edge to itself, and says whether a member edge is on it.
func shortestBalancerCycle(component []string, edges map[string][]graphEdge) (string, bool) {
	start, in := component[0], map[string]bool{}
	for _, v := range component {
		in[v] = true
	}
	type step struct {
		from string
		edge graphEdge
	}
	parent := map[string]step{}
	for queue := []string{start}; len(queue) > 0; queue = queue[1:] {
		v := queue[0]
		for _, e := range edges[v] {
			if !in[e.to] {
				continue
			}
			if e.to == start {
				path := []graphEdge{e}
				for at := v; at != start; at = parent[at].from {
					path = append(path, parent[at].edge)
				}
				slices.Reverse(path)
				return formatBalancerCycle(start, path)
			}
			if _, seen := parent[e.to]; !seen {
				parent[e.to] = step{from: v, edge: e}
				queue = append(queue, e.to)
			}
		}
	}
	return "", false
}

func formatBalancerCycle(start string, path []graphEdge) (string, bool) {
	withMember := slices.ContainsFunc(path, func(e graphEdge) bool { return e.member })
	var b strings.Builder
	b.WriteString(start)
	for _, e := range path {
		switch {
		case !withMember:
			b.WriteString(" -> ")
		case e.member:
			b.WriteString(" -[member]-> ")
		default:
			b.WriteString(" -[fallback]-> ")
		}
		b.WriteString(e.to)
	}
	return b.String(), withMember
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
// balancer of m (B1-B4) or one of its member balancers (Core spec019 M3), through that balancer.
// Every published map passed L1 (spec019 M7), so more than len(m)+1 levels is a Core defect: the
// pick is counted, logged and dropped, never sent to the default outbound (L6, spec019 M8).
func (r *Router) resolveChain(first *Balancer, m map[string]*Balancer) (string, error) {
	b := first
	for range len(m) + 1 {
		tag, fallback, err := b.pick()
		if err != nil || !fallback && !slices.Contains(b.members, tag) {
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
