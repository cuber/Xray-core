package command_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory/command"
	routercmd "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

// newChainFixture starts a Core shaped like asz.lite's monitoring root after Core spec017: l1 (c1)
// falls back to l2 (c2), l2 to l3 (c3), l3 to block, as hk-balancer -> ahk-balancer -> ctc-balancer.
// The probes reach a local 204 endpoint.
func newChainFixture(t *testing.T) *trackFixture {
	f := &trackFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	apiPort := unusedPort(t)
	roundrobin := map[string]any{"type": "roundrobin"}
	config := map[string]any{
		"api": map[string]any{
			"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort),
			"services": []string{"ObservatoryService", "RoutingService"},
		},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "c1", "protocol": "freedom"},
			map[string]any{"tag": "c2", "protocol": "freedom"},
			map[string]any{"tag": "c3", "protocol": "freedom"},
			map[string]any{"tag": "block", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"balancers": []any{
				map[string]any{"tag": "l1", "selector": []string{"c1"}, "strategy": roundrobin, "fallbackTag": "l2"},
				map[string]any{"tag": "l2", "selector": []string{"c2"}, "strategy": roundrobin, "fallbackTag": "l3"},
				map[string]any{"tag": "l3", "selector": []string{"c3"}, "strategy": roundrobin, "fallbackTag": "block"},
				map[string]any{"tag": "plain", "selector": []string{"c1"}, "strategy": roundrobin},
			},
			"rules": []any{
				map[string]any{"type": "field", "domain": []string{"full:chain.test"}, "balancerTag": "l1"},
				map[string]any{"type": "field", "domain": []string{"full:plain.test"}, "balancerTag": "plain"},
			},
		},
		"burstObservatory": map[string]any{"pingGroups": []any{map[string]any{
			"subjectSelector": []string{"c1", "c2", "c3"},
			"pingConfig":      map[string]any{"destination": server.URL + "/generate_204", "interval": "1s", "sampling": 1, "timeout": "1s"},
		}}},
	}
	raw, err := json.Marshal(config)
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
	f.observatory = command.NewObservatoryServiceClient(conn)
	f.routing = routercmd.NewRoutingServiceClient(conn)
	return f
}

