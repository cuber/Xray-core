package command_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory/command"
	routercmd "github.com/xtls/xray-core/app/router/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func unusedPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

type trackFixture struct {
	healthy     atomic.Bool
	observatory command.ObservatoryServiceClient
	routing     routercmd.RoutingServiceClient
}

// newTrackFixture starts a Core whose probes reach a local 204 endpoint that the test turns
// on and off. tk.auto-hy2 tracks the probe ahk.ecs-hy2 (spec014); tk-balancer selects it
// and falls back to tk.auto-alpine-hy2, and tk-leastping selects it alone.
func newTrackFixture(t *testing.T, track bool) *trackFixture {
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
	burst := map[string]any{"pingGroups": []any{map[string]any{
		"subjectSelector": []string{"ahk."},
		"pingConfig":      map[string]any{"destination": server.URL + "/generate_204", "interval": "1s", "sampling": 1, "timeout": "1s"},
	}}}
	if track {
		burst["track"] = []any{map[string]any{"probe": "ahk.ecs-hy2", "outbounds": []string{"tk.auto-hy2"}}}
	}
	config := map[string]any{
		"api": map[string]any{
			"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort),
			"services": []string{"ObservatoryService", "RoutingService"},
		},
		"outbounds": []any{
			map[string]any{"tag": "ahk.ecs-hy2", "protocol": "freedom"},
			map[string]any{"tag": "ahk.alpine-hy2", "protocol": "freedom"},
			map[string]any{"tag": "tk.auto-hy2", "protocol": "freedom"},
			map[string]any{"tag": "tk.auto-alpine-hy2", "protocol": "freedom"},
		},
		"burstObservatory": burst,
		"routing": map[string]any{
			"balancers": []any{
				map[string]any{
					"tag": "tk-balancer", "selector": []string{"tk.auto-hy2"},
					"strategy": map[string]any{"type": "roundrobin"}, "fallbackTag": "tk.auto-alpine-hy2",
				},
				map[string]any{
					"tag": "tk-leastping", "selector": []string{"tk.auto-hy2"},
					"strategy": map[string]any{"type": "leastPing"},
				},
			},
			"rules": []any{
				map[string]any{"type": "field", "domain": []string{"full:tk.test"}, "balancerTag": "tk-balancer"},
				map[string]any{"type": "field", "domain": []string{"full:lp.test"}, "balancerTag": "tk-leastping"},
			},
		},
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

func (f *trackFixture) track(t *testing.T) *command.GetTrackResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := f.observatory.GetTrack(ctx, &command.GetTrackRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func (f *trackFixture) probed(t *testing.T) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := f.observatory.GetOutboundStatus(ctx, &command.GetOutboundStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, status := range response.Status.Status {
		if status.TrackedFrom != "" {
			t.Fatalf("GetOutboundStatus reports the tracked status %v", status)
		}
		tags = append(tags, status.OutboundTag)
	}
	sort.Strings(tags)
	return tags
}

// route is the outbound the router picks for a domain, or "" when it picks none.
func (f *trackFixture) route(domain string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := f.routing.TestRoute(ctx, &routercmd.TestRouteRequest{RoutingContext: &routercmd.RoutingContext{
		Network: xnet.Network_TCP, TargetDomain: domain, TargetPort: 443,
	}})
	if err != nil {
		return ""
	}
	return response.OutboundTag
}

func eventually(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// CP-7: GetTrack and GetOutboundStatus on a started instance, and balancers that fail over
// and come back on the tracked probe alone.
func TestTrackContract(t *testing.T) {
	f := newTrackFixture(t, true)
	eventually(t, "the probe's first result", 10*time.Second, func() bool {
		groups := f.track(t).Groups
		return len(groups) == 1 && groups[0].ProbeStatus.GetAlive()
	})
	group := f.track(t).Groups[0]
	if group.Probe != "ahk.ecs-hy2" || len(group.Outbounds) != 1 || group.Outbounds[0] != "tk.auto-hy2" ||
		group.ProbeStatus.OutboundTag != "ahk.ecs-hy2" {
		t.Fatalf("track group = %v", group)
	}
	if got := f.probed(t); fmt.Sprint(got) != "[ahk.alpine-hy2 ahk.ecs-hy2]" {
		t.Fatalf("probed = %v", got)
	}
	if got := f.route("tk.test"); got != "tk.auto-hy2" {
		t.Fatalf("roundrobin with the probe alive picks %q", got)
	}
	// leastPing never picks an unobserved outbound: picking it shows the tracked status counts.
	if got := f.route("lp.test"); got != "tk.auto-hy2" {
		t.Fatalf("leastPing with the probe alive picks %q", got)
	}

	f.healthy.Store(false)
	eventually(t, "the fallback", 10*time.Second, func() bool { return f.route("tk.test") == "tk.auto-alpine-hy2" })
	if group := f.track(t).Groups[0]; group.ProbeStatus.GetAlive() {
		t.Fatalf("probe alive after its endpoint failed: %v", group)
	}
	f.healthy.Store(true)
	eventually(t, "the return to the primary", 15*time.Second, func() bool { return f.route("tk.test") == "tk.auto-hy2" })
}

// Without `track` GetTrack reports no group, and an unobserved roundrobin candidate never falls back.
func TestTrackContractWithoutTrack(t *testing.T) {
	f := newTrackFixture(t, false)
	if groups := f.track(t).Groups; len(groups) != 0 {
		t.Fatalf("groups without track = %v", groups)
	}
	eventually(t, "the probes' first result", 10*time.Second, func() bool { return len(f.probed(t)) == 2 })
	f.healthy.Store(false)
	time.Sleep(3 * time.Second)
	if got := f.route("tk.test"); got != "tk.auto-hy2" {
		t.Fatalf("roundrobin over an unobserved candidate picks %q", got)
	}
}
