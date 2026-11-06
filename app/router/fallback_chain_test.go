package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/app/proxyman"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/routing"
	routing_session "github.com/xtls/xray-core/features/routing/session"
	"github.com/xtls/xray-core/proxy/blackhole"
)

// Core spec017: balancer fallback chains and their loop checks.

// observe gives b's strategy an observer, as InjectContext would.
func observe(b *Balancer, o extension.Observatory) {
	ctx := context.Background()
	switch s := b.strategy.(type) {
	case *RoundRobinStrategy:
		s.ctx, s.observatory = ctx, o
	case *RandomStrategy:
		s.ctx, s.observatory = ctx, o
	case *LeastPingStrategy:
		s.ctx, s.observatory = ctx, o
	case *LeastLoadStrategy:
		s.ctx, s.observer = ctx, o
	case *WeightedLeastPingStrategy:
		s.ctx, s.observer = ctx, o
	}
}

// observer reports every candidate measured, down when listed.
func observer(down ...string) *contractObserver {
	var status []*observatory.OutboundStatus
	for _, tag := range []string{"c1", "c2", "c3", "p", "q"} {
		fail := int64(0)
		for _, d := range down {
			if d == tag {
				fail = 5
			}
		}
		status = append(status, weightedStatus(tag, 10*time.Millisecond, 5, fail))
	}
	return &contractObserver{result: &observatory.ObservationResult{Status: status}}
}

func strategySettings(strategy string) *serial.TypedMessage {
	switch strategy {
	case "leastload":
		return serial.ToTypedMessage(&StrategyLeastLoadConfig{})
	case "weightedleastping":
		return serial.ToTypedMessage(&StrategyWeightedLeastPingConfig{MinSamples: 1})
	}
	return nil
}

