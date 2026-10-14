package signal

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestActivityTimerStaleCheckDoesNotExpireReplacement(t *testing.T) {
	var calls atomic.Int32
	timer := CancelAfterInactivity(context.Background(), func() { calls.Add(1) }, time.Hour)
	t.Cleanup(func() { timer.SetTimeout(0) })
	oldCheck := timer.checkTask.Execute
	timer.SetTimeout(2 * time.Hour)
	// A timer callback can already be executing when Periodic.Close stops it.
	if err := oldCheck(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("retired check expired the replacement timeout")
	}
	timer.SetTimeout(0)
	if calls.Load() != 1 {
		t.Fatal("timeout callback must run exactly once")
	}
}

func TestActivityTimerReplacementAndActivity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		timer := CancelAfterInactivity(context.Background(), func() { calls.Add(1) }, 10*time.Second)
		t.Cleanup(func() { timer.SetTimeout(0) })
		time.Sleep(3 * time.Second)
		timer.SetTimeout(time.Second)
		time.Sleep(500 * time.Millisecond)
		timer.Update()
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		if calls.Load() != 0 {
			t.Fatal("recent activity was ignored")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("idle replacement interval did not expire")
		}
		timer.SetTimeout(time.Second)
		timer.Update()
		time.Sleep(20 * time.Second)
		if calls.Load() != 1 {
			t.Fatal("consumed timer restarted")
		}
	})
}
