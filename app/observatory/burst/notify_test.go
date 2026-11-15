package burst

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/features/extension"
)

// Core spec027 E02, E03, E14, E19: every change of the selection view notifies, outside the observer's
// locks, so a subscriber may call back into it.
func TestObserverNotifiesChanges(t *testing.T) {
	o := forceObserver(t)
	o.hpm.SetOnChange(o.changes.Fire)
	var calls atomic.Int32
	reentered := false
	cancel := o.OnChange(func() {
		calls.Add(1)
		if !reentered { // E14: read and change the observer from inside the callback
			reentered = true
			if _, err := o.GetObservation(context.Background()); err != nil {
				t.Error(err)
			}
			if _, err := o.SetForceDown(nil); err != nil {
				t.Error(err)
			}
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		hp := o.hpm.Groups()[0]
		hp.PutResult("alpine", 20*time.Millisecond) // one call, and the re-entrant SetForceDown one more
		if _, err := o.SetForceDown([]ForceDown{{Tag: "ecs"}}); err != nil {
			t.Error(err)
		}
		hp.Cleanup([]string{"ecs"}) // keeps only ecs's results
		o.subscribeRelays()
		extension.NotifyRelayChange()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("E14: a callback that re-enters the observer deadlocked")
	}
	if got := calls.Load(); got != 5 {
		t.Fatalf("notifications = %d, want 5 (result, re-entrant force down, force down, cleanup, relay)", got)
	}
	if s := selection(t, o)["ecs"]; s == nil || s.Alive {
		t.Fatalf("E02: ecs after force down %v", s)
	}
	if _, ok := selection(t, o)["alpine"]; ok {
		t.Fatal("E03: alpine still reported after cleanup")
	}
	o.relayCancel()
	cancel()
	extension.NotifyRelayChange()
	hp := o.hpm.Groups()[0]
	hp.PutResult("alpine", 20*time.Millisecond)
	if got := calls.Load(); got != 5 {
		t.Fatalf("a callback ran after cancel: %d", got)
	}
}

// Core spec027 E18: samples older than the validity window no longer make a probe alive.
func TestExpiredSamplesAreNotAlive(t *testing.T) {
	r := NewHealthPingResult(3, time.Nanosecond)
	r.Put(10 * time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if s := r.Get(); !s.Alive && s.All == 0 {
			return
		}
	}
	t.Fatalf("E18: expired samples still read %+v", r.Get())
}

// Core spec027 E13: a slow subscriber does not enter the measured RTT. Probes send their results into a
// channel sized for the round, so the reader that runs the callbacks never holds a probe back.
func TestSlowSubscriberDoesNotDistortRTT(t *testing.T) {
	contractLoopbackDialer(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	tags := []string{"a", "b", "c", "d"}
	average := func(slow bool) time.Duration {
		h := NewHealthPing(context.Background(), nil, &HealthPingConfig{Destination: server.URL, Timeout: int64(2 * time.Second), SamplingCount: 1})
		if slow {
			h.onChange = func() { time.Sleep(100 * time.Millisecond) }
		}
		if err := h.Check(tags); err != nil {
			t.Fatal(err)
		}
		var sum time.Duration
		for _, tag := range tags {
			s := h.Results[tag].Get()
			if !s.Alive {
				t.Fatalf("%s not alive", tag)
			}
			sum += s.Average
		}
		return sum / time.Duration(len(tags))
	}
	plain, slow := average(false), average(true)
	t.Logf("E13: average RTT %v without a subscriber, %v with one that takes 100ms a call", plain, slow)
	if slow < 30*time.Millisecond || slow-plain > 20*time.Millisecond {
		t.Fatalf("E13: the subscriber entered the measurement: %v vs %v", slow, plain)
	}
}
