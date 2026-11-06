package command_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory/command"
	routercmd "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

// newFallbackFixture starts a Core shaped like hq's la.aaitr.att route (Core spec016): a
// weightedLeastPing balancer over two probed candidates with a fallbackTag, the same balancer
// without one, and a roundrobin one with the fallback. The probes reach a local 204 endpoint that
// the test fails and restores. The default (first) outbound is `direct`, as on hq.
func newFallbackFixture(t *testing.T) *trackFixture {
	f := &trackFixture{}
	f.healthy.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.healthy.Load() {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	apiPort := unusedPort(t)
	weighted := map[string]any{"type": "weightedLeastPing", "settings": map[string]any{
		"maxRTT": "250ms", "rttTolerance": "100ms", "tolerance": 0.2, "minSamples": 1,
		"weights": []any{map[string]any{"match": "c1", "value": 1}, map[string]any{"match": "c2", "value": 0.1}},
	}}
	config := map[string]any{
		"api": map[string]any{
			"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort),
			"services": []string{"ObservatoryService", "RoutingService"},
		},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "c1", "protocol": "freedom"},
			map[string]any{"tag": "c2", "protocol": "freedom"},
			map[string]any{"tag": "fb", "protocol": "freedom"},
		},
		"routing": map[string]any{
			"balancers": []any{
				map[string]any{"tag": "wlp-fb", "selector": []string{"c1", "c2"}, "strategy": weighted, "fallbackTag": "fb"},
				map[string]any{"tag": "wlp", "selector": []string{"c1", "c2"}, "strategy": weighted},
				map[string]any{"tag": "rr", "selector": []string{"c1", "c2"}, "strategy": map[string]any{"type": "roundrobin"}, "fallbackTag": "fb"},
			},
			"rules": []any{
				map[string]any{"type": "field", "domain": []string{"full:fb.test"}, "balancerTag": "wlp-fb"},
				map[string]any{"type": "field", "domain": []string{"full:wlp.test"}, "balancerTag": "wlp"},
				map[string]any{"type": "field", "domain": []string{"full:rr.test"}, "balancerTag": "rr"},
			},
		},
		"burstObservatory": map[string]any{"pingGroups": []any{map[string]any{
			"subjectSelector": []string{"c1", "c2"},
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

// picks is the set of outbounds the router picks for a domain over n tries.
func (f *trackFixture) picks(domain string, n int) string {
	seen := map[string]bool{}
	for range n {
		seen[f.route(domain)] = true
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return fmt.Sprint(tags)
}

func (f *trackFixture) balancer(t *testing.T, tag string) *routercmd.BalancerMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := f.routing.GetBalancerInfo(ctx, &routercmd.GetBalancerInfoRequest{Tag: tag})
	if err != nil {
		t.Fatal(err)
	}
	return response.Balancer
}

func assertFallback(t *testing.T, got *routercmd.BalancerMsg, want *routercmd.FallbackInfo, targets ...string) {
	t.Helper()
	if !proto.Equal(got.Fallback, want) {
		t.Fatalf("fallback = %v, want %v", got.Fallback, want)
	}
	if fmt.Sprint(got.GetPrincipleTarget().GetTag()) != fmt.Sprint(targets) {
		t.Fatalf("principle targets = %v, want %v", got.GetPrincipleTarget().GetTag(), targets)
	}
}

// toFB is the fallback to the outbound fb: since Core spec017 its chain is that one outbound hop.
func toFB(active bool) *routercmd.FallbackInfo {
	return &routercmd.FallbackInfo{Tag: "fb", Active: active, Reported: true, Chain: []*routercmd.FallbackHop{{Tag: "fb"}}}
}

// CP-7 (spec016 FR-004, FR-005): GetBalancerInfo's fallback through a real gRPC server on a started
// instance, in the default healthy state, with every probe failing and with every candidate forced down.
// Core spec017 adds the chain and reports roundrobin as well.
func TestWeightedLeastPingFallbackContract(t *testing.T) {
	f := newFallbackFixture(t)
	eventually(t, "both candidates measured healthy", 15*time.Second, func() bool {
		return f.picks("fb.test", 22) == "[c1 c2]"
	})
	assertFallback(t, f.balancer(t, "wlp-fb"), toFB(false), "c1", "c2")
	assertFallback(t, f.balancer(t, "wlp"), &routercmd.FallbackInfo{Reported: true}, "c1", "c2")
	assertFallback(t, f.balancer(t, "rr"), toFB(false), "c1", "c2")

	// W1: every candidate measured and failing.
	f.healthy.Store(false)
	eventually(t, "the fallback", 15*time.Second, func() bool { return f.picks("fb.test", 22) == "[fb]" })
	assertFallback(t, f.balancer(t, "wlp-fb"), toFB(true))
	// W2: without a fallbackTag the candidates stay; roundrobin falls back on its own, reported since spec017.
	if got := f.picks("wlp.test", 22); got != "[c1 c2]" {
		t.Fatalf("wlp without a fallbackTag picks %s, want [c1 c2]", got)
	}
	assertFallback(t, f.balancer(t, "wlp"), &routercmd.FallbackInfo{Reported: true}, "c1", "c2")
	if got := f.picks("rr.test", 4); got != "[fb]" {
		t.Fatalf("rr picks %s, want [fb]", got)
	}
	assertFallback(t, f.balancer(t, "rr"), toFB(true), "c1", "c2")

	f.healthy.Store(true)
	eventually(t, "the candidates again", 15*time.Second, func() bool { return f.picks("fb.test", 22) == "[c1 c2]" })
	assertFallback(t, f.balancer(t, "wlp-fb"), toFB(false), "c1", "c2")

	// W4: forcing every candidate down (spec015) reaches the fallback; releasing returns.
	if _, err := f.setForceDown("c1", "c2"); err != nil {
		t.Fatal(err)
	}
	if got := f.picks("fb.test", 22); got != "[fb]" {
		t.Fatalf("forced-down picks %s, want [fb]", got)
	}
	assertFallback(t, f.balancer(t, "wlp-fb"), toFB(true))
	if _, err := f.setForceDown(); err != nil {
		t.Fatal(err)
	}
	if got := f.picks("fb.test", 22); got != "[c1 c2]" {
		t.Fatalf("released picks %s, want [c1 c2]", got)
	}
}
