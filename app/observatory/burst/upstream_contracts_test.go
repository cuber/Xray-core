package burst

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUpstreamZeroRTTSampleAndRecovery(t *testing.T) {
	h := NewHealthPingResult(20, time.Hour)
	if got := h.GetWithCache(); got.Alive || got.All != 0 {
		t.Fatalf("empty cached stats: %+v", got)
	}
	h.Put(0)
	if got := h.GetWithCache(); !got.Alive || got.All != 1 || got.Average != 0 {
		t.Fatalf("zero RTT is a tested success: %+v", got)
	}
	h.Put(rttFailed)
	for i := 1; i <= 3; i++ {
		h.Put(0)
		if got := h.GetWithCache(); got.Alive != (i == 3) || got.All != i+2 || got.Fail != 1 {
			t.Fatalf("zero RTT recovery %d: %+v", i, got)
		}
	}
	for _, sample := range h.rtts {
		sample.time = time.Now().Add(-2 * time.Hour)
	}
	if got := h.GetWithCache(); got.Alive || got.All != 0 {
		t.Fatalf("expired zero RTT: %+v", got)
	}
}

func TestUpstreamSchedulerSupersedesInflightRound(t *testing.T) {
	contractLoopbackDialer(t)
	entered := make(chan context.Context, 64)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		entered <- r.Context()
		<-r.Context().Done()
	}))
	defer server.Close()
	h := NewHealthPing(context.Background(), nil, &HealthPingConfig{
		Destination: server.URL, Interval: int64(200 * time.Millisecond),
		Timeout: int64(time.Minute), SamplingCount: 1,
	})
	h.StartScheduler(func() ([]string, error) { return []string{"probe"}, nil })
	defer h.StopScheduler()
	request := func() context.Context {
		select {
		case ctx := <-entered:
			return ctx
		case <-time.After(2 * time.Second):
			t.Fatal("initial and scheduled probes did not start")
			return nil
		}
	}
	first, second := request(), request()
	// One request is the initial check, the other belongs to the first round.
	// Only the scheduled request should be superseded by the next tick.
	select {
	case <-first.Done():
	case <-second.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("next round did not cancel an in-flight prior round")
	}
	h.StopScheduler()
	if len(h.Results) != 0 {
		t.Fatal("superseded/stopped round published a sample")
	}
	h.PutResult("explicit", time.Millisecond)
	if !h.Results["explicit"].Get().Alive {
		t.Fatal("scheduler stop disabled independent result publication")
	}
}

func TestUpstreamSchedulerParentCancellationJoinsWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := NewHealthPing(ctx, nil, &HealthPingConfig{Interval: int64(time.Hour)})
	entered := make(chan struct{}, 2)
	h.StartScheduler(func() ([]string, error) { entered <- struct{}{}; return nil, nil })
	defer h.StopScheduler()
	contractWait(t, entered, "initial selector")
	contractWait(t, entered, "scheduled selector")
	cancel()
	done := make(chan struct{})
	go func() { h.workers.Wait(); close(done) }()
	contractWait(t, done, "parent cancellation joins scheduler without Stop")
}
