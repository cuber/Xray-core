package splithttp

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
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
	for range 100 {
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
