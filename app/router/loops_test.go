package router

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
)

// Core spec018 S2: loops through loopbacks (FR-002, FR-003).

type loopHandler struct {
	tag, via, loopback string
	isLoopback         bool
}

func (h *loopHandler) Start() error                              { return nil }
func (h *loopHandler) Close() error                              { return nil }
func (h *loopHandler) Tag() string                               { return h.tag }
func (h *loopHandler) Dispatch(context.Context, *transport.Link) {}
func (h *loopHandler) SenderSettings() *serial.TypedMessage      { return nil }
func (h *loopHandler) ProxySettings() *serial.TypedMessage       { return nil }
func (h *loopHandler) DialerProxy() string                       { return h.via }
func (h *loopHandler) LoopbackInbound() (string, bool)           { return h.loopback, h.isLoopback }

func out(tag string) outbound.Handler       { return &loopHandler{tag: tag} }
func via(tag, next string) outbound.Handler { return &loopHandler{tag: tag, via: next} }
func lo(tag, inbound string) outbound.Handler {
	return &loopHandler{tag: tag, loopback: inbound, isLoopback: true}
}

// to is a rule to an outbound; inbound nil applies to every inbound, and a domain makes the rule
// conditional.
func to(ruleTag, outboundTag string, inbound []string, domain bool) *Rule {
	return &Rule{Tag: outboundTag, RuleTag: ruleTag, Config: ruleConfig(inbound, domain)}
}

func toBalancer(ruleTag string, b *Balancer, inbound []string, domain bool) *Rule {
	return &Rule{Balancer: b, RuleTag: ruleTag, Config: ruleConfig(inbound, domain)}
}

func ruleConfig(inbound []string, domain bool) *RoutingRule {
	config := &RoutingRule{InboundTag: inbound}
	if domain {
		config.Domain = []*geodata.DomainRule{{}}
	}
	return config
}

// checkLoops runs S2 with the first handler as the default outbound.
func checkLoops(rules []*Rule, balancers map[string]*Balancer, override map[string]string, handlers ...outbound.Handler) error {
	var def outbound.Handler
	if len(handlers) > 0 {
		def = handlers[0]
	}
	return loopbackLoops(&loopGraph{rules: rules, balancers: balancers, override: override, def: def}, handlers)
}

