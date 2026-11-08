package api

import (
	"context"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	routerService "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/main/commands/base"
)

// fakeRouting answers GetBalancerInfo and ListBalancers from a fixed set, in tag order for the list.
type fakeRouting struct {
	routerService.UnimplementedRoutingServiceServer
	tags      []string
	balancers map[string]*routerService.BalancerMsg
}

func (f *fakeRouting) GetBalancerInfo(_ context.Context, r *routerService.GetBalancerInfoRequest) (*routerService.GetBalancerInfoResponse, error) {
	b, ok := f.balancers[r.Tag]
	if !ok {
		return nil, status.Error(codes.Unknown, "app/router: cannot find tag")
	}
	return &routerService.GetBalancerInfoResponse{Balancer: b}, nil
}

func (f *fakeRouting) ListBalancers(context.Context, *routerService.ListBalancersRequest) (*routerService.ListBalancersResponse, error) {
	response := &routerService.ListBalancersResponse{}
	for _, tag := range f.tags {
		response.Balancers = append(response.Balancers, &routerService.BalancerItem{Tag: tag, Balancer: f.balancers[tag]})
	}
	return response, nil
}

func alive(delay int64) *routerService.BalancerStatus {
	return &routerService.BalancerStatus{Known: true, Alive: true, Delay: delay}
}