func (f *trackFixture) listBalancers(t *testing.T) *routercmd.ListBalancersResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := f.routing.ListBalancers(ctx, &routercmd.ListBalancersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func (f *trackFixture) loopGuards(t *testing.T) *routercmd.GetLoopGuardsResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := f.routing.GetLoopGuards(ctx, &routercmd.GetLoopGuardsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

// chainInfo is l1's fallback with l2 and l3 active as given.
func chainInfo(l1, l2, l3 bool) *routercmd.FallbackInfo {
	return &routercmd.FallbackInfo{Tag: "l2", Active: l1, Reported: true, Balancer: true, Chain: []*routercmd.FallbackHop{
		{Tag: "l2", Balancer: true, Active: l2, Reported: true},
		{Tag: "l3", Balancer: true, Active: l3, Reported: true},
		{Tag: "block"},
	}}
}

// CP-7 (Core spec017 FR-009): the chain through a real gRPC server on a started instance, healthy,
// with its first level forced down (spec015) and with the first two; ListBalancers equals
// GetBalancerInfo; GetLoopGuards is zero (TestLoopGuardsContract counts a loop).
func TestFallbackChainContract(t *testing.T) {
	f := newChainFixture(t)
	eventually(t, "c1 measured healthy", 15*time.Second, func() bool { return f.route("chain.test") == "c1" })
	assertFallback(t, f.balancer(t, "l1"), chainInfo(false, false, false), "c1")
	assertFallback(t, f.balancer(t, "plain"), &routercmd.FallbackInfo{Reported: true}, "c1")

	assertList := func() {
		t.Helper()
		list := f.listBalancers(t)
		var tags []string
		for _, item := range list.Balancers {
			tags = append(tags, item.Tag)
			if !proto.Equal(item.Balancer, f.balancer(t, item.Tag)) {
				t.Fatalf("ListBalancers %s = %v, GetBalancerInfo = %v", item.Tag, item.Balancer, f.balancer(t, item.Tag))
			}
		}
		if fmt.Sprint(tags) != "[l1 l2 l3 plain]" {
			t.Fatalf("ListBalancers tags = %v", tags)
		}
	}
	assertList()

	for _, step := range []struct {
		down       []string
		route      string
		l1, l2, l3 bool
	}{
		{[]string{"c1"}, "c2", true, false, false},
		{[]string{"c1", "c2"}, "c3", true, true, false},
		{[]string{"c1", "c2", "c3"}, "block", true, true, true},
		{nil, "c1", false, false, false},
	} {
		if _, err := f.setForceDown(step.down...); err != nil {
			t.Fatal(err)
		}
		if got := f.route("chain.test"); got != step.route {
			t.Fatalf("forced down %v: route = %q, want %q", step.down, got, step.route)
		}
		assertFallback(t, f.balancer(t, "l1"), chainInfo(step.l1, step.l2, step.l3), "c1")
		assertList()
	}

	if guards := f.loopGuards(t); guards.FallbackChain != 0 || guards.LoopbackReentry != 0 {
		t.Fatalf("guards = %v", guards)
	}
}

// CP-7: GetLoopGuards is zero, then one after a loopback loop. Start refuses a loop that rules make
// (Core spec018 S2), so a routing script makes this one: a dokodemo inbound leads into a loopback
// whose inbound the script routes back to it (spec018 L-F, which L7 ends).
func TestLoopGuardsContract(t *testing.T) {
	apiPort, loopPort := unusedPort(t), unusedPort(t)
	script := filepath.Join(t.TempDir(), "route.lua")
	if err := os.WriteFile(script, []byte(`
function HandleRoute(ctx, inboundTag)
    if inboundTag == "in-loop" or inboundTag == "via-lo" then
        return "lo", "loop"
    end
    return "direct", "out"
end`), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"api": map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort), "services": []string{"RoutingService"}},
		"inbounds": []any{map[string]any{
			"tag": "in-loop", "listen": "127.0.0.1", "port": loopPort, "protocol": "dokodemo-door",
			"settings": map[string]any{"address": "127.0.0.1", "port": 9, "network": "tcp"},
		}},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "lo", "protocol": "loopback", "settings": map[string]any{"inboundTag": "via-lo"}},
		},
		"routing": map[string]any{"script": script},
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
	f := &trackFixture{routing: routercmd.NewRoutingServiceClient(conn)}
	if guards := f.loopGuards(t); guards.FallbackChain != 0 || guards.LoopbackReentry != 0 {
		t.Fatalf("guards at start = %v", guards)
	}
	loop, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(loopPort)), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	loop.Write([]byte("ping"))
	defer loop.Close()
	eventually(t, "the loopback re-entry counted", 5*time.Second, func() bool {
		guards := f.loopGuards(t)
		return guards.FallbackChain == 0 && guards.LoopbackReentry == 1
	})
}

// CP-7: a routing configuration without balancers lists none and counts nothing.
func TestFallbackChainContractWithoutBalancers(t *testing.T) {
	apiPort := unusedPort(t)
	raw, err := json.Marshal(map[string]any{
		"api":       map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort), "services": []string{"RoutingService"}},
		"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom"}},
		"routing":   map[string]any{"rules": []any{}},
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
	f := &trackFixture{routing: routercmd.NewRoutingServiceClient(conn)}
	if list := f.listBalancers(t); len(list.Balancers) != 0 {
		t.Fatalf("ListBalancers = %v", list)
	}
	if guards := f.loopGuards(t); guards.FallbackChain != 0 || guards.LoopbackReentry != 0 {
		t.Fatalf("guards = %v", guards)
	}
}
