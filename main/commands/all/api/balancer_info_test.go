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
	defer func() { os.Stdout = stdout }()
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
		"    1   x          outbound  unknown   1       0.0%\n",
		"    2   a          balancer  50ms      2       66.7%\n",
		"    3   b          balancer  30ms      1       33.3%\n",
		"    1   a-hy2      outbound  50ms      -       100.0%\n",
		"    2   a-anytls   outbound  down      -       0.0%\n",
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

	two := runBalancerInfo(t, server, "route", "a")
	if !strings.HasPrefix(two, "route  weightedleastping") || !strings.Contains(two, "\na  leastping") || strings.Contains(two, "\nold") {
		t.Fatalf("two tags, in the order given:\n%s", two)
	}
}
