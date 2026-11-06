package router

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/outbound"
)

// Core spec018 S2: can routing bring a connection a loopback fed back into routing to a loopback
// again? The states are the inbound tags loopbacks feed; only through them does a connection come
// back. Every condition but the inbound tag may hold (S2.2), so this answers whether a loop is
// possible, which is what start refuses.

// notConditions are the RoutingRule fields that say where a rule leads, not when it matches. Any
// other field, an upstream addition included, counts as a condition that may hold.
var notConditions = map[protoreflect.Name]bool{"tag": true, "balancing_tag": true, "rule_tag": true, "webhook": true}

// matchesOnInboundOnly says whether the inbound tag is the rule's only condition: for a state it
// names, the rule then certainly matches and later rules are never reached.
func matchesOnInboundOnly(rule *RoutingRule) bool {
	if rule == nil || len(rule.InboundTag) == 0 {
		return false
	}
	only := true
	rule.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if name := fd.Name(); name != "inbound_tag" && !notConditions[name] {
			only = false
		}
		return only
	})
	return only
}

// loopEdge leads from a state to the state of the loopback that via reaches, by the step label.
type loopEdge struct {
	to, label, via string
}

// loopGraph holds what one S2 check reads.
type loopGraph struct {
	rules     []*Rule
	balancers map[string]*Balancer
	override  map[string]string // a target about to be set (S3.2), over each balancer's current one
	byTag     map[string]outbound.Handler
	tags      []string
	def       outbound.Handler
}

func (g *loopGraph) reach(h outbound.Handler, label string, out *[]loopEdge) {
	if h == nil {
		return
	}
	if inbound, path, ok := outbound.LoopbackReached(h, g.byTag); ok {
		*out = append(*out, loopEdge{to: inbound, label: label, via: path})
	}
}

// chain adds every outbound a rule's balancer may lead to: at each level of its fallback chain the
// candidates its selectors match, every level of its member balancers (Core spec019 M9), its
// override target, and after the last level its fallback outbound, or the default outbound when it
// has none (spec017 B4).
func (g *loopGraph) chain(b *Balancer, label string, out *[]loopEdge) {
	g.level(b, label+", balancer "+b.tag, out, map[string]bool{})
}

func (g *loopGraph) level(b *Balancer, level string, out *[]loopEdge, seen map[string]bool) {
	for b != nil && !seen[b.tag] {
		seen[b.tag] = true
		for _, tag := range g.tags {
			for _, selector := range b.selectors {
				if strings.HasPrefix(tag, selector) {
					g.reach(g.byTag[tag], level, out)
					break
				}
			}
		}
		for _, member := range b.members {
			if inner, ok := g.balancers[member]; ok {
				g.level(inner, level+" member "+member, out, seen)
			}
		}
		target, set := g.override[b.tag]
		if !set {
			target = b.override.Get()
		}
		if target != "" {
			g.reach(g.byTag[target], level+" override", out)
		}
		next, isBalancer := g.balancers[b.fallbackTag]
		switch {
		case b.fallbackTag == "":
			g.reach(g.def, level+" default", out)
			b = nil
		case isBalancer:
			level += " -> " + next.tag
			b = next
		default:
			g.reach(g.byTag[b.fallbackTag], level+" fallback", out)
			b = nil
		}
	}
}

// from lists the edges of a state, in rule order, then the default outbound unless a rule that
// matches on the state's inbound tag alone ended the walk.
func (g *loopGraph) from(state string) []loopEdge {
	var out []loopEdge
	for i, rule := range g.rules {
		inbound := rule.Config.GetInboundTag()
		if len(inbound) > 0 && !slices.Contains(inbound, state) {
			continue
		}
		label := "rule " + rule.RuleTag
		if rule.RuleTag == "" {
			label = "rule #" + strconv.Itoa(i+1)
		}
		if rule.Balancer != nil {
			g.chain(rule.Balancer, label, &out)
		} else {
			g.reach(g.byTag[rule.Tag], label, &out)
		}
		if matchesOnInboundOnly(rule.Config) {
			return out
		}
	}
	g.reach(g.def, "default", &out)
	return out
}

// loopbackLoops refuses a routing graph in which a state reaches a state on a cycle, itself
// included (S2.4); one cycle is printed per strongly connected set of states (S4).
func loopbackLoops(g *loopGraph, handlers []outbound.Handler) error {
	g.byTag = make(map[string]outbound.Handler, len(handlers))
	states := map[string]bool{}
	for _, h := range handlers {
		if tag := h.Tag(); tag != "" {
			g.byTag[tag] = h
		}
		if view, ok := h.(outbound.LoopView); ok {
			if inbound, loop := view.LoopbackInbound(); loop {
				states[inbound] = true
			}
		}
	}
	if len(states) == 0 {
		return nil
	}
	g.tags = slices.Sorted(maps.Keys(g.byTag))
	edges := make(map[string][]loopEdge, len(states))
	for state := range states {
		edges[state] = g.from(state)
	}
	var cycles []string
	for _, component := range stronglyConnected(edges) {
		if cycle := shortestCycle(edges, component); cycle != "" {
			cycles = append(cycles, cycle)
		}
	}
	if len(cycles) == 0 {
		return nil
	}
	slices.Sort(cycles)
	return errors.New("loopback loop: ", strings.Join(cycles, "; "))
}

// stronglyConnected returns the strongly connected components of the state graph (Tarjan), each
// sorted, the states visited in sorted order.
func stronglyConnected(edges map[string][]loopEdge) [][]string {
	index, low := map[string]int{}, map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var components [][]string
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
			components = append(components, component)
		}
	}
	for _, v := range slices.Sorted(maps.Keys(edges)) {
		if _, seen := index[v]; !seen {
			visit(v)
		}
	}
	return components
}