func TestLoopbackLoopsRefused(t *testing.T) {
	b1 := buildBalancer(t, "b1", "roundrobin", "b2", "c1")
	b2 := buildBalancer(t, "b2", "roundrobin", "lo", "c2")
	chain := map[string]*Balancer{"b1": b1, "b2": b2}
	cand := buildBalancer(t, "cand", "roundrobin", "direct", "lo")
	noFallback := buildBalancer(t, "nofb", "roundrobin", "", "c1")
	override := buildBalancer(t, "ov", "roundrobin", "direct", "c1")
	in := []string{"in"}
	cases := []struct {
		name      string
		rules     []*Rule
		balancers map[string]*Balancer
		override  map[string]string
		handlers  []outbound.Handler
		want      string
	}{
		{
			"a rule to its own inbound's loopback",
			[]*Rule{to("r", "lo", in, false)},
			nil, nil,
			[]outbound.Handler{out("direct"), lo("lo", "in")},
			"in -> [rule r] lo -> in",
		},
		{
			"two loopbacks",
			[]*Rule{to("a", "lo1", in, false), to("b", "lo2", []string{"via1"}, false)},
			nil, nil,
			[]outbound.Handler{out("direct"), lo("lo1", "via1"), lo("lo2", "in")},
			"in -> [rule a] lo1 -> via1 -> [rule b] lo2 -> in",
		},
		{
			"a balancer's candidate",
			[]*Rule{toBalancer("r", cand, in, false)},
			map[string]*Balancer{"cand": cand},
			nil,
			[]outbound.Handler{out("direct"), lo("lo", "in")},
			"in -> [rule r, balancer cand] lo -> in",
		},
		{
			"only the second fallback level",
			[]*Rule{toBalancer("r", b1, in, false)},
			chain, nil,
			[]outbound.Handler{out("direct"), out("c1"), out("c2"), lo("lo", "in")},
			"in -> [rule r, balancer b1 -> b2 fallback] lo -> in",
		},
		{
			"only the default outbound",
			[]*Rule{to("r", "direct", []string{"other"}, false)},
			nil, nil,
			[]outbound.Handler{lo("lo", "in"), out("direct")},
			"in -> [default] lo -> in",
		},
		{
			"a conditional rule does not end the walk",
			[]*Rule{to("r", "direct", in, true)},
			nil, nil,
			[]outbound.Handler{lo("lo", "in"), out("direct")},
			"in -> [default] lo -> in",
		},
		{
			"a rule on every inbound",
			[]*Rule{to("r", "lo", nil, true)},
			nil, nil,
			[]outbound.Handler{out("direct"), lo("lo", "in")},
			"in -> [rule r] lo -> in",
		},
		{
			"a dialerProxy chain to a loopback",
			[]*Rule{to("r", "x", in, false)},
			nil, nil,
			[]outbound.Handler{out("direct"), via("x", "y"), via("y", "lo"), lo("lo", "in")},
			"in -> [rule r] x via y via lo -> in",
		},
		{
			"a balancer without a fallbackTag",
			[]*Rule{toBalancer("r", noFallback, in, false)},
			map[string]*Balancer{"nofb": noFallback},
			nil,
			[]outbound.Handler{lo("lo", "in"), out("c1")},
			"in -> [rule r, balancer nofb default] lo -> in",
		},
		{
			"an override target",
			[]*Rule{toBalancer("r", override, in, false)},
			map[string]*Balancer{"ov": override},
			map[string]string{"ov": "lo"},
			[]outbound.Handler{out("direct"), out("c1"), lo("lo", "in")},
			"in -> [rule r, balancer ov override] lo -> in",
		},
		{
			"an unnamed rule",
			[]*Rule{{Tag: "lo", Config: ruleConfig(in, false)}},
			nil, nil,
			[]outbound.Handler{out("direct"), lo("lo", "in")},
			"in -> [rule #1] lo -> in",
		},
	}
	for _, c := range cases {
		err := checkLoops(c.rules, c.balancers, c.override, c.handlers...)
		if err == nil || !strings.HasSuffix(err.Error(), ": loopback loop: "+c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

func TestLoopbackLoopsAccepted(t *testing.T) {
	in := []string{"in"}
	cases := []struct {
		name     string
		rules    []*Rule
		handlers []outbound.Handler
	}{
		{
			"an earlier rule on the inbound alone leads elsewhere",
			[]*Rule{to("first", "direct", in, false), to("r", "lo", in, false)},
			[]outbound.Handler{out("direct"), lo("lo", "in")},
		},
		{
			"a loopback whose inbound no rule leads back",
			[]*Rule{to("r", "lo", []string{"entry"}, false), to("v", "direct", []string{"via"}, false)},
			[]outbound.Handler{out("direct"), lo("lo", "via")},
		},
		// The local source's API: a loopback without an inbound tag, which no rule names.
		{
			"the local source's api",
			[]*Rule{to("sys:api", "api", []string{"api-in"}, false), to("cn", "direct", nil, true)},
			[]outbound.Handler{out("direct"), lo("api", "")},
		},
		{"no loopback", []*Rule{to("r", "x", nil, true)}, []outbound.Handler{via("x", "x")}},
	}
	for _, c := range cases {
		if err := checkLoops(c.rules, nil, nil, c.handlers...); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	// A routing script replaces the rules and is not decidable (L-F).
	r := &Router{script: &scriptEngine{}}
	if err := r.checkLoopbacks([]*Rule{to("r", "lo", in, false)}, nil, nil,
		[]outbound.Handler{lo("lo", "in")}, nil); err != nil {
		t.Errorf("a routing script: %v", err)
	}
}

// Every RoutingRule field is a condition unless notConditions lists it, so a field an upstream
// update adds can only make the check stricter; notConditions names fields that exist.
func TestRoutingRuleConditions(t *testing.T) {
	fields := (&RoutingRule{}).ProtoReflect().Descriptor().Fields()
	for name := range notConditions {
		if fields.ByName(name) == nil {
			t.Errorf("notConditions names %s, which RoutingRule does not have", name)
		}
	}
	if !matchesOnInboundOnly(&RoutingRule{InboundTag: []string{"in"}, RuleTag: "r", TargetTag: &RoutingRule_Tag{Tag: "x"}}) {
		t.Fatal("a rule on its inbound alone")
	}
	if matchesOnInboundOnly(&RoutingRule{RuleTag: "r"}) {
		t.Fatal("a rule without an inbound tag")
	}
	for i := range fields.Len() {
		fd := fields.Get(i)
		if notConditions[fd.Name()] || fd.Name() == "inbound_tag" {
			continue
		}
		rule := &RoutingRule{InboundTag: []string{"in"}}
		m := rule.ProtoReflect()
		switch {
		case fd.IsList():
			list := m.Mutable(fd).List()
			list.Append(list.NewElement())
		case fd.IsMap():
			m.Mutable(fd).Map().Set(protoreflect.ValueOfString("k").MapKey(), protoreflect.ValueOfString("v"))
		case fd.Message() != nil:
			m.Mutable(fd)
		default:
			t.Fatalf("field %s: no way to set it here", fd.Name())
		}
		if matchesOnInboundOnly(rule) {
			t.Errorf("a rule with %s matches on its inbound alone", fd.Name())
		}
	}
}
