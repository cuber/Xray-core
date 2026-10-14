package anytls

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/xtls/xray-core/common/buf"
)

type deliveredWriteConn struct {
	net.Conn
	marker                    []byte
	entered, closed, returned chan struct{}
	once                      sync.Once
}

func (c *deliveredWriteConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err == nil && bytes.Contains(p, c.marker) {
		close(c.entered)
		<-c.closed
		close(c.returned)
		return n, net.ErrClosed
	}
	return n, err
}

func (c *deliveredWriteConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })
	return err
}

type deliveredReceiptHandler struct {
	size     int
	receipts chan string
	done     chan struct{}
}

func (h deliveredReceiptHandler) NewConnectionEx(_ context.Context, conn net.Conn, _, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	defer func() { conn.Close(); h.done <- struct{}{} }()
	if N.ReportHandshakeSuccess(conn) != nil {
		return
	}
	request := make([]byte, h.size)
	if _, err := io.ReadFull(conn, request); err != nil {
		return
	}
	h.receipts <- string(request)
	if _, err := conn.Write(request); err != nil {
		return
	}
	io.Copy(io.Discard, conn)
}

func TestClientDeliveredWriteCancellationAndFreshRecovery(t *testing.T) {
	first, second := []byte("first-request-id-0001"), []byte("fresh-request-id-0002")
	peer := deliveredReceiptHandler{len(first), make(chan string, 8), make(chan struct{}, 8)}
	c, dialer, count := clientFixture(t, peer)
	dial := dialer.dial
	held := &deliveredWriteConn{marker: first, entered: make(chan struct{}), closed: make(chan struct{}), returned: make(chan struct{})}
	var firstDial atomic.Bool
	dialer.dial = func(ctx context.Context) (net.Conn, error) {
		conn, err := dial(ctx)
		if err == nil && firstDial.CompareAndSwap(false, true) {
			held.Conn = conn
			return held, nil
		}
		return conn, err
	}
	wait := func(ch <-chan struct{}, label string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not join", label)
		}
	}
	conn, stop := clientStream(t, c, dialer)
	err := clientExchange(conn, first)
	if err != nil {
		stop()
		t.Fatal(err)
	}
	wait(held.entered, "delivered physical write")
	// The peer has echoed the payload, but the physical Write is still active.
	stop()
	wait(held.returned, "canceled physical write")
	wait(peer.done, "first peer stream")
	completed := make(chan struct{})
	conn, stop = clientStream(t, c, dialer, func(reader buf.Reader) buf.Reader {
		return &uploadedReader{Reader: reader, remaining: int32(len(second)), done: completed}
	})
	err = clientExchange(conn, second)
	if err != nil {
		stop()
		t.Fatal(err)
	}
	wait(completed, "fresh upload")
	stop()
	wait(peer.done, "fresh peer stream")
	c.Close()
	if count.Load() != 2 {
		t.Fatalf("physical connections=%d, want 2", count.Load())
	}
	if len(peer.receipts) != 2 {
		t.Fatalf("receipts=%d, want 2", len(peer.receipts))
	}
	if got := <-peer.receipts; got != string(first) {
		t.Fatalf("first receipt=%q", got)
	}
	if got := <-peer.receipts; got != string(second) {
		t.Fatalf("fresh receipt=%q", got)
	}
}
