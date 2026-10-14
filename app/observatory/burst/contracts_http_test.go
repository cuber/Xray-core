package burst

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport/internet/tagged"
)

// Keep these tests serial: tagged.Dialer is a process-wide integration hook.
func contractLoopbackDialer(t *testing.T) {
	t.Helper()
	old := tagged.Dialer
	tagged.Dialer = func(ctx context.Context, _ routing.Dispatcher, dest xnet.Destination, _ string) (xnet.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", dest.NetAddr())
	}
	t.Cleanup(func() { tagged.Dialer = old })
}

func contractWait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out: %s", what)
	}
}

func TestContractProbeConnectionOwnsDispatchContext(t *testing.T) {
	old := tagged.Dialer
	t.Cleanup(func() { tagged.Dialer = old })
	var contexts []context.Context
	tagged.Dialer = func(ctx context.Context, _ routing.Dispatcher, _ xnet.Destination, _ string) (xnet.Conn, error) {
		contexts = append(contexts, ctx)
		client, peer := net.Pipe()
		t.Cleanup(func() { client.Close(); peer.Close() })
		return client, nil
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := newHTTPClient(parent, nil, "probe", time.Second, true)
	transport := client.Transport.(*http.Transport)
	// net/http can detach a dial from request cancellation to permit reuse.
	// The observer parent must still cancel its owned dispatch on shutdown.
	first, err := transport.DialContext(context.WithoutCancel(parent), "tcp", "example.test:80")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := transport.DialContext(context.WithoutCancel(parent), "tcp", "example.test:80")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	first.Close()
	contractWait(t, contexts[0].Done(), "closing probe connection cancels dispatch")
	if contexts[1].Err() != nil || parent.Err() != nil {
		t.Fatal("connection close canceled another connection or the observer")
	}
	cancel()
	contractWait(t, contexts[1].Done(), "observer cancellation reaches detached dispatch")
}

func TestContractProbeQueryCapture(t *testing.T) {
	contractLoopbackDialer(t)
	requests := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.RequestURI
		w.WriteHeader(204)
	}))
	defer server.Close()
	h := NewHealthPing(context.Background(), nil, &HealthPingConfig{
		Destination: server.URL + "/default?ob=z&raw=%2F", Timeout: int64(time.Second),
		DestinationsByPrefix: map[string]string{"a": server.URL + "/a?ob=x", "a-b": server.URL + "/ab?ob=y"},
	})
	for _, tc := range []struct{ tag, uri string }{{"a-x", "/a?ob=x"}, {"a-b-x", "/ab?ob=y"}, {"z", "/default?ob=z&raw=%2F"}} {
		if err := h.Check([]string{tc.tag}); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-requests:
			if got != tc.uri {
				t.Fatalf("%s: URI=%q want %q", tc.tag, got, tc.uri)
			}
			t.Logf("%s -> %s", tc.tag, got)
		default:
			t.Fatal("probe did not reach HTTP handler")
		}
		if !h.Results[tc.tag].Get().Alive {
			t.Fatalf("%s unhealthy", tc.tag)
		}
	}
}

func TestContractHTTPStatusesAndConnections(t *testing.T) {
	contractLoopbackDialer(t)
	var redirects atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/204":
			w.WriteHeader(204)
		case "/200":
			w.WriteHeader(200)
		case "/302":
			http.Redirect(w, r, "/target", 302)
		case "/503":
			w.WriteHeader(503)
		case "/target":
			redirects.Add(1)
			w.WriteHeader(204)
		case "/timeout":
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	for _, path := range []string{"204", "200", "302", "503", "timeout"} {
		t.Run(path, func(t *testing.T) {
			c := newPingClient(context.Background(), nil, server.URL+"/"+path, 100*time.Millisecond, "probe", false)
			defer c.CloseIdleConnections()
			delay, err := c.MeasureDelay(http.MethodGet)
			if (err == nil) != (path == "204") {
				t.Fatalf("delay=%v err=%v", delay, err)
			}
			if err != nil && delay != rttFailed {
				t.Fatalf("failure delay=%v", delay)
			}
		})
	}
	if redirects.Load() != 0 {
		t.Fatalf("redirect target hit %d times", redirects.Load())
	}
	for _, keep := range []bool{false, true} {
		t.Run(fmt.Sprintf("keepAlive=%v", keep), func(t *testing.T) {
			var accepts atomic.Int32
			var mu sync.Mutex
			var peers []string
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				peers = append(peers, r.RemoteAddr)
				mu.Unlock()
				w.WriteHeader(204)
			}))
			s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					accepts.Add(1)
				}
			}
			s.Start()
			defer s.Close()
			c := newPingClient(context.Background(), nil, s.URL, time.Second, "probe", keep)
			defer c.CloseIdleConnections()
			for range 10 {
				if _, err := c.MeasureDelay(http.MethodGet); err != nil {
					t.Fatal(err)
				}
			}
			want := int32(10)
			if keep {
				want = 1
			}
			if accepts.Load() != want {
				t.Fatalf("accepts=%d want %d", accepts.Load(), want)
			}
			mu.Lock()
			defer mu.Unlock()
			t.Logf("accepts=%d request peers=%v", accepts.Load(), peers)
		})
	}
}

