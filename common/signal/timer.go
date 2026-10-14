package signal

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/task"
)

type ActivityUpdater interface {
	Update()
}

type ActivityTimer struct {
	mu        sync.RWMutex
	updated   chan struct{}
	checkTask *task.Periodic
	onTimeout func()
	consumed  atomic.Bool
}

func (t *ActivityTimer) Update() {
	select {
	case t.updated <- struct{}{}:
	default:
	}
}

func (t *ActivityTimer) check(owner *task.Periodic) error {
	t.mu.Lock()
	if t.consumed.Load() || t.checkTask != owner {
		t.mu.Unlock()
		return nil
	}
	select {
	case <-t.updated:
		t.mu.Unlock()
	default:
		t.consumed.Store(true)
		common.CloseIfExists(t.checkTask)
		t.mu.Unlock()
		t.onTimeout()
	}
	return nil
}

func (t *ActivityTimer) finish() {
	t.mu.Lock()
	if t.consumed.Load() {
		t.mu.Unlock()
		return
	}
	t.consumed.Store(true)
	common.CloseIfExists(t.checkTask)
	t.mu.Unlock()
	t.onTimeout()
}

func (t *ActivityTimer) SetTimeout(timeout time.Duration) {
	if t.consumed.Load() {
		return
	}
	if timeout == 0 {
		t.finish()
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	// double check, just in case
	if t.consumed.Load() {
		return
	}
	newCheckTask := &task.Periodic{
		Interval: timeout,
	}
	var primed atomic.Bool
	newCheckTask.Execute = func() error {
		// Periodic.Start executes synchronously while SetTimeout owns mu.
		// Arm the interval without checking activity until its first tick.
		if primed.CompareAndSwap(false, true) {
			return nil
		}
		return t.check(newCheckTask)
	}
	common.CloseIfExists(t.checkTask)
	t.checkTask = newCheckTask
	select {
	case <-t.updated:
	default:
	}
	common.Must(newCheckTask.Start())
}

func CancelAfterInactivity(ctx context.Context, cancel context.CancelFunc, timeout time.Duration) *ActivityTimer {
	timer := &ActivityTimer{
		updated:   make(chan struct{}, 1),
		onTimeout: cancel,
	}
	timer.SetTimeout(timeout)
	return timer
}