func buildBalancer(t *testing.T, tag, strategy, fallback string, selector ...string) *Balancer {
	t.Helper()
	b, err := (&BalancingRule{
		Tag: tag, Strategy: strategy, OutboundSelector: selector, FallbackTag: fallback,
		StrategySettings: strategySettings(strategy),
	}).Build(&luaRouteOutboundManager{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// chainOf is l1 -> ... -> l<levels> -> out, level i selecting ci, every level observed by o.
func chainOf(t *testing.T, strategy string, levels int, o extension.Observatory) map[string]*Balancer {
	t.Helper()
	m := map[string]*Balancer{}
	for i := 1; i <= levels; i++ {
		fallback := fmt.Sprintf("l%d", i+1)
		if i == levels {
			fallback = "out"
		}
		b := buildBalancer(t, fmt.Sprintf("l%d", i), strategy, fallback, fmt.Sprintf("c%d", i))
		observe(b, o)
		m[b.tag] = b
	}
	return m
}

func routerWith(m map[string]*Balancer) *Router {
	r := &Router{}
	r.balancers.Store(&m)
	return r
}

func pickFrom(t *testing.T, r *Router, tag string) string {
	t.Helper()
	got, err := r.pickTag(&Rule{Balancer: (*r.balancers.Load())[tag]})
	if err != nil {
		t.Fatalf("pick %s: %v", tag, err)
	}
	return got
}

// FR-001 (B1, B2): two- and three-level chains over every strategy end where a direct reference
// to the level they reach would.
func TestFallbackChainEveryStrategy(t *testing.T) {
	for _, strategy := range []string{"roundrobin", "random", "leastping", "leastload", "weightedleastping"} {
		for _, levels := range []int{2, 3} {
			t.Run(fmt.Sprintf("%s/%d", strategy, levels), func(t *testing.T) {
				o := observer()
				r := routerWith(chainOf(t, strategy, levels, o))
				for downTo := 0; downTo <= levels; downTo++ {
					var down []string
					for i := 1; i <= downTo; i++ {
						down = append(down, fmt.Sprintf("c%d", i))
					}
					o.result = observer(down...).result
					want := "out"
					if downTo < levels {
						want = fmt.Sprintf("c%d", downTo+1)
						if direct := pickFrom(t, r, fmt.Sprintf("l%d", downTo+1)); direct != want {
							t.Fatalf("direct l%d = %q, want %q", downTo+1, direct, want)
						}
					}
					if got := pickFrom(t, r, "l1"); got != want {
						t.Fatalf("down %v: chain = %q, want %q", down, got, want)
					}
				}
			})
		}
	}
}

// FR-001 (B3): a candidate or override target equal to a balancer tag ends the pick.
func TestFallbackChainResolvesFallbackResultsOnly(t *testing.T) {
	o := observer() // "l2" is no observed outbound, so it counts as alive
	l1 := buildBalancer(t, "l1", "roundrobin", "l2", "l2")
	l2 := buildBalancer(t, "l2", "roundrobin", "out", "c2")
	observe(l1, o)
	observe(l2, o)
	r := routerWith(map[string]*Balancer{"l1": l1, "l2": l2})
	if got := pickFrom(t, r, "l1"); got != "l2" {
		t.Fatalf("candidate named like a balancer = %q, want l2", got)
	}
	l1.selectors = []string{"c1"}
	o.result = observer("c1").result
	l1.override.Put("l2")
	if got := pickFrom(t, r, "l1"); got != "l2" {
		t.Fatalf("override named like a balancer = %q, want l2", got)
	}
}

// FR-002 (B4): a chain ending at an outbound uses it; a last balancer with neither candidate nor
// fallback is an error, so the dispatcher takes the default outbound.
func TestFallbackChainEnd(t *testing.T) {
	o := observer("c1", "c2")
	l1 := buildBalancer(t, "l1", "roundrobin", "l2", "c1")
	l2 := buildBalancer(t, "l2", "leastping", "", "c2")
	observe(l1, o)
	observe(l2, o)
	r := routerWith(map[string]*Balancer{"l1": l1, "l2": l2})
	got, err := r.pickTag(&Rule{Balancer: l1})
	if err == nil || got != "" || err == routing.ErrRouteDropped {
		t.Fatalf("chain without an end = %q, %v; want the strategy's error", got, err)
	}
	l2.fallbackTag = "out"
	if got := pickFrom(t, r, "l1"); got != "out" {
		t.Fatalf("chain to an outbound = %q, want out", got)
	}
}

// FR-003 (B5, B6): an outer override prevents the fallback, an inner one applies; one round-robin
// index across direct and fallback use.
func TestFallbackChainOverridesAndSharedIndex(t *testing.T) {
	o := observer("c1")
	l1 := buildBalancer(t, "l1", "roundrobin", "l2", "c1")
	l2 := buildBalancer(t, "l2", "roundrobin", "out", "p", "q")
	observe(l1, o)
	observe(l2, o)
	r := routerWith(map[string]*Balancer{"l1": l1, "l2": l2})

	if got := []string{pickFrom(t, r, "l2"), pickFrom(t, r, "l1"), pickFrom(t, r, "l2")}; fmt.Sprint(got) != "[p q p]" {
		t.Fatalf("direct, fallback, direct = %v, want [p q p]", got)
	}
	l2.override.Put("inner")
	if got := pickFrom(t, r, "l1"); got != "inner" {
		t.Fatalf("inner override = %q", got)
	}
	l1.override.Put("outer")
	if got := pickFrom(t, r, "l1"); got != "outer" {
		t.Fatalf("outer override = %q", got)
	}
}

// FR-004 (L1, L4): cycles are rejected with every cycle, from its smallest tag, in tag order; an
// acyclic chain of 64 levels is accepted and resolves to its end.
func TestFallbackCycleCheck(t *testing.T) {
	balancers := func(edges ...string) map[string]*Balancer {
		m := map[string]*Balancer{}
		for _, edge := range edges {
			from, to, _ := strings.Cut(edge, ">")
			m[from] = &Balancer{tag: from, fallbackTag: to}
		}
		return m
	}
	for _, c := range []struct {
		name  string
		edges []string
		want  string
	}{
		{"self", []string{"a>a"}, "a -> a"},
		{"two", []string{"a>b", "b>a"}, "a -> b -> a"},
		{"three", []string{"c>a", "a>b", "b>c"}, "a -> b -> c -> a"},
		{"tail into a cycle", []string{"a>b", "b>c", "c>b"}, "b -> c -> b"},
		{"two cycles", []string{"d>c", "c>d", "b>a", "a>b", "e>out"}, "a -> b -> a; c -> d -> c"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := checkBalancerGraph(balancers(c.edges...))
			if err == nil || !strings.HasSuffix(err.Error(), "balancer fallback cycle: "+c.want) {
				t.Fatalf("checkBalancerGraph = %v, want %q", err, c.want)
			}
		})
	}
	if err := checkBalancerGraph(balancers("a>b", "b>out", "c>", "d>b")); err != nil {
		t.Fatalf("acyclic map rejected: %v", err)
	}

	o := observer("c1")
	m := map[string]*Balancer{}
	for i := range 64 {
		fallback := fmt.Sprintf("l%02d", i+1)
		if i == 63 {
			fallback = "end"
		}
		b := buildBalancer(t, fmt.Sprintf("l%02d", i), "roundrobin", fallback, "c1")
		observe(b, o)
		m[b.tag] = b
	}
	if err := checkBalancerGraph(m); err != nil {
		t.Fatalf("64 levels rejected: %v", err)
	}
	if got := pickFrom(t, routerWith(m), "l00"); got != "end" {
		t.Fatalf("64 levels end at %q", got)
	}
}

// FR-006 (L6): a cyclic map that bypassed the check (white box) ends the pick, counts it and drops
// the connection, also when the first level comes from an earlier map.
func TestFallbackChainGuard(t *testing.T) {
	o := observer("c1", "c2")
	a := buildBalancer(t, "a", "roundrobin", "b", "c1")
	b := buildBalancer(t, "b", "roundrobin", "a", "c2")
	old := buildBalancer(t, "old", "roundrobin", "a", "c1")
	for _, x := range []*Balancer{a, b, old} {
		observe(x, o)
	}
	r := routerWith(map[string]*Balancer{"a": a, "b": b})
	for i, first := range []*Balancer{a, old} {
		got, err := r.pickTag(&Rule{Balancer: first})
		if got != "" || err != routing.ErrRouteDropped {
			t.Fatalf("cyclic pick from %s = %q, %v", first.tag, got, err)
		}
		if chain, reentry := r.LoopGuards(); chain != uint64(i+1) || reentry != 0 {
			t.Fatalf("guards = %d, %d", chain, reentry)
		}
	}
	if got := staticChain(old, map[string]*Balancer{"a": a, "b": b}); fmt.Sprint(got) != "[old a b a b]" {
		t.Fatalf("logged path = %v", got)
	}
}

// The control plane reads the chain, with each balancer hop's state, on one map.
func TestFallbackChainView(t *testing.T) {
	o := observer("c1")
	r := routerWith(chainOf(t, "roundrobin", 3, o))
	chain, err := r.GetFallbackChain("l1")
	if err != nil {
		t.Fatal(err)
	}
	want := []routing.FallbackHop{
		{Tag: "l2", Balancer: true, Reported: true},
		{Tag: "l3", Balancer: true, Reported: true},
		{Tag: "out"},
	}
	if fmt.Sprint(chain) != fmt.Sprint(want) {
		t.Fatalf("chain = %v, want %v", chain, want)
	}
	o.result = observer("c1", "c2").result
	chain, _ = r.GetFallbackChain("l1")
	if !chain[0].Active || chain[1].Active {
		t.Fatalf("chain with l2 down = %v", chain)
	}
	if chain, _ := r.GetFallbackChain("l3"); fmt.Sprint(chain) != "[{out false false false}]" {
		t.Fatalf("last level's chain = %v", chain)
	}
	if tags := r.Balancers().Tags(); fmt.Sprint(tags) != "[l1 l2 l3]" {
		t.Fatalf("tags = %v", tags)
	}
	if _, err := r.GetFallbackChain("nope"); err == nil {
		t.Fatal("unknown balancer has a chain")
	}
}

// startChainCore starts a Core with config's router and the outbounds c1, c2 and out. Its
// observatory probes nothing, so every candidate counts as alive and a balancer falls back when its
// selector matches no outbound.
func startChainCore(t *testing.T, config *Config) *Router {
	t.Helper()
	var outbounds []*core.OutboundHandlerConfig
	for _, tag := range []string{"c1", "c2", "out"} {
		outbounds = append(outbounds, &core.OutboundHandlerConfig{Tag: tag, ProxySettings: serial.ToTypedMessage(&blackhole.Config{})})
	}
	instance, err := core.New(&core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(config),
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&observatory.Config{SubjectSelector: []string{"probe-none"}, ProbeInterval: int64(time.Hour)}),
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
	return instance.GetFeature(routing.RouterType()).(*Router)
}

func chainRule(balancer string) *RoutingRule {
	return &RoutingRule{
		RuleTag: "to-" + balancer, TargetTag: &RoutingRule_BalancingTag{BalancingTag: balancer},
		Networks: []net.Network{net.Network_TCP},
	}
}

func routeOf(t *testing.T, r *Router) string {
	t.Helper()
	ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{
		Target: net.TCPDestination(net.DomainAddress("example.com"), 80),
	}})
	route, err := r.PickRoute(routing_session.AsRoutingContext(ctx))
	if err != nil {
		t.Fatalf("PickRoute: %v", err)
	}
	return route.GetOutboundTag()
}

