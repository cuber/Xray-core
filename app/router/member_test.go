package router

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	routing_session "github.com/xtls/xray-core/features/routing/session"
	"github.com/xtls/xray-core/proxy/blackhole"
	"github.com/xtls/xray-core/transport"
	_ "github.com/xtls/xray-core/transport/internet/tagged/taggedimpl" // the observatory's probe dialer
)

// Core spec019: balancers as members.

// memberManager selects a selector's entries as they are and reports the listed tags as blackholes.
type memberManager struct {
	outbound.Manager
	blackholes map[string]bool
}

func (*memberManager) Select(selectors []string) []string { return selectors }

func (m *memberManager) GetHandler(tag string) outbound.Handler {
	if m.blackholes[tag] {
		return &blackholeHandler{tag: tag}
	}
	return nil
}

type blackholeHandler struct{ tag string }

func (h *blackholeHandler) Start() error                              { return nil }
func (h *blackholeHandler) Close() error                              { return nil }
func (h *blackholeHandler) Tag() string                               { return h.tag }
func (h *blackholeHandler) Dispatch(context.Context, *transport.Link) {}
func (h *blackholeHandler) SenderSettings() *serial.TypedMessage      { return nil }
func (h *blackholeHandler) ProxySettings() *serial.TypedMessage {
	return serial.ToTypedMessage(&blackhole.Config{})
}

type level struct {
	tag, strategy, fallback string
	selector, members       []string
	weights                 map[string]float32
}

// observedAs is an observation: up tags alive at their delay, down tags measured and dead.
func observedAs(up map[string]time.Duration, down ...string) *contractObserver {
	var statuses []*observatory.OutboundStatus
	for tag, delay := range up {
		statuses = append(statuses, weightedStatus(tag, delay, 5, 0))
	}
	for _, tag := range down {
		statuses = append(statuses, weightedStatus(tag, 10*time.Millisecond, 5, 5))
	}
	return &contractObserver{result: &observatory.ObservationResult{Status: statuses}}
}

// memberMap builds the levels as one published map, each strategy reading o as Balancer.InjectContext
// would hand it over: wrapped for a level with members.
func memberMap(t *testing.T, o extension.Observatory, levels ...level) map[string]*Balancer {
	t.Helper()
	ohm := &memberManager{blackholes: map[string]bool{"block": true}}
	m := map[string]*Balancer{}
	for _, l := range levels {
		settings := strategySettings(l.strategy)
		if l.strategy == "weightedleastping" {
			config := &StrategyWeightedLeastPingConfig{MinSamples: 1}
			for match, value := range l.weights {
				config.Weights = append(config.Weights, &StrategyWeight{Match: match, Value: value})
			}
			settings = serial.ToTypedMessage(config)
		}
		b, err := (&BalancingRule{
			Tag: l.tag, Strategy: l.strategy, OutboundSelector: l.selector,
			FallbackTag: l.fallback, Balancers: l.members, StrategySettings: settings,
		}).Build(ohm, nil)
		if err != nil {
			t.Fatal(err)
		}
		m[b.tag] = b
	}
	for _, b := range m {
		b.peers.Store(&m)
		reader := o
		if len(b.members) > 0 {
			reader = &memberObservatory{Observatory: o, balancer: b}
		}
		observe(b, reader)
	}
	if err := checkBalancerGraph(m); err != nil {
		t.Fatal(err)
	}
	return m
}

func picks(t *testing.T, m map[string]*Balancer, tag string, n int) map[string]int {
	t.Helper()
	count := map[string]int{}
	for range n {
		count[pickFrom(t, routerWith(m), tag)]++
	}
	return count
}

// FR-001 (M1-M3): candidates are the selector's outbounds, then the members; a pick of a member goes
// on inside it; the outer weights apply to the member as a node, and none reach inside it.
func TestMembersArePickedAsNodes(t *testing.T) {
	o := observedAs(map[string]time.Duration{
		"a-hy2": 50 * time.Millisecond, "a-anytls": 80 * time.Millisecond,
		"b-hy2": 40 * time.Millisecond, "b-anytls": 30 * time.Millisecond, "x": 60 * time.Millisecond,
	})
	m := memberMap(t, o,
		level{
			tag: "route", strategy: "weightedleastping", fallback: "block", selector: []string{"x"},
			members: []string{"a", "b"}, weights: map[string]float32{"a": 2, "b": 1, "x": 1},
		},
		level{tag: "a", strategy: "leastping", fallback: "a-hy2", selector: []string{"a-hy2", "a-anytls"}},
		level{tag: "b", strategy: "leastping", fallback: "b-hy2", selector: []string{"b-hy2", "b-anytls"}},
	)
	if got, _ := m["route"].SelectOutbounds(); fmt.Sprint(got) != "[x a b]" {
		t.Fatalf("candidates = %v", got)
	}
	// Node a weighs 2, b and x 1, whatever their protocols; inside a node the faster protocol.
	if got := picks(t, m, "route", 400); fmt.Sprint(got) != "map[a-hy2:200 b-anytls:100 x:100]" {
		t.Fatalf("picks = %v", got)
	}
}

