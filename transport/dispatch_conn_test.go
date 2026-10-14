package transport

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/transport/pipe"
)

func TestNewDispatchConnPreservesTimeoutReader(t *testing.T) {
	result := make(chan bool, 1)

	conn := NewDispatchConn(context.Background(), []pipe.Option{pipe.WithSizeLimit(16)}, DispatchConnOutputStream, func(ctx context.Context, link *Link) {
		_, ok := link.Reader.(buf.TimeoutReader)
		result <- ok
	})
	defer conn.Close()

	select {
	case ok := <-result:
		if !ok {
			t.Fatal("expected borrowed reader to preserve buf.TimeoutReader")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dispatch result")
	}
}

func TestNewDispatchConnForwardsInterrupt(t *testing.T) {
	var linkReader buf.Reader
	entered := make(chan struct{})
	conn := NewDispatchConn(context.Background(), []pipe.Option{pipe.WithSizeLimit(16)}, DispatchConnOutputStream, func(ctx context.Context, link *Link) {
		linkReader = link.Reader
		close(entered)
		<-ctx.Done()
	})
	defer conn.Close()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runner to start")
	}

	// Interrupting the borrowed reader must abort the underlying pipe, not
	// silently no-op. singbridge.PipeConnWrapper relies on this path to recover
	// from a stuck Read on chained outbounds.
	common.Interrupt(linkReader)

	// After interrupt, reads must return promptly with an error instead of
	// hanging forever.
	done := make(chan error, 1)
	go func() {
		_, err := linkReader.ReadMultiBuffer()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from reader after Interrupt")
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not unblock after Interrupt — borrowedReader dropped the signal")
	}
}

// TestNewDispatchConnDoesNotCloseWhenRunnerExits guards the self-loop
// regression: if the runner returns before the caller has had a chance to
// write its first byte (e.g. dest=127.0.0.1:<sidecar> where Dispatch completes
// almost immediately), preemptively closing the response pipe there would
// propagate a zero-payload EOF/FIN back to the caller before any bytes ever
// flowed. Instead the runner's exit must leave pipes usable; only the
// caller's Close is allowed to tear them down.
func TestNewDispatchConnDoesNotCloseWhenRunnerExits(t *testing.T) {
	done := make(chan struct{})
	conn := NewDispatchConn(context.Background(), []pipe.Option{pipe.WithSizeLimit(64 * 1024)}, DispatchConnOutputStream, func(ctx context.Context, link *Link) {
		// Fast-path runner: returns immediately without reading or writing.
		close(done)
	})
	defer conn.Close()

	<-done
	// Give the goroutine a moment to settle any (buggy) teardown.
	time.Sleep(50 * time.Millisecond)

	// A subsequent Write from the caller must succeed. Before this fix the
	// runner's exit would call common.Interrupt on the request writer, so the
	// Write here would fail with io.ErrClosedPipe — that is the exact bug
	// surfacing as a zero-payload FIN on the wire in production.
	n, err := conn.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write after runner exit should still succeed, got err=%v", err)
	}
	if n != 5 {
		t.Fatalf("short write: wrote %d, want 5", n)
	}
}

func TestNewDispatchConnCloseUnblocksRunnerRead(t *testing.T) {
	entered := make(chan struct{})
	readErr := make(chan error, 1)

	conn := NewDispatchConn(context.Background(), []pipe.Option{pipe.WithSizeLimit(16)}, DispatchConnOutputStream, func(ctx context.Context, link *Link) {
		close(entered)
		_, err := link.Reader.ReadMultiBuffer()
		readErr <- err
	})

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runner to start")
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("failed to close conn: %v", err)
	}

	select {
	case err := <-readErr:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("expected io.EOF after conn close, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runner read to unblock")
	}
}
