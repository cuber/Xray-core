package api

import (
	"bytes"
	"context"
	"strings"
	"testing"

	handlerService "github.com/xtls/xray-core/app/proxyman/command"
)

// Core spec026 FR-009: xray api conns and connclose.

type fakeConnections struct {
	handlerService.UnimplementedHandlerServiceServer
	closeRequest *handlerService.CloseConnectionsRequest
}

func (f *fakeConnections) GetConnections(_ context.Context, r *handlerService.GetConnectionsRequest) (*handlerService.GetConnectionsResponse, error) {
	in := &handlerService.InboundConnections{
		Tag:              "ss-auto-in",
		Counts:           &handlerService.ConnectionCounts{Tcp: 5, SingMuxCarriers: 2, UdpSessions: 1},
		Unbound:          1,
		SingMuxStreams:   7,
		SingMuxProtocols: map[string]uint64{"h2mux": 1, "smux": 1},
	}
	if r.UserRows {
		in.Users = []*handlerService.ConnectionUser{{User: "a@test", Counts: &handlerService.ConnectionCounts{Tcp: 4, SingMuxCarriers: 2}}}
	}
	return &handlerService.GetConnectionsResponse{Inbounds: []*handlerService.InboundConnections{in}}, nil
}

func (f *fakeConnections) CloseConnections(_ context.Context, r *handlerService.CloseConnectionsRequest) (*handlerService.CloseConnectionsResponse, error) {
	f.closeRequest = r
	return &handlerService.CloseConnectionsResponse{Inbounds: []*handlerService.InboundClosed{
		{Tag: "hy2-in", Closed: &handlerService.ConnectionCounts{Hy2Connections: 1, Hy2Streams: 3}, Pending: 1},
	}}, nil
}

func TestConnsCommand(t *testing.T) {
	fake := &fakeConnections{}
	client := quicStatsClient(t, fake)
	var out bytes.Buffer
	if err := runConns(context.Background(), &out, client, &handlerService.GetConnectionsRequest{UserRows: true}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ss-auto-in  tcp 5 (mux.cool 0, sing-mux 2)  hy2 0 conns 0 streams 0 udp  udp sessions 1  unbound 1  closing 0",
		"streams  mux.cool 0  sing-mux 7  carriers h2mux 1, smux 1",
		"    a@test  tcp 4 (mux.cool 0, sing-mux 2)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}

	out.Reset()
	request := &handlerService.CloseConnectionsRequest{AllUsers: true, Tags: []string{"hy2-in"}, ExcludeUsers: []string{"admin"}, Reason: "move"}
	if err := runConnClose(context.Background(), &out, client, request); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "hy2-in  closed tcp 0 (mux.cool 0, sing-mux 0)  hy2 1 conns 3 streams 0 udp  udp sessions 0  pending 1") {
		t.Errorf("close output\n%s", out.String())
	}
	if r := fake.closeRequest; !r.AllUsers || r.Tags[0] != "hy2-in" || r.ExcludeUsers[0] != "admin" || r.Reason != "move" {
		t.Errorf("request %v", r)
	}

	// an older Core
	older := quicStatsClient(t, &olderCore{})
	if err := runConns(context.Background(), &out, older, &handlerService.GetConnectionsRequest{}); err == nil || !strings.Contains(err.Error(), "before spec026") {
		t.Errorf("older Core: %v", err)
	}
	if err := runConnClose(context.Background(), &out, older, request); err == nil || !strings.Contains(err.Error(), "before spec026") {
		t.Errorf("older Core: %v", err)
	}
}