// FR-002 (M4): a member's status comes from its override, its level or its fallback.
func TestMemberStatusBubblesUp(t *testing.T) {
	status := func(m map[string]*Balancer, o *contractObserver, tag string) string {
		s := newStatusReader(m, o.result.(*observatory.ObservationResult).Status,
			&memberManager{blackholes: map[string]bool{"block": true}}).balancer(tag)
		if s == nil {
			return "unknown"
		}
		return fmt.Sprintf("%s alive=%v delay=%d", s.OutboundTag, s.Alive, s.Delay)
	}
	o := observedAs(map[string]time.Duration{
		"p": 30 * time.Millisecond, "q": 10 * time.Millisecond,
		"f": 99 * time.Millisecond,
	}, "d1", "d2")
	m := memberMap(t, o,
		level{tag: "up", strategy: "leastping", fallback: "block", selector: []string{"p", "q"}},
		level{tag: "to-outbound", strategy: "leastping", fallback: "f", selector: []string{"d1", "d2"}},
		level{tag: "to-balancer", strategy: "leastping", fallback: "up", selector: []string{"d1"}},
		level{tag: "to-blackhole", strategy: "leastping", fallback: "block", selector: []string{"d1", "d2"}},
		level{tag: "fresh", strategy: "leastping", fallback: "block", selector: []string{"d1", "unseen"}},
	)
	for tag, want := range map[string]string{
		"up":           "up alive=true delay=10",
		"to-outbound":  "to-outbound alive=true delay=99",
		"to-balancer":  "to-balancer alive=true delay=10",
		"to-blackhole": "to-blackhole alive=false delay=0",
		"fresh":        "unknown",
	} {
		if got := status(m, o, tag); got != want {
			t.Errorf("%s: %s, want %s", tag, got, want)
		}
	}
	m["up"].override.Put("p")
	if got := status(m, o, "up"); got != "up alive=true delay=30" {
		t.Errorf("override: %s", got)
	}
}

// FR-002 (M5): every strategy as the outer level reads a down member as down and never picks it.
func TestOuterStrategiesSkipADownMember(t *testing.T) {
	o := observedAs(map[string]time.Duration{"b-hy2": 20 * time.Millisecond}, "a-hy2", "a-anytls")
	for _, strategy := range []string{"weightedleastping", "leastping", "roundrobin", "random"} {
		t.Run(strategy, func(t *testing.T) {
			m := memberMap(t, o,
				level{tag: "route", strategy: strategy, fallback: "out", members: []string{"a", "b"}},
				level{tag: "a", strategy: "leastping", fallback: "block", selector: []string{"a-hy2", "a-anytls"}},
				level{tag: "b", strategy: "leastping", fallback: "block", selector: []string{"b-hy2"}},
			)
			if got := picks(t, m, "route", 20); fmt.Sprint(got) != "map[b-hy2:20]" {
				t.Fatalf("picks = %v", got)
			}
		})
	}
}

// FR-002: at a fresh start nothing is observed; a leastPing member takes its fallback, never the
// default outbound.
func TestAFreshLeastPingMemberUsesItsFallback(t *testing.T) {
	m := memberMap(t, observedAs(nil),
		level{tag: "route", strategy: "weightedleastping", fallback: "out", members: []string{"a"}},
		level{tag: "a", strategy: "leastping", fallback: "a-anytls", selector: []string{"a-hy2", "a-anytls"}},
	)
	if got := picks(t, m, "route", 5); fmt.Sprint(got) != "map[a-anytls:5]" {
		t.Fatalf("picks = %v", got)
	}
}