// chainConfig is l1 (no candidate) -> l2 (c2) -> out with a rule to l1.
func chainConfig() *Config {
	return &Config{
		Rule: []*RoutingRule{chainRule("l1")},
		BalancingRule: []*BalancingRule{
			{Tag: "l1", Strategy: "roundrobin", OutboundSelector: []string{"none"}, FallbackTag: "l2"},
			{Tag: "l2", Strategy: "roundrobin", OutboundSelector: []string{"c2"}, FallbackTag: "out"},
		},
	}
}

// FR-004 (L3) and FR-005 (L2, L5): Init, a replacing and a closing append are rejected; the old map
// keeps picking; RemoveRule and an override leave the graph as it is.
func TestFallbackCycleOnReload(t *testing.T) {
	cyclic := &Config{BalancingRule: []*BalancingRule{
		{Tag: "x", OutboundSelector: []string{"c1"}, FallbackTag: "y"},
		{Tag: "y", OutboundSelector: []string{"c1"}, FallbackTag: "x"},
	}}
	if _, err := core.New(&core.Config{App: []*serial.TypedMessage{
		serial.ToTypedMessage(cyclic),
		serial.ToTypedMessage(&dispatcher.Config{}), serial.ToTypedMessage(&proxyman.OutboundConfig{}),
		serial.ToTypedMessage(&observatory.Config{SubjectSelector: []string{"probe-none"}}),
	}}); err == nil ||
		!strings.Contains(err.Error(), "balancer fallback cycle: x -> y -> x") {
		t.Fatalf("core.New with a cycle = %v", err)
	}

	r := startChainCore(t, chainConfig())
	if got := routeOf(t, r); got != "c2" {
		t.Fatalf("chain route = %q, want c2", got)
	}
	before, _ := r.GetFallbackChain("l1")
	if err := r.ReloadRules(cyclic, false); err == nil || !strings.HasSuffix(err.Error(), "balancer fallback cycle: x -> y -> x") {
		t.Fatalf("replace with a cycle = %v", err)
	}
	// L5: m's fallback n is no balancer when m is appended; appending a balancer n that falls back to
	// m closes m -> n -> m.
	if err := r.ReloadRules(&Config{BalancingRule: []*BalancingRule{
		{Tag: "m", OutboundSelector: []string{"c1"}, FallbackTag: "n"},
	}}, true); err != nil {
		t.Fatal(err)
	}
	closing := &Config{BalancingRule: []*BalancingRule{{Tag: "n", OutboundSelector: []string{"c1"}, FallbackTag: "m"}}}
	if err := r.ReloadRules(closing, true); err == nil || !strings.HasSuffix(err.Error(), "balancer fallback cycle: m -> n -> m") {
		t.Fatalf("append closing a cycle = %v", err)
	}
	if tags := r.Balancers().Tags(); fmt.Sprint(tags) != "[l1 l2 m]" {
		t.Fatalf("balancers after rejections = %v", tags)
	}
	if got := routeOf(t, r); got != "c2" {
		t.Fatalf("route after rejections = %q, want c2", got)
	}
	if err := r.SetOverrideTarget("l2", "c1"); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveRule("to-l1"); err != nil {
		t.Fatal(err)
	}
	if after, _ := r.GetFallbackChain("l1"); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("chain changed from %v to %v", before, after)
	}
}

