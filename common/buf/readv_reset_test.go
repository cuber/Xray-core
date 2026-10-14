//go:build !windows && !wasm && !illumos && !openbsd

package buf

import (
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestReadvReturnsConnectionReset(t *testing.T) {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := server.SetLinger(0); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := client.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	reader := NewReadVReader(client, raw, nil)
	// Select the actual readv path without depending on adaptive warmup.
	reader.alloc.current = 2
	client.SetReadDeadline(time.Now().Add(time.Second))
	mb, err := reader.ReadMultiBuffer()
	ReleaseMulti(mb)
	if !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("readv lost reset: %v", err)
	}
}
