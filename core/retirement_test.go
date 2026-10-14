package core

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestInstanceCloseJoinsOwnedRetirement(t *testing.T) {
	s := new(Instance)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	if !s.OnClose(func() { calls.Add(1); close(entered); <-release }) {
		t.Fatal("registration refused")
	}
	go func() { s.Close(); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("retirement never started")
	}
	if s.OnClose(func() { t.Error("late hook ran") }) {
		t.Error("accepted resource during shutdown")
	}
	select {
	case <-done:
		t.Error("Close returned before owned cleanup")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not join cleanup")
	}
	s.Close()
	if calls.Load() != 1 {
		t.Fatal("cleanup was not exactly once")
	}
}