// L6: a rule read before a replacing reload resolves its chain on the map it was published with.
func TestFallbackChainStaysOnItsMap(t *testing.T) {
	r := startChainCore(t, chainConfig())
	before := (*r.rules.Load())[0]
	if err := r.ReloadRules(&Config{
		Rule:          []*RoutingRule{chainRule("l1")},
		BalancingRule: []*BalancingRule{{Tag: "l1", Strategy: "roundrobin", OutboundSelector: []string{"none"}, FallbackTag: "out"}},
	}, false); err != nil {
		t.Fatal(err)
	}
	if got, err := r.pickTag(before); got != "c2" || err != nil {
		t.Fatalf("earlier rule = %q, %v; want c2 through its own l2", got, err)
	}
	if got := routeOf(t, r); got != "out" {
		t.Fatalf("new rule = %q, want out", got)
	}
}

// FR-006: picks during concurrent reloads end (run under -race in the gate).
func TestFallbackChainConcurrentReloads(t *testing.T) {
	r := startChainCore(t, chainConfig())
	short := &Config{
		Rule:          []*RoutingRule{chainRule("l1")},
		BalancingRule: []*BalancingRule{{Tag: "l1", Strategy: "roundrobin", OutboundSelector: []string{"none"}, FallbackTag: "out"}},
	}
	cyclic := &Config{
		Rule: []*RoutingRule{chainRule("l1")},
		BalancingRule: []*BalancingRule{
			{Tag: "l1", Strategy: "roundrobin", OutboundSelector: []string{"none"}, FallbackTag: "l2"},
			{Tag: "l2", Strategy: "roundrobin", OutboundSelector: []string{"none"}, FallbackTag: "l1"},
		},
	}
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
				if tag := route.GetOutboundTag(); tag != "c2" && tag != "out" {
					t.Errorf("route = %q", tag)
					return
				}
			}
		})
	}
	wg.Go(func() {
		for range 50 {
			for _, c := range []*Config{short, chainConfig()} {
				if err := r.ReloadRules(c, false); err != nil {
					t.Error(err)
				}
			}
			if err := r.ReloadRules(cyclic, false); err == nil {
				t.Error("cyclic reload accepted")
			}
		}
	})
	wg.Wait()
	if chain, _ := r.LoopGuards(); chain != 0 {
		t.Fatalf("fallback_chain guard = %d", chain)
	}
}