// runBalancerInfo runs `xray api bi` against the fake server and returns what it printed.
func runBalancerInfo(t *testing.T, server *fakeRouting, args ...string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	routerService.RegisterRoutingServiceServer(s, server)
	go s.Serve(listener)
	defer s.Stop()

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = write
	// -json sets the package's flag for the tests that follow; leave it as they expect.
	defer func() { os.Stdout = stdout; apiJSON = false }()
	executeBalancerInfo(&base.Command{CustomFlags: true}, append([]string{"--server=" + listener.Addr().String()}, args...))
	write.Close()
	out, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// Core spec020 FR-005 (P5).
func TestBalancerInfoListsCandidatesAndShares(t *testing.T) {
	server := &fakeRouting{tags: []string{"a", "old", "route"}, balancers: map[string]*routerService.BalancerMsg{
		"route": {
			Strategy: "weightedleastping", Status: alive(30),
			Members: []*routerService.BalancerMember{
				{Tag: "x", Status: &routerService.BalancerStatus{}, Weight: 1},
				{Tag: "a", Balancer: true, Status: alive(50), Weight: 2, Share: 2.0 / 3},
				{Tag: "b", Balancer: true, Status: alive(30), Weight: 1, Share: 1.0 / 3},
				// Live but outside the pool, with a float32 weight as spec020's first build widened it.
				{Tag: "z", Status: alive(260), Weight: float64(float32(0.1)), Share: 0},
			},
			Fallback:        &routerService.FallbackInfo{Tag: "block"},
			PrincipleTarget: &routerService.PrincipleTargetInfo{Tag: []string{"a", "b"}},
		},
		"a": {
			Strategy: "leastping", Status: alive(50), Override: &routerService.OverrideInfo{},
			Members: []*routerService.BalancerMember{
				{Tag: "a-hy2", Status: alive(50), Share: 1},
				{Tag: "a-anytls", Status: &routerService.BalancerStatus{Known: true}},
			},
			Fallback:        &routerService.FallbackInfo{Tag: "a-hy2", Active: true},
			PrincipleTarget: &routerService.PrincipleTargetInfo{Tag: []string{"a-hy2"}},
		},
		// A Core before spec019: the pool only.
		"old": {Override: &routerService.OverrideInfo{}, PrincipleTarget: &routerService.PrincipleTargetInfo{Tag: []string{"p"}}},
	}}

	all := runBalancerInfo(t, server)
	if heads := []string{"a  leastping  50ms\n", "old\n", "route  weightedleastping  30ms\n"}; strings.Index(all, heads[0]) > strings.Index(all, heads[1]) ||
		strings.Index(all, heads[1]) > strings.Index(all, heads[2]) || strings.Index(all, heads[0]) < 0 {
		t.Fatalf("without a tag, every balancer in order:\n%s", all)
	}
	for _, want := range []string{
		// A Core before spec020 P6 reports no source (P9).
		"        candidate  kind      status    source  weight  share\n",
		"    1   x          outbound  unknown   -       1       0.0%\n",
		"    2   a          balancer  50ms      -       2       66.7%\n",
		"    3   b          balancer  30ms      -       1       33.3%\n",
		"    4   z          outbound  260ms     -       0.1     not picked\n",
		"    1   a-hy2      outbound  50ms      -       -       100.0%\n",
		"    2   a-anytls   outbound  down      -       -       0.0%\n",
		"  - Fallback: block\n",
		"  - Fallback: a-hy2 (active: new picks use it)\n",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if old := all[strings.Index(all, "old\n"):strings.Index(all, "route  ")]; strings.Contains(old, "Candidates") || !strings.Contains(old, "    1   p") {
		t.Errorf("an older Core's balancer:\n%s", old)
	}
	// With shares, the strategy's selects are not repeated.
	if strings.Count(all, "Selects") != 1 {
		t.Errorf("selects listed where shares are:\n%s", all)
	}

	two := runBalancerInfo(t, server, "route", "a")
	if !strings.HasPrefix(two, "route  weightedleastping") || !strings.Contains(two, "\na  leastping") || strings.Contains(two, "\nold") {
		t.Fatalf("two tags, in the order given:\n%s", two)
	}
}

// Core spec020 FR-010 (P9): the source column, a dead candidate's reason at the end of its row, a level's own
// reason when it is down; a Core without P6 prints "-"; -json carries the fields as the response has them.
func TestBalancerInfoShowsWhereStatusesComeFrom(t *testing.T) {
	provenance := func(s *routerService.BalancerStatus) *routerService.BalancerStatus {
		s.Provenance = true
		return s
	}
	server := &fakeRouting{tags: []string{"down", "old", "relays"}, balancers: map[string]*routerService.BalancerMsg{
		"relays": {
			Strategy: "leastping", Status: provenance(alive(12)),
			Members: []*routerService.BalancerMember{
				{Tag: "relay-a", Status: provenance(&routerService.BalancerStatus{Known: true, Alive: true, Delay: 12, TrackedFrom: "owner"}), Share: 1},
				// A refused relay keeps its probe's delay (P7); the row shows none.
				{Tag: "relay-c", Status: provenance(&routerService.BalancerStatus{Known: true, Delay: 12, TrackedFrom: "owner", Reason: "hysteria relay denied"})},
				{Tag: "p", Status: provenance(alive(30))},
				{Tag: "u", Status: provenance(&routerService.BalancerStatus{})},
				{Tag: "node", Balancer: true, Status: provenance(&routerService.BalancerStatus{Known: true, Reason: "force down: cdt"})},
			},
			Fallback: &routerService.FallbackInfo{Tag: "owner"},
		},
		"down": {
			Strategy: "roundrobin", Status: provenance(&routerService.BalancerStatus{Known: true, Reason: "force down: cdt"}),
			Members: []*routerService.BalancerMember{
				{Tag: "f", Status: provenance(&routerService.BalancerStatus{Known: true, Delay: 40, Reason: "force down: cdt"})},
			},
			Fallback: &routerService.FallbackInfo{Tag: "block", Active: true},
		},
		// A Core before P6: no provenance, so no source and no reason.
		"old": {
			Strategy: "leastping", Status: &routerService.BalancerStatus{Known: true},
			Members: []*routerService.BalancerMember{
				{Tag: "x", Status: alive(10), Share: 1},
				{Tag: "y", Status: &routerService.BalancerStatus{Known: true}},
			},
		},
	}}
	all := runBalancerInfo(t, server)
	for _, want := range []string{
		"down  roundrobin  down  force down: cdt\n",
		"    1   f          outbound  down      probe   -       0.0%        force down: cdt\n",
		"old  leastping  down\n",
		"    1   x          outbound  10ms      -       -       100.0%\n",
		"    2   y          outbound  down      -       -       0.0%\n",
		"relays  leastping  12ms\n",
		"        candidate  kind      status    source       weight  share\n",
		"    1   relay-a    outbound  12ms      track owner  -       100.0%\n",
		"    2   relay-c    outbound  down      track owner  -       0.0%        hysteria relay denied\n",
		"    3   p          outbound  30ms      probe        -       not picked\n",
		"    4   u          outbound  unknown   -            -       0.0%\n",
		"    5   node       balancer  down      balancer     -       0.0%        force down: cdt\n",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}

	printed := runBalancerInfo(t, server, "-json", "relays")
	for _, want := range []string{`"trackedFrom": "owner"`, `"reason": "hysteria relay denied"`, `"provenance": true`, `"delay": 12`} {
		if !strings.Contains(printed, want) {
			t.Errorf("-json is missing %s:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "track owner") || strings.Contains(printed, "Candidates") {
		t.Errorf("-json prints the response only:\n%s", printed)
	}
}
