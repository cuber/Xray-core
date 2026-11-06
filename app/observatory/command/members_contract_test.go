package command_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/xtls/xray-core/app/observatory/command"
	handlercmd "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/app/router"
	routercmd "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/freedom"
)

// Core spec019 FR-006 (CP-7), FR-003: the shape of xray-config spec021 through a real gRPC server.
// route (weightedLeastPing) has the outbound x-main and the node balancers a and b (leastPing over a
// node's two protocols); every outbound reaches a local 204 endpoint.
func newMembersFixture(t *testing.T) (*trackFixture, handlercmd.HandlerServiceClient) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	apiPort := unusedPort(t)
	outbounds := []any{
		map[string]any{"tag": "direct", "protocol": "freedom"},
		map[string]any{"tag": "block", "protocol": "blackhole"},
	}
	for _, tag := range []string{"a-hy2", "a-anytls", "b-hy2", "b-anytls", "x-main"} {
		outbounds = append(outbounds, map[string]any{"tag": tag, "protocol": "freedom"})
	}
	node := func(tag string) map[string]any {
		return map[string]any{
			"tag": tag, "selector": []string{tag + "-hy2", tag + "-anytls"},
			"strategy": map[string]any{"type": "leastping"}, "fallbackTag": tag + "-hy2",
		}
	}
	raw, err := json.Marshal(map[string]any{
		"api": map[string]any{
			"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort),
			"services": []string{"ObservatoryService", "RoutingService", "HandlerService"},
		},
		"outbounds": outbounds,
		"routing": map[string]any{
			"balancers": []any{
				map[string]any{
					"tag": "route", "selector": []string{"x"}, "balancers": []string{"a", "b"},
					"strategy": map[string]any{"type": "weightedLeastPing", "settings": map[string]any{
						// A weight matches by substring, the first that matches: x-main, which contains "a",
						// comes first (spec020's shares showed it taking a's weight).
						"minSamples": 1, "weights": []any{
							map[string]any{"match": "x-main", "value": 1},
							map[string]any{"match": "a", "value": 2},
							map[string]any{"match": "b", "value": 1},
						},
					}},
					"fallbackTag": "block",
				},
				node("a"), node("b"),
			},
			"rules": []any{map[string]any{"type": "field", "domain": []string{"full:route.test"}, "balancerTag": "route"}},
		},
		"burstObservatory": map[string]any{"pingGroups": []any{map[string]any{
			"subjectSelector": []string{"a-", "b-", "x-main"},
			"pingConfig":      map[string]any{"destination": server.URL + "/generate_204", "interval": "1s", "sampling": 1, "timeout": "1s"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	x, err := core.StartInstance("json", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", apiPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	f := &trackFixture{observatory: command.NewObservatoryServiceClient(conn), routing: routercmd.NewRoutingServiceClient(conn)}
	return f, handlercmd.NewHandlerServiceClient(conn)
}

func members(msg *routercmd.BalancerMsg) string {
	var out []string
	for _, m := range msg.Members {
		kind := "outbound"
		if m.Balancer {
			kind = "balancer"
		}
		out = append(out, m.Tag+":"+kind)
	}
	return strings.Join(out, " ")
}

// picks renders each candidate's status as the level reads it and its share (Core spec020 P1, P2).
func picks(msg *routercmd.BalancerMsg) string {
	var out []string
	for _, m := range msg.Members {
		status := "absent"
		switch s := m.Status; {
		case s == nil:
		case !s.Known:
			status = "unknown"
		case s.Alive:
			status = "alive"
		default:
			status = "down"
		}
		out = append(out, fmt.Sprintf("%s:%s:%.2f", m.Tag, status, m.Share))
	}
	return strings.Join(out, " ")
}

func memberStatus(msg *routercmd.BalancerMsg) string {
	s := msg.Status
	switch {
	case s == nil || !s.Known:
		return "unknown"
	case s.Alive:
		return "alive"
	}
	return "down"
}

func TestBalancerMembersContract(t *testing.T) {
	f, handlers := newMembersFixture(t)
	eventually(t, "every outbound measured", 15*time.Second, func() bool {
		return memberStatus(f.balancer(t, "route")) == "alive" && memberStatus(f.balancer(t, "a")) == "alive" &&
			memberStatus(f.balancer(t, "b")) == "alive"
	})

	// M10: one flat item per balancer, each with its own candidates in order.
	list := f.listBalancers(t)
	var tags []string
	for _, item := range list.Balancers {
		tags = append(tags, item.Tag)
	}
	if fmt.Sprint(tags) != "[a b route]" {
		t.Fatalf("ListBalancers = %v", tags)
	}
	if got := members(f.balancer(t, "route")); got != "x-main:outbound a:balancer b:balancer" {
		t.Fatalf("route members = %s", got)
	}
	// The selector's outbounds come in the outbound manager's order, by tag.
	if got := members(f.balancer(t, "a")); got != "a-anytls:outbound a-hy2:outbound" {
		t.Fatalf("a members = %s", got)
	}
	// spec020 P2, P3: weights 2, 1, 1 over a healthy pool.
	route := f.balancer(t, "route")
	if got := picks(route); got != "x-main:alive:0.25 a:alive:0.50 b:alive:0.25" || route.Strategy != "weightedleastping" {
		t.Fatalf("route picks = %s (%s)", got, route.Strategy)
	}
	if weights := fmt.Sprint(route.Members[0].Weight, route.Members[1].Weight, route.Members[2].Weight); weights != "1 2 1" {
		t.Fatalf("route weights = %s", weights)
	}

	seen := func(n int) []string {
		var got []string
		for range n {
			if tag := f.route("route.test"); !slices.Contains(got, tag) {
				got = append(got, tag)
			}
		}
		slices.Sort(got)
		return got
	}
	for _, step := range []struct {
		down               []string
		a, route, without  string
		aPicks, routePicks string
	}{
		// a's faster protocol is forced down: a stays up on its other one.
		{
			[]string{"a-hy2"},
			"alive", "alive", "a-hy2",
			"a-anytls:alive:1.00 a-hy2:down:0.00", "x-main:alive:0.25 a:alive:0.50 b:alive:0.25",
		},
		// both of a's: a bubbles up as down and route stops picking it.
		{
			[]string{"a-hy2", "a-anytls"},
			"down", "alive", "a-",
			"a-anytls:down:0.00 a-hy2:down:0.00", "x-main:alive:0.50 a:down:0.00 b:alive:0.50",
		},
		// everything: route is down, its picks use its blackhole fallback.
		{
			[]string{"a-hy2", "a-anytls", "b-hy2", "b-anytls", "x-main"},
			"down", "down", "-",
			"a-anytls:down:0.00 a-hy2:down:0.00", "x-main:down:0.00 a:down:0.00 b:down:0.00",
		},
	} {
		if _, err := f.setForceDown(step.down...); err != nil {
			t.Fatal(err)
		}
		if got := memberStatus(f.balancer(t, "a")); got != step.a {
			t.Fatalf("forced %v: a is %s, want %s", step.down, got, step.a)
		}
		if got := memberStatus(f.balancer(t, "route")); got != step.route {
			t.Fatalf("forced %v: route is %s, want %s", step.down, got, step.route)
		}
		if got := picks(f.balancer(t, "a")); got != step.aPicks {
			t.Fatalf("forced %v: a picks %s, want %s", step.down, got, step.aPicks)
		}
		if got := picks(f.balancer(t, "route")); got != step.routePicks {
			t.Fatalf("forced %v: route picks %s, want %s", step.down, got, step.routePicks)
		}
		for _, tag := range seen(40) {
			if strings.HasPrefix(tag, step.without) || step.route == "down" && tag != "block" {
				t.Fatalf("forced %v: route picked %s", step.down, tag)
			}
		}
	}
	if _, err := f.setForceDown(); err != nil {
		t.Fatal(err)
	}

	// M6 on change: an outbound under route that the observatory does not report is refused.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := handlers.AddOutbound(ctx, &handlercmd.AddOutboundRequest{Outbound: &core.OutboundHandlerConfig{
		Tag: "x-new", ProxySettings: serial.ToTypedMessage(&freedom.Config{}),
	}})
	if err == nil || !strings.Contains(err.Error(), "balancer route: outbound x-new is not observed") {
		t.Fatalf("AddOutbound x-new = %v", err)
	}
	_, err = f.routing.AddRule(ctx, &routercmd.AddRuleRequest{ShouldAppend: true, Config: serial.ToTypedMessage(&router.Config{
		BalancingRule: []*router.BalancingRule{
			{Tag: "c", OutboundSelector: []string{"direct"}, Strategy: "leastping", FallbackTag: "block"},
			{Tag: "route2", Balancers: []string{"c"}, Strategy: "roundrobin", FallbackTag: "block"},
		},
	})})
	if err == nil || !strings.Contains(err.Error(), "balancer c: outbound direct is not observed") {
		t.Fatalf("AddRule route2 = %v", err)
	}
	if got := len(f.listBalancers(t).Balancers); got != 3 {
		t.Fatalf("balancers after the refusals = %d", got)
	}
}
