package api

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	handlerService "github.com/xtls/xray-core/app/proxyman/command"
)

// fakeHandlers answers GetQuicStats from a fixed set; an empty one stands for a Core before spec021.
type fakeHandlers struct {
	handlerService.UnimplementedHandlerServiceServer
	handlers []*handlerService.QuicHandlerStats
	requests []*handlerService.GetQuicStatsRequest
}

type olderCore struct {
	handlerService.UnimplementedHandlerServiceServer
}

func (f *fakeHandlers) GetQuicStats(_ context.Context, r *handlerService.GetQuicStatsRequest) (*handlerService.GetQuicStatsResponse, error) {
	f.requests = append(f.requests, r)
	response := &handlerService.GetQuicStatsResponse{}
	for _, h := range f.handlers {
		if r.Tag == "" || h.Tag == r.Tag {
			if !r.Connections {
				h = &handlerService.QuicHandlerStats{
					Tag: h.Tag, Direction: h.Direction, Transport: h.Transport, Effective: h.Effective,
					Totals: h.Totals, ConnectionsActive: h.ConnectionsActive, ConnectionsTotal: h.ConnectionsTotal,
				}
			}
			response.Handlers = append(response.Handlers, h)
		}
	}
	if r.Tag != "" && len(response.Handlers) == 0 {
		return nil, status.Error(codes.NotFound, "no QUIC handler "+r.Tag)
	}
	return response, nil
}

func quicStatsClient(t *testing.T, server handlerService.HandlerServiceServer) handlerService.HandlerServiceClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	handlerService.RegisterHandlerServiceServer(s, server)
	go s.Serve(listener)
	t.Cleanup(s.Stop)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return handlerService.NewHandlerServiceClient(conn)
}

func quicStatsFixture() *fakeHandlers {
	return &fakeHandlers{handlers: []*handlerService.QuicHandlerStats{
		{
			Tag: "hy2-in", Direction: "inbound", Transport: "hysteria",
			Effective: &handlerService.QuicEffective{
				MaxAckDelayMs: 2, AckElicitingThreshold: 2, InitialRttMs: 15,
				PtoBackoffLimit: 1, LossTimeThreshold: 1.125, LossPacketThreshold: 3, DuplicateMaxSize: 300, DuplicateTail: true, DuplicateDelayMs: 3,
			},
			Totals:            &handlerService.QuicCounters{PacketsSent: 1000, BytesSent: 900000, PacketsLost: 7, BytesLost: 6300, PtoCount: 2, CopiesSent: 50, CopyBytesSent: 4000, DuplicatesReceived: 40},
			ConnectionsActive: 1, ConnectionsTotal: 3,
			Connections: []*handlerService.QuicConnection{{
				Remote: "192.0.2.1:4433", User: "a@example", AgeMs: 61000,
				SmoothedRttUs: 7500, MinRttUs: 7100, LatestRttUs: 7900, RttVarianceUs: 400, Counters: &handlerService.QuicCounters{PacketsLost: 3},
			}},
			Truncated: true,
		},
		{
			Tag: "hy2-out", Direction: "outbound", Transport: "hysteria",
			Effective: &handlerService.QuicEffective{MaxAckDelayMs: 25, AckElicitingThreshold: 2, InitialRttMs: 100, LossTimeThreshold: 1.125, LossPacketThreshold: 3},
			Totals:    &handlerService.QuicCounters{},
		},
	}}
}

