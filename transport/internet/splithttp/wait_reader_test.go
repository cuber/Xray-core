package splithttp

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countedReader struct {
	io.Reader
	closes atomic.Int32
}

func (r *countedReader) Close() error { r.closes.Add(1); return nil }

func TestWaitReadCloserLifecycle(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		w := &WaitReadCloser{Wait: make(chan struct{})}
		r := &countedReader{Reader: strings.NewReader("response")}
		if closeFirst {
			_ = w.Close()
		}
		w.Set(r)
		data, err := io.ReadAll(w)
		if closeFirst {
			if err != io.ErrClosedPipe {
				t.Fatalf("read after close: %v", err)
			}
		} else if err != nil || string(data) != "response" {
			t.Fatalf("read: %q %v", data, err)
		}
		_ = w.Close()
		_ = w.Close()
		if r.closes.Load() != 1 {
			t.Fatal("underlying response must close exactly once")
		}
	}
}

func TestWaitReadCloserConcurrentSetClose(t *testing.T) {
	for range 1000 {
		w := &WaitReadCloser{Wait: make(chan struct{})}
		r := &countedReader{Reader: strings.NewReader("response")}
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); w.Set(r) }()
		go func() { defer wg.Done(); _ = w.Close() }()
		go func() { defer wg.Done(); _, _ = io.ReadAll(w) }()
		wg.Wait()
		if r.closes.Load() != 1 {
			t.Fatal("underlying response must close exactly once")
		}
	}
}

func TestWaitReadCloserBlockedAndRepeatedSet(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		w := &WaitReadCloser{Wait: make(chan struct{})}
		first := &countedReader{Reader: strings.NewReader("first")}
		second := &countedReader{Reader: strings.NewReader("second")}
		started, done := make(chan struct{}), make(chan struct{})
		var data []byte
		var err error
		go func() {
			close(started)
			data, err = io.ReadAll(w)
			close(done)
		}()
		<-started
		select {
		case <-done:
			t.Fatal("Read returned before Set or Close")
		default:
		}
		if closeFirst {
			_ = w.Close()
		}
		w.Set(first)
		w.Set(second)
		select {
		case <-done:
		case <-time.After(time.Second):
			_ = w.Close()
			t.Fatal("blocked Read was not woken")
		}
		if closeFirst {
			if err != io.ErrClosedPipe || len(data) != 0 {
				t.Fatalf("read after close: %q, %v", data, err)
			}
		} else if err != nil || string(data) != "first" {
			t.Fatalf("repeated Set replaced reader: %q, %v", data, err)
		}
		_ = w.Close()
		_ = w.Close()
		if first.closes.Load() != 1 || second.closes.Load() != 1 {
			t.Fatalf("close counts: %d, %d", first.closes.Load(), second.closes.Load())
		}
	}
}
