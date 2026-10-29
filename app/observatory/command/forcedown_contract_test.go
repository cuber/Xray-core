package command_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory/command"
	routercmd "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// newForceFixture starts a Core shaped like asz.lite's AHK paths (spec015): two probes,
// each with a tracked route copy; roundrobin balancers over the pair that fall back to
// `block`, and a leastPing one over the probes. The default (first) outbound is a probe,
// as on asz.lite. With classic, it runs the classic observatory instead.
func newForceFixture(t *testing.T, classic bool) *trackFixture {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	apiPort := unusedPort(t)
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
			map[string]any{"tag": "block", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"balancers": []any{
				map[string]any{
					"tag": "ahk-balancer", "selector": []string{"ahk.ecs-hy2", "ahk.alpine-hy2"},
					"strategy": map[string]any{"type": "roundrobin"}, "fallbackTag": "block",
				},
				map[string]any{
					"tag": "tk-balancer", "selector": []string{"tk.auto-hy2", "tk.auto-alpine-hy2"},
					"strategy": map[string]any{"type": "roundrobin"}, "fallbackTag": "block",
				},
				map[string]any{
					"tag": "lp-balancer", "selector": []string{"ahk.ecs-hy2", "ahk.alpine-hy2"},
					"strategy": map[string]any{"type": "leastPing"},
				},
			},
			"rules": []any{
				map[string]any{"type": "field", "domain": []string{"full:ahk.test"}, "balancerTag": "ahk-balancer"},
				map[string]any{"type": "field", "domain": []string{"full:tk.test"}, "balancerTag": "tk-balancer"},
				map[string]any{"type": "field", "domain": []string{"full:lp.test"}, "balancerTag": "lp-balancer"},
			},
		},
	}
	if classic {
		config["observatory"] = map[string]any{"subjectSelector": []string{"ahk."}, "probeUrl": server.URL, "probeInterval": "1s"}
	} else {
		config["burstObservatory"] = map[string]any{
			"pingGroups": []any{map[string]any{
				"subjectSelector": []string{"ahk."},
				"pingConfig":      map[string]any{"destination": server.URL + "/generate_204", "interval": "1s", "sampling": 1, "timeout": "1s"},
			}},
			"track": []any{
				map[string]any{"probe": "ahk.ecs-hy2", "outbounds": []string{"tk.auto-hy2"}},
				map[string]any{"probe": "ahk.alpine-hy2", "outbounds": []string{"tk.auto-alpine-hy2"}},
			},
		}
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
	return &trackFixture{observatory: command.NewObservatoryServiceClient(conn), routing: routercmd.NewRoutingServiceClient(conn)}
}

func (f *trackFixture) setForceDown(tags ...string) (*command.SetForceDownResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := &command.SetForceDownRequest{}
	for _, tag := range tags {
		request.Entries = append(request.Entries, &command.ForceDownEntry{OutboundTag: tag, Reason: "cdt quota 190/190 GiB"})
	}
	return f.observatory.SetForceDown(ctx, request)
}

func (f *trackFixture) forceDown(t *testing.T) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := f.observatory.GetForceDown(ctx, &command.GetForceDownRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, entry := range response.Entries {
		tags = append(tags, entry.OutboundTag)
	}
	return tags
}

// routes is the set of outbounds the router picks for a domain over n tries (roundrobin turns).
func (f *trackFixture) routes(domain string, n int) string {
	seen := map[string]bool{}
	for range n {
		seen[f.route(domain)] = true
	}
	var tags []string
	for _, tag := range []string{"ahk.ecs-hy2", "ahk.alpine-hy2", "tk.auto-hy2", "tk.auto-alpine-hy2", "block", ""} {
		if seen[tag] {
			tags = append(tags, tag)
		}
	}
	return fmt.Sprint(tags)
}

