package signal_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/signal"
)

// Core spec027 TL-5: cancel waits for a call in flight; no call after cancel returns.
func TestBroadcastCancelWaitsForACallInFlight(t *testing.T) {
	var b signal.Broadcast
	b.Fire() // no subscriber
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	cancel := b.Subscribe(func() {
		calls.Add(1)
		once.Do(func() {
			close(entered)
			<-release
		})
	})
	go b.Fire()
	<-entered
	cancelled := make(chan struct{})
	go func() {
		cancel()
		close(cancelled)
	}()
	select {
	case <-cancelled:
		t.Fatal("cancel returned while a call was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-cancelled
	b.Fire()
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d", got)
	}
	cancel() // idempotent
}

func TestBroadcastFiresEverySubscriber(t *testing.T) {
	var b signal.Broadcast
	var a, c atomic.Int32
	cancelA := b.Subscribe(func() { a.Add(1) })
	defer b.Subscribe(func() { c.Add(1) })()
	b.Fire()
	cancelA()
	b.Fire()
	if a.Load() != 1 || c.Load() != 2 {
		t.Fatalf("a %d c %d", a.Load(), c.Load())
	}
}
