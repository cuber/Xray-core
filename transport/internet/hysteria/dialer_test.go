package hysteria

import (
	"context"
	"testing"
	"time"
)

func TestClientManagerGetOrCreateDoesNotHoldManagerLockWhileSettingContext(t *testing.T) {
	m := &clientManager{m: map[dialerConf]*client{}}
	conf := dialerConf{}
	c := &client{}
	c.mutex.Lock()
	m.m[conf] = c

	done := make(chan struct{})
	created := make(chan struct{}, 1)
	go func() {
		got := m.getOrCreate(conf, func() *client {
			created <- struct{}{}
			return &client{}
		})
		got.setCtx(context.Background())
		close(done)
	}()

	assertManagerLockAvailable(t, m)

	select {
	case <-created:
		t.Fatal("unexpected client creation")
	default:
	}

	c.mutex.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("setCtx did not finish after client lock was released")
	}
}

func TestClientManagerCleanDoesNotHoldManagerLockWhileCleaningClient(t *testing.T) {
	m := &clientManager{m: map[dialerConf]*client{dialerConf{}: &client{}}}
	for _, c := range m.m {
		c.mutex.Lock()
	}

	done := make(chan struct{})
	go func() {
		m.clean()
		close(done)
	}()

	assertManagerLockAvailable(t, m)

	select {
	case <-done:
		t.Fatal("clean unexpectedly finished while client lock is held")
	default:
	}

	for _, c := range m.m {
		c.mutex.Unlock()
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("clean did not finish after client lock was released")
	}
}

func assertManagerLockAvailable(t *testing.T, m *clientManager) {
	t.Helper()

	acquired := make(chan struct{})
	go func() {
		m.mutex.Lock()
		m.mutex.Unlock()
		close(acquired)
	}()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("manager mutex is blocked by a per-client operation")
	}
}
