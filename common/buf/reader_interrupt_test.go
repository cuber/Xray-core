package buf

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/common"
)

type interruptReadObserver struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

func (r *interruptReadObserver) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.Conn.Read(p)
}

// Deliberately expose Interrupt but not Close, to check interface forwarding
// rather than only common.Interrupt's io.Closer fallback.
type interruptOnlyByteReader struct {
	io.Reader
	interrupt func()
}

func (r *interruptOnlyByteReader) Interrupt() { r.interrupt() }

func assertReaderInterrupt(t *testing.T, reader Reader, started <-chan struct{}, release func()) {
	t.Helper()
	done := make(chan struct{})
	var readErr error
	var size int32
	go func() {
		defer close(done)
		mb, err := reader.ReadMultiBuffer()
		readErr, size = err, mb.Len()
		ReleaseMulti(mb)
	}()
	// Even the unfixed baseline closes and joins the real blocked read.
	defer func() {
		release()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("read worker did not join after fixture cleanup")
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("read did not enter underlying reader")
	}
	if _, ownsClose := reader.(io.Closer); ownsClose {
		t.Fatal("reader wrapper must not acquire Close ownership")
	}
	if err := common.Close(reader); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatalf("read ended before Interrupt: %v", readErr)
	default:
	}
	if err := common.Interrupt(reader); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if size != 0 || readErr == nil {
			t.Fatalf("interrupted read returned bytes=%d error=%v", size, readErr)
		}
		var timeout net.Error
		if errors.As(readErr, &timeout) && timeout.Timeout() {
			t.Fatalf("read timeout is not an interrupt: %v", readErr)
		}
		if !errors.Is(readErr, net.ErrClosed) && !errors.Is(readErr, io.ErrClosedPipe) {
			t.Fatalf("read did not report local connection closure: %v", readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Interrupt did not unblock underlying read")
	}
}

func TestSingleReaderInterrupt(t *testing.T) {
	for _, kind := range []string{"closable", "interrupt-only"} {
		t.Run(kind, func(t *testing.T) {
			local, peer := net.Pipe()
			defer peer.Close()
			observed := &interruptReadObserver{Conn: local, started: make(chan struct{})}
			var underlying io.Reader = observed
			if kind == "interrupt-only" {
				underlying = &interruptOnlyByteReader{Reader: observed, interrupt: func() { local.Close() }}
			}
			assertReaderInterrupt(t, &SingleReader{Reader: underlying}, observed.started, func() { local.Close() })
		})
	}
}

func TestPacketReaderInterrupt(t *testing.T) {
	for _, kind := range []string{"closable", "interrupt-only"} {
		t.Run(kind, func(t *testing.T) {
			local, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer local.Close()
			observed := &interruptReadObserver{Conn: local, started: make(chan struct{})}
			var underlying io.Reader = observed
			if kind == "interrupt-only" {
				underlying = &interruptOnlyByteReader{Reader: observed, interrupt: func() { local.Close() }}
			}
			assertReaderInterrupt(t, &PacketReader{Reader: underlying}, observed.started, func() { local.Close() })
		})
	}
}