// CP-7: the force-down set through a real gRPC server on a started instance, its effect on
// roundrobin, tracked and leastPing selection, and the measured control-plane view.
func TestForceDownContract(t *testing.T) {
	f := newForceFixture(t, false)
	if got := f.forceDown(t); got != nil {
		t.Fatalf("force down at start = %v", got)
	}
	eventually(t, "both probes' first result", 10*time.Second, func() bool { return len(f.probed(t)) == 2 })
	if got := f.routes("ahk.test", 4); got != "[ahk.ecs-hy2 ahk.alpine-hy2]" {
		t.Fatalf("roundrobin over two alive probes = %s", got)
	}

	// An invalid entry refuses the request and leaves the set alone.
	for _, tags := range [][]string{{"nope"}, {"tk.auto-hy2"}, {"block"}, {"ahk.ecs-hy2", "ahk.ecs-hy2"}} {
		if _, err := f.setForceDown(tags...); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("SetForceDown %v = %v, want InvalidArgument", tags, err)
		}
	}
	if got := f.forceDown(t); got != nil {
		t.Fatalf("set after refusals = %v", got)
	}

	applied, err := f.setForceDown("ahk.ecs-hy2")
	if err != nil || len(applied.Entries) != 1 || applied.Entries[0].OutboundTag != "ahk.ecs-hy2" || applied.Entries[0].Since == 0 {
		t.Fatalf("SetForceDown = %v, %v", applied, err)
	}
	if got := f.forceDown(t); fmt.Sprint(got) != "[ahk.ecs-hy2]" {
		t.Fatalf("GetForceDown = %v", got)
	}
	for domain, want := range map[string]string{
		"ahk.test": "[ahk.alpine-hy2]",
		"tk.test":  "[tk.auto-alpine-hy2]", // the tracked copy inherits the force
		"lp.test":  "[ahk.alpine-hy2]",
	} {
		if got := f.routes(domain, 4); got != want {
			t.Fatalf("%s with ahk.ecs-hy2 forced = %s, want %s", domain, got, want)
		}
	}

	// The control-plane view keeps the measurement and carries the force fields.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outbounds, err := f.observatory.GetOutboundStatus(ctx, &command.GetOutboundStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range outbounds.Status.Status {
		forced := s.OutboundTag == "ahk.ecs-hy2"
		if !s.Alive || s.ForceDown != forced || s.TrackedFrom != "" ||
			(forced && (s.ForceDownReason != "cdt quota 190/190 GiB" || s.ForceDownSince != applied.Entries[0].Since)) {
			t.Fatalf("GetOutboundStatus %v", s)
		}
	}
	for _, group := range f.track(t).Groups {
		if probe := group.ProbeStatus; !probe.GetAlive() || probe.GetForceDown() != (group.Probe == "ahk.ecs-hy2") {
			t.Fatalf("GetTrack %v", group)
		}
	}

	// Both forced: every balancer falls back to block, not to the default outbound.
	if _, err := f.setForceDown("ahk.alpine-hy2", "ahk.ecs-hy2"); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"ahk.test", "tk.test"} {
		if got := f.routes(domain, 4); got != "[block]" {
			t.Fatalf("%s with both forced = %s", domain, got)
		}
	}

	// Released: the measured status at once.
	if _, err := f.setForceDown(); err != nil {
		t.Fatal(err)
	}
	if got := f.forceDown(t); got != nil {
		t.Fatalf("set after release = %v", got)
	}
	if got := f.routes("tk.test", 4); got != "[tk.auto-hy2 tk.auto-alpine-hy2]" {
		t.Fatalf("tk.test after release = %s", got)
	}

	// A new instance (a Core restart) starts with an empty set.
	if got := newForceFixture(t, false).forceDown(t); got != nil {
		t.Fatalf("force down after restart = %v", got)
	}
}

// The classic observatory has no force-down set: Get is empty and Set is refused.
func TestForceDownClassicObservatory(t *testing.T) {
	f := newForceFixture(t, true)
	if got := f.forceDown(t); got != nil {
		t.Fatalf("classic GetForceDown = %v", got)
	}
	if _, err := f.setForceDown("ahk.ecs-hy2"); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("classic SetForceDown = %v, want FailedPrecondition", err)
	}
}
