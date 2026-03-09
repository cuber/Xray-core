package transport

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

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

func TestNewDispatchConnInterruptsRequestWriterWhenRunnerReturns(t *testing.T) {
	conn := NewDispatchConn(context.Background(), []pipe.Option{pipe.WithSizeLimit(16)}, DispatchConnOutputStream, func(ctx context.Context, link *Link) {})
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for {
		_, err := conn.Write([]byte("hello"))
		if errors.Is(err, io.ErrClosedPipe) {
			return
		}
		if err != nil {
			t.Fatalf("expected io.ErrClosedPipe, got %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for request writer to be interrupted")
		}
		time.Sleep(10 * time.Millisecond)
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