// FR-005 (L2): reloadRules is the only writer of the balancer map; Init only stores an empty one.
func TestBalancerMapHasOneWriter(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	store := regexp.MustCompile(`balancers\.Store\((.*)\)`)
	var writes []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range store.FindAllStringSubmatch(string(source), -1) {
			writes = append(writes, file+": "+m[1])
		}
	}
	if fmt.Sprint(writes) != "[router.go: &map[string]*Balancer{} router.go: &newBalancers]" {
		t.Fatalf("balancer map writes = %v", writes)
	}
}

// FR-003 (B7): Lua PickOutbound resolves the chain.
func TestRouterScriptPicksThroughTheChain(t *testing.T) {
	config := chainConfig()
	config.Rule = nil
	config.Script = writeRouteScript(t, `
local router = require("xray.router")
function HandleRoute()
    local tag, err = router.PickOutbound("l1")
    return tag, "chained", err
end`)
	r := startChainCore(t, config)
	route, err := r.PickRoute(&routing_session.Context{})
	if err != nil || route.GetOutboundTag() != "c2" {
		t.Fatalf("Lua route = %v, %v, want c2", route, err)
	}
}

// FR-003 (B7): a rule webhook reports the chain's final outbound.
func TestFallbackChainWebhookReportsTheFinalTag(t *testing.T) {
	tags := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev struct {
			OutboundTag string `json:"outboundTag"`
		}
		if err := json.NewDecoder(r.Body).Decode(&ev); err == nil {
			tags <- ev.OutboundTag
		}
	}))
	t.Cleanup(server.Close)
	config := chainConfig()
	config.Rule[0].Webhook = &WebhookConfig{Url: server.URL}
	r := startChainCore(t, config)
	if got := routeOf(t, r); got != "c2" {
		t.Fatalf("route = %q", got)
	}
	select {
	case tag := <-tags:
		if tag != "c2" {
			t.Fatalf("webhook outboundTag = %q, want c2", tag)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook")
	}
}
