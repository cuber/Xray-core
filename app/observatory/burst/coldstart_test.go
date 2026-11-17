package burst

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Core spec027 TL-10, cases K01-K05: the scheduler's cold start. The probe target answers each request after
// a delay the test chooses by the request's number, so a slow first request stands for a connection set up
// by the first probe.

type coldTarget struct {
	*httptest.Server
	requests atomic.Int32
	mu       sync.Mutex
	at       []time.Time
}

func newColdTarget(t *testing.T, answer func(n int32, w http.ResponseWriter, r *http.Request)) *coldTarget {
	t.Helper()
	c := &coldTarget{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := c.requests.Add(1)
		c.mu.Lock()
		c.at = append(c.at, time.Now())
		c.mu.Unlock()
		answer(n, w, r)
	}))
	t.Cleanup(c.Close)
	return c
}

func coldHealthPing(destinations map[string]string, spacing time.Duration) *HealthPing {
	h := NewHealthPing(context.Background(), nil, &HealthPingConfig{
		Destination: destinations[""], DestinationsByPrefix: destinations,
		Interval: int64(time.Hour), Timeout: int64(2 * time.Second), SamplingCount: 10,
	})
	h.Settings.coldSpacing = spacing
	return h
}

func coldStats(h *HealthPing, tag string) HealthPingStats {
	h.access.Lock()
	defer h.access.Unlock()
	if r := h.Results[tag]; r != nil {
		return *r.Get()
	}
	return HealthPingStats{}
}

// K01: a successful first probe is not recorded; coldStartSamples samples follow, coldSpacing apart.
func TestColdStartDoesNotRecordTheFirstProbe(t *testing.T) {
	contractLoopbackDialer(t)
	target := newColdTarget(t, func(n int32, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			time.Sleep(300 * time.Millisecond)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := coldHealthPing(map[string]string{"": target.URL}, 100*time.Millisecond)
	if h.Settings.coldSamples != 4 || coldStartSpacing != time.Second {
		t.Fatalf("cold start defaults: %d samples, %v apart", h.Settings.coldSamples, coldStartSpacing)
	}
	h.coldStart(context.Background(), []string{"probe"})
	s := coldStats(h, "probe")
	if target.requests.Load() != 5 || s.All != 4 || s.Fail != 0 || !s.Alive {
		t.Fatalf("requests=%d stats=%+v", target.requests.Load(), s)
	}
	if s.Max >= 300*time.Millisecond {
		t.Fatalf("the first probe was recorded: max %v", s.Max)
	}
	target.mu.Lock()
	defer target.mu.Unlock()
	for i := 2; i < 5; i++ { // the second request follows the first at once, the later ones are spaced
		if gap := target.at[i].Sub(target.at[i-1]); gap < 100*time.Millisecond {
			t.Fatalf("request %d only %v after the previous", i+1, gap)
		}
	}
}

// K02: the first recorded sample is published at once; the later ones do not hold it back.
func TestColdStartPublishesTheFirstSampleAtOnce(t *testing.T) {
	contractLoopbackDialer(t)
	release := make(chan struct{})
	target := newColdTarget(t, func(n int32, w http.ResponseWriter, r *http.Request) {
		if n == 3 {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := coldHealthPing(map[string]string{"": target.URL}, time.Millisecond)
	changed := make(chan struct{}, 16)
	h.onChange = func() { changed <- struct{}{} }
	done := make(chan struct{})
	go func() { h.coldStart(context.Background(), []string{"probe"}); close(done) }()
	contractWait(t, changed, "first recorded sample")
	if s := coldStats(h, "probe"); s.All != 1 || !s.Alive {
		t.Fatalf("after the first recorded sample: %+v", s)
	}
	close(release)
	contractWait(t, done, "cold start end")
	if s := coldStats(h, "probe"); s.All != 4 {
		t.Fatalf("after the cold start: %+v", s)
	}
}

// K03: a failed first probe is recorded and ends that outbound's cold start; another outbound's continues.
func TestColdStartRecordsAFailureAndEndsThatOutbound(t *testing.T) {
	contractLoopbackDialer(t)
	failing := newColdTarget(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	healthy := newColdTarget(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := coldHealthPing(map[string]string{"": healthy.URL, "down": failing.URL}, time.Millisecond)
	h.coldStart(context.Background(), []string{"down", "up"})
	if s := coldStats(h, "down"); failing.requests.Load() != 1 || s.All != 1 || s.Fail != 1 || s.Alive {
		t.Fatalf("failing: requests=%d stats=%+v", failing.requests.Load(), s)
	}
	if s := coldStats(h, "up"); healthy.requests.Load() != 5 || s.All != 4 || !s.Alive {
		t.Fatalf("healthy: requests=%d stats=%+v", healthy.requests.Load(), s)
	}
}

// K04: a recorded sample that fails ends the cold start there.
func TestColdStartEndsAtAFailedSample(t *testing.T) {
	contractLoopbackDialer(t)
	target := newColdTarget(t, func(n int32, w http.ResponseWriter, _ *http.Request) {
		if n == 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := coldHealthPing(map[string]string{"": target.URL}, time.Millisecond)
	h.coldStart(context.Background(), []string{"probe"})
	if s := coldStats(h, "probe"); target.requests.Load() != 3 || s.All != 2 || s.Fail != 1 {
		t.Fatalf("requests=%d stats=%+v", target.requests.Load(), s)
	}
}

// K05: Stop during the cold start's spacing returns at once and nothing is published afterwards; scheduled
// probes due during the cold start do not run before it ends (with the upstream contract test of rounds).
func TestColdStartStopsWithTheScheduler(t *testing.T) {
	contractLoopbackDialer(t)
	target := newColdTarget(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := NewHealthPing(context.Background(), nil, &HealthPingConfig{
		Destination: target.URL, Interval: int64(time.Millisecond), Timeout: int64(2 * time.Second), SamplingCount: 1,
	})
	h.Settings.coldSpacing = time.Hour
	changed := make(chan struct{}, 16)
	h.onChange = func() { changed <- struct{}{} }
	h.StartScheduler(func() ([]string, error) { return []string{"probe"}, nil })
	contractWait(t, changed, "first recorded sample")
	time.Sleep(50 * time.Millisecond) // rounds tick every millisecond meanwhile
	if n := target.requests.Load(); n != 2 {
		t.Fatalf("%d requests during the cold start's spacing, want the first probe and one sample", n)
	}
	done := make(chan struct{})
	go func() { h.StopScheduler(); close(done) }()
	contractWait(t, done, "Stop during the spacing")
	// The window here is 2ms long, so count requests and publications rather than reading expired stats.
	if n, more := target.requests.Load(), len(changed); n != 2 || more != 0 {
		t.Fatalf("after Stop: %d requests, %d more publications", n, more)
	}
}
