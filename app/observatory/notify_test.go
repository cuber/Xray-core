package observatory

import (
	"context"
	"testing"
	"time"
)

// Core spec027 E04: a classic result notifies after statusLock is released.
func TestObserverNotifiesResults(t *testing.T) {
	o := &Observer{}
	calls := 0
	cancel := o.OnChange(func() {
		calls++
		if _, err := o.GetObservation(context.Background()); err != nil {
			t.Error(err)
		}
	})
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		o.updateStatusForResult("a", &ProbeResult{Alive: true, Delay: 5})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a callback reading the observation deadlocked")
	}
	if calls != 1 {
		t.Fatalf("notifications = %d", calls)
	}
}
