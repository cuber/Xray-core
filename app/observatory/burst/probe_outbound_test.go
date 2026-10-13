package burst

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport/internet/tagged"
)

func TestProbeOutboundKeepsSubjectHealthAndGroupIsolation(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusNoContent)
	requests := make(chan string, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.RequestURI
		w.WriteHeader(int(status.Load()))
	}))
	defer server.Close()
	dials := make(chan string, 16)
	old := tagged.Dialer
	t.Cleanup(func() { tagged.Dialer = old })
	tagged.Dialer = func(ctx context.Context, _ routing.Dispatcher, dest xnet.Destination, tag string) (xnet.Conn, error) {
		dials <- tag
		if tag == "missing" {
			return nil, fmt.Errorf("unknown outbound")
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", dest.NetAddr())
	}
	manager := NewManager(context.Background(), nil, []*HealthPingGroup{
		{SubjectSelector: []string{"hkt-socks"}, PingConfig: &HealthPingConfig{
			Destination: server.URL + "/generate_204?ob=hkt", ProbeOutboundTag: "sidecar-uds",
			Timeout: int64(time.Second), SamplingCount: 3,
		}},
		{SubjectSelector: []string{"ordinary"}, PingConfig: &HealthPingConfig{
			Destination: server.URL + "/generate_204", Timeout: int64(time.Second),
		}},
	})
	for _, tc := range []struct {
		tag, transport, request string
		status                  int
		alive                   bool
	}{
		{"hkt-socks", "sidecar-uds", "HEAD /generate_204?ob=hkt", 204, true},
		{"ordinary", "ordinary", "HEAD /generate_204", 204, true},
		{"hkt-socks", "sidecar-uds", "HEAD /generate_204?ob=hkt", 503, false},
		{"hkt-socks", "sidecar-uds", "HEAD /generate_204?ob=hkt", 204, false},
		{"hkt-socks", "sidecar-uds", "HEAD /generate_204?ob=hkt", 204, false},
		{"hkt-socks", "sidecar-uds", "HEAD /generate_204?ob=hkt", 204, true},
	} {
		status.Store(int32(tc.status))
		if err := manager.Check([]string{tc.tag}); err != nil {
			t.Fatal(err)
		}
		if got := <-dials; got != tc.transport {
			t.Fatalf("transport=%q want %q", got, tc.transport)
		}
		if got := <-requests; got != tc.request {
			t.Fatalf("request=%q want %q", got, tc.request)
		}
		h := manager.findGroup(tc.tag)
		if got := h.Results[tc.tag].Get().Alive; got != tc.alive {
			t.Fatalf("subject=%q status=%d alive=%v want %v", tc.tag, tc.status, got, tc.alive)
		}
		if _, exists := h.Results["sidecar-uds"]; exists {
			t.Fatal("transport tag leaked into observation results")
		}
	}
	h := manager.Groups()[0]
	h.Settings.ProbeOutboundTag = "missing"
	if err := h.Check([]string{"hkt-socks"}); err != nil {
		t.Fatal(err)
	}
	if got := <-dials; got != "missing" || h.Results["hkt-socks"].Get().Alive {
		t.Fatal("missing probe transport did not fail closed")
	}
	if len(requests) != 0 || len(dials) != 0 {
		t.Fatal("probe silently retried another transport")
	}
}

func TestProbeOutboundTimeoutAndStop(t *testing.T) {
	contractLoopbackDialer(t)
	entered := make(chan struct{}, 2)
	finished := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-r.Context().Done()
		finished <- struct{}{}
	}))
	defer server.Close()
	h := NewHealthPing(context.Background(), nil, &HealthPingConfig{
		Destination: server.URL, ProbeOutboundTag: "sidecar-uds",
		Timeout: int64(100 * time.Millisecond), SamplingCount: 1,
	})
	if err := h.Check([]string{"hkt-socks"}); err != nil {
		t.Fatal(err)
	}
	contractWait(t, entered, "override timeout request")
	contractWait(t, finished, "override timeout cancellation")
	if h.Results["hkt-socks"].Get().Alive {
		t.Fatal("timed-out local probe reported healthy")
	}
	h = NewHealthPing(context.Background(), nil, &HealthPingConfig{
		Destination: server.URL, ProbeOutboundTag: "sidecar-uds", Timeout: int64(time.Minute),
	})
	h.StartScheduler(func() ([]string, error) { return []string{"hkt-socks"}, nil })
	contractWait(t, entered, "override scheduled request")
	h.StopScheduler()
	contractWait(t, finished, "override stop cancellation")
	if len(h.Results) != 0 {
		t.Fatal("canceled scheduler published a health result")
	}
}