// spec021 FR-008.
func TestQuicStatsText(t *testing.T) {
	server := quicStatsFixture()
	client := quicStatsClient(t, server)
	var out bytes.Buffer
	if err := runQuicStats(context.Background(), &out, client, false, nil); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"hy2-in  inbound  hysteria  connections 1 active, 3 total\n",
		"    effective  maxAckDelay 2ms  ackEliciting 2  initialRtt 15ms  ptoBackoff 1  lossTime 1.125  lossPacket 3  copies <=300B+tail after 3ms\n",
		"    sent 1000 pkts 900000B  received 0 pkts 0B  lost 7 pkts 6300B  spurious 0  pto 2\n",
		"    copies 50 (4000B)  skipped 0  duplicates received 40\n",
		"hy2-out  outbound  hysteria  connections 0 active, 0 total\n",
		"ptoBackoff unlimited",
		"copies off",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "192.0.2.1") {
		t.Errorf("rows only with -connections:\n%s", text)
	}

	out.Reset()
	if err := runQuicStats(context.Background(), &out, client, true, []string{"hy2-in"}); err != nil {
		t.Fatal(err)
	}
	text = out.String()
	for _, want := range []string{
		"    192.0.2.1:4433  a@example  age 1m1s  srtt 7.5ms  min 7.1ms  latest 7.9ms  var 400µs\n",
		"        sent 0 pkts 0B  received 0 pkts 0B  lost 3 pkts 0B  spurious 0  pto 0\n",
		"    (more connections not listed)\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "hy2-out") {
		t.Errorf("a tag selects its handler only:\n%s", text)
	}
	if last := server.requests[len(server.requests)-1]; last.Tag != "hy2-in" || !last.Connections {
		t.Errorf("request %+v", last)
	}

	if err := runQuicStats(context.Background(), &out, client, false, []string{"missing"}); err == nil || !strings.Contains(err.Error(), "no QUIC handler missing") {
		t.Errorf("an unknown tag: %v", err)
	}
}

// Core spec024 FR-009: a session owner, its connection's pool state and bindings, and a relay outbound.
func TestQuicStatsRelaySessionText(t *testing.T) {
	var out bytes.Buffer
	writeQuicHandlerStats(&out, &handlerService.QuicHandlerStats{
		Tag: "owner", Direction: "outbound", Transport: "hysteria", ConnectionsActive: 2, ConnectionsTotal: 3,
		Session: &handlerService.QuicSession{Min: 1, Max: 4, Active: 1, Draining: 1, StreamsOpen: 130},
		Connections: []*handlerService.QuicConnection{{
			Remote: "192.0.2.1:443", State: "active", StreamsOpen: 128,
			Relay: []*handlerService.QuicRelayBinding{{Index: 1, User: "a@example", Status: "ok"}, {Index: 2, Status: "denied"}},
		}},
	})
	writeQuicHandlerStats(&out, &handlerService.QuicHandlerStats{
		Tag: "relay-a", Direction: "outbound", Transport: "hysteria",
		Relay: &handlerService.QuicRelay{Session: "owner", User: "a@example", Status: "bound"},
	})
	writeQuicHandlerStats(&out, &handlerService.QuicHandlerStats{
		Tag: "relay-x", Direction: "outbound", Transport: "hysteria", Relay: &handlerService.QuicRelay{Session: "owner", Status: "unsupported"},
	})
	text := out.String()
	for _, want := range []string{
		"owner  outbound  hysteria  connections 2 active, 3 total\n",
		"    session  1–4 connections: 1 active, 1 draining, 0 spare, 0 suspect; 130 streams open\n",
		"  active, 128 streams\n",
		"        relay 1=ok a@example, 2=denied\n",
		"relay-a  outbound  hysteria  relay through owner as a@example: bound\n",
		"relay-x  outbound  hysteria  relay through owner as -: unsupported\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestQuicStatsJSON(t *testing.T) {
	client := quicStatsClient(t, quicStatsFixture())
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = write
	apiJSON = true
	defer func() { os.Stdout = stdout; apiJSON = false }()
	var out bytes.Buffer
	err = runQuicStats(context.Background(), &out, client, false, []string{"hy2-out"})
	write.Close()
	if err != nil {
		t.Fatal(err)
	}
	printed, _ := io.ReadAll(read)
	if out.Len() != 0 || !strings.Contains(string(printed), `"tag": "hy2-out"`) || !strings.Contains(string(printed), `"maxAckDelayMs": 25`) {
		t.Fatalf("JSON output:\n%s", printed)
	}
}

func TestQuicStatsOlderCore(t *testing.T) {
	client := quicStatsClient(t, &olderCore{})
	var out bytes.Buffer
	err := runQuicStats(context.Background(), &out, client, false, nil)
	if err == nil || !strings.Contains(err.Error(), "unavailable") || out.Len() != 0 {
		t.Fatalf("an older Core is unavailable, never zero loss: %v, %q", err, out.String())
	}
}
