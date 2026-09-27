package anytls

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"
)

type peerIdleObservedConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (c *peerIdleObservedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })
	return err
}

// This is a controlled peer-expiry stimulus, not a substitute for the native
// 30s timer tests. The peer is closed only after exact idle observations; the
// first new business request after the client's close observation must succeed
// without retry. Real TCP destinations independently record every business ID.
func TestClientPeerIdleExpiryFreshRequest(t *testing.T) {
	payloads := make([][]byte, 3)
	for id, size := range []int{32, 8192, 65536} {
		payloads[id] = make([]byte, size)
		for i := range payloads[id] {
			payloads[id][i] = byte(i*31 + id)
		}
		binary.BigEndian.PutUint32(payloads[id], uint32(id))
	}
	f := newConcurrencyFixture(t, payloads)
	firstClosed := make(chan struct{})
	dial := f.dialer.dial
	f.dialer.dial = func(ctx context.Context) (net.Conn, error) {
		conn, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		if f.dials.Load() == 1 {
			return &peerIdleObservedConn{Conn: conn, closed: firstClosed}, nil
		}
		return conn, nil
	}
	waitCounts := func(label string, logical, ended int64) {
		t.Helper()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			got := [4]int64{f.logicalClosed.Load(), f.destinationsClosed.Load(), f.sessionsClosed.Load(), f.logical.Load()}
			if got == [4]int64{logical, logical, ended, logical} {
				return
			}
			select {
			case <-tick.C:
			case <-deadline.C:
				t.Fatalf("%s logicalClosed/destinationClosed/sessionClosed/logical=%v", label, got)
			}
		}
	}
	for _, payload := range payloads[:2] {
		if err := f.exchange(payload); err != nil {
			t.Fatal(err)
		}
	}
	waitCounts("healthy idle", 2, 0)
	if got := [4]int64{f.dials.Load(), f.physical.Load(), f.sessions.Load(), f.destinations.Load()}; got != [4]int64{1, 1, 1, 2} {
		t.Fatalf("before expiry dials/accepts/sessions/destinations=%v", got)
	}
	select {
	case <-firstClosed:
		t.Fatal("client closed healthy session before peer expiry")
	default:
	}

	// Select only the peer-side physical AnyTLS socket, never a destination or
	// client-side connection. No handler removal or client pool reset is used.
	var peer net.Conn
	count := 0
	f.mu.Lock()
	for conn := range f.sockets {
		if conn.LocalAddr().String() == f.listener.Addr().String() {
			peer = conn
			count++
		}
	}
	f.mu.Unlock()
	if count != 1 {
		t.Fatalf("idle peer physical sockets=%d want=1", count)
	}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("client did not observe peer idle expiry")
	}
	waitCounts("expired peer", 2, 1)
	if f.dials.Load() != 1 {
		t.Fatal("peer expiry caused an unsolicited reconnect")
	}
	// Exactly one call with a fresh ID: a failed request is not retried.
	if err := f.exchange(payloads[2]); err != nil {
		t.Fatalf("first fresh request after peer idle expiry: %v", err)
	}
	waitCounts("fresh request", 3, 1)
	if got := [4]int64{f.dials.Load(), f.physical.Load(), f.sessions.Load(), f.destinations.Load()}; got != [4]int64{2, 2, 2, 3} {
		t.Fatalf("after expiry dials/accepts/sessions/destinations=%v", got)
	}
	f.stop()
	if got := [4]int64{f.logicalClosed.Load(), f.destinationsClosed.Load(), f.sessionsClosed.Load(), f.logical.Load()}; got != [4]int64{3, 3, 2, 3} {
		t.Fatalf("joined logicalClosed/destinationClosed/sessionClosed/logical=%v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sockets) != 0 || len(f.receipts) != 3 {
		t.Fatalf("after join owned sockets=%d receipt IDs=%d", len(f.sockets), len(f.receipts))
	}
	for id := range payloads {
		if f.receipts[uint32(id)] != 1 {
			t.Errorf("business ID=%d receipts=%d want=1", id, f.receipts[uint32(id)])
		}
	}
	t.Log("peer idle expiry observed before first fresh request; physical dials/accepts/sessions/closed=2/2/2/2; logical/destinations=3/3; IDs 0,1,2 received exactly once; all fixture workers and sockets joined")
}