// FR-004 (M2, M7): member and fallback edges form one graph; the checks and their texts.
func TestBalancerGraphWithMembers(t *testing.T) {
	graph := func(edges ...string) map[string]*Balancer {
		m := map[string]*Balancer{}
		for _, edge := range edges { // "a>b+c,d": fallback b, members c and d
			from, rest, _ := strings.Cut(edge, ">")
			fallback, members, _ := strings.Cut(rest, "+")
			b := &Balancer{tag: from, fallbackTag: fallback}
			if members != "" {
				b.members = strings.Split(members, ",")
			}
			m[from] = b
		}
		return m
	}
	for _, c := range []struct {
		name  string
		edges []string
		want  string
	}{
		{"a member of itself", []string{"a>out+a"}, "balancer cycle: a -[member]-> a"},
		{"two members", []string{"a>out+b", "b>out+a"}, "balancer cycle: a -[member]-> b -[member]-> a"},
		{"a member falling back", []string{"a>out+b", "b>a"}, "balancer cycle: a -[member]-> b -[fallback]-> a"},
		{"fallbacks alone keep spec017's text", []string{"t>out+x", "x>y", "y>x"}, "balancer fallback cycle: x -> y -> x"},
		{"a member that is no balancer", []string{"a>out+zz"}, "balancer a: member zz is no balancer"},
		{"a member listed twice", []string{"a>out+b,b", "b>out"}, "balancer a: member b is listed twice"},
		{"a member without a fallback", []string{"a>out+b", "b>"}, "balancer b is a member and has no fallbackTag"},
	} {
		if err := checkBalancerGraph(graph(c.edges...)); err == nil || !strings.HasSuffix(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	var dag []string
	for i := range 32 {
		dag = append(dag, fmt.Sprintf("l%02d>out+l%02d,l%02d", i, i+1, i+2))
	}
	dag = append(dag, "l32>out", "l33>out")
	if err := checkBalancerGraph(graph(dag...)); err != nil {
		t.Fatalf("a 32-level DAG: %v", err)
	}
}

// FR-005 (M9): a loopback reached only through a member balancer is a loop spec018 S2 finds.
func TestLoopbackThroughAMember(t *testing.T) {
	outer := &Balancer{tag: "outer", fallbackTag: "direct", members: []string{"n1"}}
	inner := &Balancer{tag: "n1", fallbackTag: "direct", selectors: []string{"lo"}}
	err := loopbackLoops(&loopGraph{
		rules:     []*Rule{{Balancer: outer, RuleTag: "r", Config: ruleConfig([]string{"in"}, false)}},
		balancers: map[string]*Balancer{"outer": outer, "n1": inner},
	}, []outbound.Handler{out("direct"), lo("lo", "in")})
	want := "loopback loop: in -> [rule r, balancer outer member n1] lo -> in"
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("%v, want %q", err, want)
	}
}

// FR-007: the shape of xray-config spec021 on a started Core, picking while the rules are replaced by
// a flat shape and back, and a member cycle is refused; with -race.
func TestMembersUnderConcurrentReloads(t *testing.T) {
	var outbounds []*core.OutboundHandlerConfig
	for _, tag := range []string{"out", "x", "n1-hy2", "n1-anytls", "n2-hy2"} {
		outbounds = append(outbounds, &core.OutboundHandlerConfig{Tag: tag, ProxySettings: serial.ToTypedMessage(&blackhole.Config{})})
	}
	rule := &RoutingRule{TargetTag: &RoutingRule_BalancingTag{BalancingTag: "route"}, Networks: []net.Network{net.Network_TCP}}
	nested := &Config{Rule: []*RoutingRule{rule}, BalancingRule: []*BalancingRule{
		{Tag: "route", Strategy: "roundrobin", OutboundSelector: []string{"x"}, Balancers: []string{"n1", "n2"}, FallbackTag: "out"},
		{Tag: "n1", Strategy: "leastping", OutboundSelector: []string{"n1-"}, FallbackTag: "n1-anytls"},
		{Tag: "n2", Strategy: "leastping", OutboundSelector: []string{"n2-"}, FallbackTag: "n2-hy2"},
	}}
	flat := &Config{Rule: []*RoutingRule{rule}, BalancingRule: []*BalancingRule{
		{Tag: "route", Strategy: "roundrobin", OutboundSelector: []string{"x", "n1-hy2"}, FallbackTag: "out"},
	}}
	cyclic := &Config{Rule: []*RoutingRule{rule}, BalancingRule: []*BalancingRule{
		{Tag: "route", Strategy: "roundrobin", Balancers: []string{"n1"}, FallbackTag: "out"},
		{Tag: "n1", Strategy: "leastping", OutboundSelector: []string{"n1-"}, FallbackTag: "route"},
	}}
	instance, err := core.New(&core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(nested),
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&observatory.Config{SubjectSelector: []string{"x", "n1-", "n2-", "out"}, ProbeInterval: int64(time.Hour)}),
		},
		Outbound: outbounds,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { instance.Close() })
	r := instance.GetFeature(routing.RouterType()).(*Router)
	allowed := map[string]bool{"x": true, "n1-hy2": true, "n1-anytls": true, "n2-hy2": true, "out": true}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 200 {
				ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{
					Target: net.TCPDestination(net.DomainAddress("example.com"), 80),
				}})
				route, err := r.PickRoute(routing_session.AsRoutingContext(ctx))
				if err != nil {
					t.Errorf("PickRoute: %v", err)
					return
				}
				if tag := route.GetOutboundTag(); !allowed[tag] {
					t.Errorf("route = %q", tag)
					return
				}
			}
		})
	}
	wg.Go(func() {
		for range 50 {
			for _, c := range []*Config{flat, nested} {
				if err := r.ReloadRules(c, false); err != nil {
					t.Error(err)
				}
			}
			if err := r.ReloadRules(cyclic, false); err == nil || !strings.Contains(err.Error(), "balancer cycle: n1 -[fallback]-> route -[member]-> n1") {
				t.Errorf("cyclic reload = %v", err)
			}
		}
	})
	wg.Wait()
	if chain, _ := r.LoopGuards(); chain != 0 {
		t.Fatalf("fallback_chain guard = %d", chain)
	}
}
