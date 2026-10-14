//go:build !wasm && !openbsd

package buf

import (
	"io"
	"net"
	"sync"
	"syscall"
	"testing"
)

type interruptReadvObserver struct {
	syscall.RawConn
	blocked chan struct{}
	once    sync.Once
}

func (r *interruptReadvObserver) Read(f func(uintptr) bool) error {
	return r.RawConn.Read(func(fd uintptr) bool {
		ready := f(fd)
		if !ready {
			r.once.Do(func() { close(r.blocked) })
		}
		return ready
	})
}

func TestReadVReaderInterrupt(t *testing.T) {
	for _, kind := range []string{"closable", "interrupt-only"} {
		t.Run(kind, func(t *testing.T) {
			listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			local, err := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer local.Close()
			peer, err := listener.AcceptTCP()
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			raw, err := local.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			observed := &interruptReadvObserver{RawConn: raw, blocked: make(chan struct{})}
			var underlying io.Reader = local
			if kind == "interrupt-only" {
				underlying = &interruptOnlyByteReader{Reader: local, interrupt: func() { local.Close() }}
			}
			reader := NewReadVReader(underlying, observed, nil)
			// Force real readv instead of depending on adaptive single-read warmup.
			reader.alloc.current = 2
			assertReaderInterrupt(t, reader, observed.blocked, func() { local.Close() })
		})
	}
}
