package anytls

import (
	"bytes"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type deadlineAuditConn struct {
	net.Conn
	deadlines atomic.Int32
}

func (c *deadlineAuditConn) SetWriteDeadline(time.Time) error {
	c.deadlines.Add(1)
	return nil
}

func TestControlWriteDoesNotChangeTransportDeadline(t *testing.T) {
	c := &deadlineAuditConn{Conn: &finiteConn{Reader: bytes.NewReader(nil)}}
	s := &session{conn: c, writeAccess: make(chan struct{}, 1), done: make(chan struct{}), streams: make(map[uint32]*stream)}
	defer s.Close()
	if err := s.writeFrame(commandHeartResponse, 0, nil); err != nil {
		t.Fatal(err)
	}
	if c.deadlines.Load() != 0 {
		t.Fatal("control frame changed a shared connection deadline")
	}
}

func TestServerQueuedControlWriteTimeout(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	s := &session{conn: noWriteDeadlineConn{a}, writeAccess: make(chan struct{}, 1), done: make(chan struct{}), streams: make(map[uint32]*stream)}
	defer s.Close()
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- s.writeFrame(commandHeartResponse, 1, nil) }()
	until := time.Now().Add(time.Second)
	for len(s.writeAccess) == 0 {
		if time.Now().After(until) {
			t.Fatal("first writer did not acquire lock")
		}
		time.Sleep(time.Millisecond)
	}
	go func() { second <- s.writeFrame(commandSYNACK, 2, nil) }()
	var header [frameOverhead]byte
	if _, err := io.ReadFull(b, header[:]); err != nil {
		t.Fatal(err)
	}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-second:
		if err == nil || !s.IsClosed() {
			t.Fatal("blocked write did not retire session")
		}
	case <-time.After(controlFrameWriteTimeout + time.Second):
		b.Close()
		<-second
		t.Fatal("queued control frame ignored timeout")
	}
}

func TestServerStreamWriteDeadlineAndClose(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "close"}[cancel], func(t *testing.T) {
			a, b := net.Pipe()
			defer b.Close()
			s := &session{conn: noWriteDeadlineConn{a}, writeAccess: make(chan struct{}, 1), done: make(chan struct{}), streams: make(map[uint32]*stream)}
			defer s.Close()
			st := newStream(1, s)
			st.handshakeDone.Store(true)
			s.streams[1] = st
			done := make(chan error, 1)
			go func() { _, err := st.Write([]byte("bounded-write")); done <- err }()
			// Consume one byte so cancellation happens during a partial frame.
			var one [1]byte
			if _, err := io.ReadFull(b, one[:]); err != nil {
				t.Fatal(err)
			}
			closed := make(chan struct{})
			if cancel {
				go func() { st.Close(); close(closed) }()
			} else {
				st.SetWriteDeadline(time.Now().Add(20 * time.Millisecond))
				close(closed)
			}
			select {
			case err := <-done:
				if err == nil || !s.IsClosed() {
					t.Fatal("partial frame remained reusable")
				}
			case <-time.After(time.Second):
				b.Close()
				<-done
				t.Fatal("blocked stream ignored cancellation")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("close did not finish")
			}
		})
	}
}