func TestContractManagerGroupIsolation(t *testing.T) {
	contractLoopbackDialer(t)
	entered := make(chan struct{})
	canceled := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(canceled) }))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI != "/b?ob=b" {
			t.Errorf("B URI=%s", r.RequestURI)
		}
		w.WriteHeader(204)
	}))
	defer fast.Close()
	m := NewManager(context.Background(), nil, []*HealthPingGroup{
		{SubjectSelector: []string{"a"}, PingConfig: &HealthPingConfig{Destination: slow.URL + "/a", Timeout: int64(500 * time.Millisecond)}},
		{SubjectSelector: []string{"b"}, PingConfig: &HealthPingConfig{Destination: fast.URL + "/b?ob=b", Timeout: int64(time.Second)}},
	})
	defer m.StopAll()
	done := make(chan struct{})
	go func() { defer close(done); _ = m.Check([]string{"a-x"}) }()
	contractWait(t, entered, "slow request entered")
	if err := m.Check([]string{"b-x", "unmatched"}); err != nil {
		t.Fatal(err)
	}
	if !m.groups[1].Results["b-x"].Get().Alive {
		t.Fatal("B not healthy while A blocked")
	}
	contractWait(t, done, "A timeout")
	contractWait(t, canceled, "A handler canceled")
	if m.groups[0].Results["a-x"].Get().Alive {
		t.Fatal("A timeout healthy")
	}
	if len(m.groups[0].Results) != 1 || len(m.groups[1].Results) != 1 {
		t.Fatal("cross-group results")
	}
	if err := ValidatePingGroups([]*HealthPingGroup{{SubjectSelector: []string{"a", "a-b"}}}); err != nil {
		t.Fatalf("same-group nesting: %v", err)
	}
	noop := NewManager(context.Background(), nil, resolvePingGroups(&Config{PingConfig: &HealthPingConfig{Destination: slow.URL}}))
	noop.StartAll(func([]string) ([]string, error) { t.Error("disabled group resolved tags"); return nil, nil })
	_ = noop.Check([]string{"a"})
	noop.StopAll()
}