// shortestCycle prints the shortest cycle through the component's smallest state, edges in rule
// order: "in -> [rule sys:x] lo -> in". A single state without an edge to itself has none.
func shortestCycle(edges map[string][]loopEdge, component []string) string {
	start := component[0]
	member := map[string]bool{}
	for _, v := range component {
		member[v] = true
	}
	type step struct {
		from string
		edge loopEdge
	}
	parent := map[string]step{}
	queue := []string{start}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, e := range edges[v] {
			if !member[e.to] {
				continue
			}
			if e.to == start {
				path := []loopEdge{e}
				for at := v; at != start; at = parent[at].from {
					path = append(path, parent[at].edge)
				}
				slices.Reverse(path)
				var b strings.Builder
				b.WriteString(start)
				for _, p := range path {
					b.WriteString(" -> [" + p.label + "] " + p.via + " -> " + p.to)
				}
				return b.String()
			}
			if _, seen := parent[e.to]; !seen && e.to != start {
				parent[e.to] = step{from: v, edge: e}
				queue = append(queue, e.to)
			}
		}
	}
	return ""
}

// CheckLoopbackLoops implements outbound.LoopChecker on the published rules and balancers (S3.1,
// S3.3), with Core spec019 M6. The first call is the start check: from then on a routing change
// runs M6 too, when the handlers and the observatory are in place.
func (r *Router) CheckLoopbackLoops(handlers []outbound.Handler, def outbound.Handler) error {
	balancers := *r.balancers.Load()
	if err := r.checkMemberObservation(balancers, handlers); err != nil {
		return err
	}
	r.started.Store(true)
	return r.checkLoopbacks(*r.rules.Load(), balancers, nil, handlers, def)
}

// checkLoopbacks is S2 over the given routing. A routing script, which replaces the rules, is not
// decidable; L7 ends its loops at run time (L-F).
func (r *Router) checkLoopbacks(rules []*Rule, balancers map[string]*Balancer, override map[string]string,
	handlers []outbound.Handler, def outbound.Handler,
) error {
	if r.script != nil {
		return nil
	}
	return loopbackLoops(&loopGraph{rules: rules, balancers: balancers, override: override, def: def}, handlers)
}

// checkMemberObservation is Core spec019 M6: every balancer with members, and every balancer reached
// from one through members or fallbacks, has every outbound candidate and outbound fallback observed;
// a blackhole fallback needs none.
func (r *Router) checkMemberObservation(m map[string]*Balancer, handlers []outbound.Handler) error {
	reached := map[string]bool{}
	var reach func(string)
	reach = func(tag string) {
		b, ok := m[tag]
		if !ok || reached[tag] {
			return
		}
		reached[tag] = true
		for _, member := range b.members {
			reach(member)
		}
		reach(b.fallbackTag)
	}
	for _, tag := range slices.Sorted(maps.Keys(m)) {
		if len(m[tag].members) > 0 {
			reach(tag)
		}
	}
	if len(reached) == 0 {
		return nil
	}
	observes := r.observes()
	byTag := make(map[string]outbound.Handler, len(handlers))
	for _, h := range handlers {
		byTag[h.Tag()] = h
	}
	for _, tag := range slices.Sorted(maps.Keys(reached)) {
		b := m[tag]
		var outbounds []string
		for _, h := range slices.Sorted(maps.Keys(byTag)) {
			for _, selector := range b.selectors {
				if strings.HasPrefix(h, selector) {
					outbounds = append(outbounds, h)
					break
				}
			}
		}
		if _, isBalancer := m[b.fallbackTag]; !isBalancer && b.fallbackTag != "" {
			if h := byTag[b.fallbackTag]; h == nil || h.ProxySettings().GetType() != "xray.proxy.blackhole.Config" {
				outbounds = append(outbounds, b.fallbackTag)
			}
		}
		for _, outboundTag := range outbounds {
			if !observes(outboundTag) {
				return errors.New("balancer ", tag, ": outbound ", outboundTag,
					" is not observed (balancer members need every outbound observed)")
			}
		}
	}
	return nil
}

// observes reads the instance's observatory (spec019 M6); without one nothing is observed.
func (r *Router) observes() func(string) bool {
	if r.ctx != nil {
		if instance := core.FromContext(r.ctx); instance != nil {
			if observed, ok := instance.GetFeature(extension.ObservatoryType()).(extension.ObservedOutbounds); ok {
				return observed.Observes
			}
		}
	}
	return func(string) bool { return false }
}

// guardRouting runs S2 on a routing change about to be published, against the current handlers
// (S3.2). A router over a manager that is no outbound.GraphGuard (a test's mock) is not checked.
func (r *Router) guardRouting(rules []*Rule, balancers map[string]*Balancer, override map[string]string) error {
	if _, ok := r.ohm.(outbound.GraphGuard); !ok {
		return nil
	}
	handlers := r.ohm.ListHandlers(r.ctx)
	if r.started.Load() { // before the start check, handlers are still being added (spec019 M6)
		if err := r.checkMemberObservation(balancers, handlers); err != nil {
			return err
		}
	}
	return r.checkLoopbacks(rules, balancers, override, handlers, r.ohm.GetDefaultHandler())
}

// lockGraph serialises a routing change and its check with handler changes (S3.4). It is taken
// before r.mu; the manager's checks read the published rules without r.mu.
func (r *Router) lockGraph() func() {
	if guard, ok := r.ohm.(outbound.GraphGuard); ok {
		return guard.LockGraph()
	}
	return func() {}
}