func TestContractSchedulerStopCancelsInflight(t *testing.T) {
	contractLoopbackDialer(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var active atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		once.Do(func() { close(entered) })
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	h := NewHealthPing(context.Background(), nil, &HealthPingConfig{Destination: server.URL, Interval: int64(3 * time.Second), Timeout: int64(2 * time.Second), SamplingCount: 20})
	if h.Settings.Interval != 3*time.Second || h.Settings.Timeout != 2*time.Second || h.Settings.SamplingCount != 20 {
		t.Fatalf("3s/2s/20 changed: %+v", h.Settings)
	}
	h.StartScheduler(func() ([]string, error) { return []string{"probe"}, nil })
	contractWait(t, entered, "in-flight request")
	done := make(chan struct{})
	m := &Manager{groups: []*HealthPing{h}}
	go func() { m.StopAll(); close(done) }()
	contractWait(t, done, "StopScheduler joins probes and delayed samples")
	deadline := time.After(time.Second)
	for active.Load() != 0 {
		select {
		case <-deadline:
			t.Fatal("StopScheduler left live HTTP request")
		case <-time.After(time.Millisecond):
		}
	}
	h.access.Lock()
	count := len(h.Results)
	h.access.Unlock()
	if count != 0 {
		t.Fatalf("canceled probes published %d results", count)
	}
	h.StopScheduler()
}

func TestContractHealthRecoveryCacheMatrix(t *testing.T) {
	for _, capacity := range []int{1, 2, 20} {
		for _, seq := range []string{"S", "SF", "FSSS", "FSFSSS"} {
			t.Run(fmt.Sprintf("cap%d/%s", capacity, seq), func(t *testing.T) {
				h := NewHealthPingResult(capacity, time.Hour)
				if h.Get().Alive {
					t.Fatal("empty alive")
				}
				recovering, streak := false, 0
				for _, sample := range seq {
					value := time.Millisecond
					if sample == 'F' {
						value = rttFailed
						recovering = true
						streak = 0
					} else {
						streak++
					}
					want := sample == 'S' && (!recovering || streak >= min(3, capacity))
					h.Put(value)
					if got := h.GetWithCache().Alive; got != want {
						t.Fatalf("sample=%c streak=%d alive=%v want %v", sample, streak, got, want)
					}
					t.Logf("sample=%c alive=%v recovery=%d", sample, want, h.successesSinceFailure)
				}
				for range 3 {
					h.Put(time.Millisecond)
				}
				if !h.GetWithCache().Alive {
					t.Fatal("pre-expiry cache not healthy")
				}
				// Move sample timestamps, not the system clock; expiry and cache are real code paths.
				for _, r := range h.rtts {
					r.time = time.Now().Add(-2 * time.Hour)
				}
				if s := h.GetWithCache(); s.Alive || s.All != 0 {
					t.Fatalf("expired cached stats=%+v", s)
				}
				for i := 1; i <= min(3, capacity); i++ {
					h.Put(time.Millisecond)
					if got := h.GetWithCache().Alive; got != (i == min(3, capacity)) {
						t.Fatalf("post-expiry success %d alive=%v", i, got)
					}
				}
			})
		}
	}
}

func TestContractManagerBlockedCallbackAndSnapshot(t *testing.T) {
	h := NewHealthPing(context.Background(), nil, &HealthPingConfig{SamplingCount: 20})
	h.PutResult("a", time.Millisecond)
	m := &Manager{groups: []*HealthPing{h}}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		m.WalkResults(func(_ string, s HealthPingStats) { close(entered); <-release; s.Alive = false; s.All = 999 })
	}()
	contractWait(t, entered, "callback")
	put := make(chan struct{})
	go func() { h.PutResult("a", time.Millisecond); close(put) }()
	select {
	case <-put:
	case <-time.After(time.Second):
		close(release)
		contractWait(t, done, "callback cleanup")
		t.Fatal("PutResult blocked by callback")
	}
	close(release)
	contractWait(t, done, "walk complete")
	m.WalkResults(func(_ string, s HealthPingStats) {
		if !s.Alive || s.All != 2 {
			t.Fatalf("snapshot mutation leaked: %+v", s)
		}
	})
}

func TestContractSchedulerConcurrentStartStop(t *testing.T) {
	h := NewHealthPing(context.Background(), nil, &HealthPingConfig{Interval: int64(3 * time.Second), SamplingCount: 20})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				h.StartScheduler(func() ([]string, error) { return nil, nil })
				h.StopScheduler()
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	contractWait(t, done, "concurrent Start/Stop")
	h.StopScheduler()
	if h.ticker != nil || h.cancel != nil || h.tickerClose != nil {
		t.Fatal("scheduler state retained after Stop")
	}
}

func TestContractSchedulerStopDoesNotWaitForSelector(t *testing.T) {
	h := NewHealthPing(context.Background(), nil, nil)
	entered, release, returned := make(chan struct{}, 2), make(chan struct{}), make(chan struct{}, 2)
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	h.StartScheduler(func() ([]string, error) {
		entered <- struct{}{}
		<-release
		returned <- struct{}{}
		return []string{"must-not-probe"}, nil
	})
	contractWait(t, entered, "blocked selector")
	contractWait(t, entered, "blocked scheduled selector")
	done := make(chan struct{})
	go func() { h.StopScheduler(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		unblock()
		contractWait(t, done, "selector cleanup")
		t.Fatal("Stop waited for uncancellable selector")
	}
	// The callback owns its resources; release it even though Stop has returned.
	unblock()
	contractWait(t, returned, "selector released")
	contractWait(t, returned, "scheduled selector released")
	if len(h.Results) != 0 {
		t.Fatal("canceled selector published results")
	}
}

func TestContractCanceledCheckDoesNotPublish(t *testing.T) {
	contractLoopbackDialer(t)
	entered, release := make(chan struct{}), make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(204) }))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := NewHealthPing(ctx, nil, &HealthPingConfig{Destination: s.URL, Timeout: int64(time.Second)})
	done := make(chan struct{})
	go func() { defer close(done); _ = h.Check([]string{"a"}) }()
	contractWait(t, entered, "check request")
	h.access.Lock()
	cancel()
	close(release)
	h.access.Unlock()
	contractWait(t, done, "canceled Check joined")
	if len(h.Results) != 0 {
		t.Fatal("canceled successful request published a sample")
	}
}
