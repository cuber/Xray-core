package anytls

import (
	"bytes"
	"testing"
	"time"
)

func TestCompletedWriteDoesNotAbortClosedStreamSession(t *testing.T) {
	for i := range 100 {
		c, err := NewClient(ClientOptions{Password: "test", CancelableWrites: true})
		if err != nil {
			t.Fatal(err)
		}
		s := newClientSession(c, &finiteConn{Reader: bytes.NewReader(nil)})
		stream := newStream(1, s)
		stop := s.watchWrite(stream, &stream.writeDeadline)
		stop()
		close(stream.done)
		closed := s.IsClosed()
		s.Close()
		c.Close()
		if closed {
			t.Fatalf("completed write closed physical session at iteration %d", i)
		}
	}
}

func TestCanceledWriteAbortsSession(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		c, err := NewClient(ClientOptions{Password: "test", CancelableWrites: true})
		if err != nil {
			t.Fatal(err)
		}
		s := newClientSession(c, &finiteConn{Reader: bytes.NewReader(nil)})
		stream := newStream(1, s)
		stop := s.watchWrite(stream, &stream.writeDeadline)
		if deadline {
			stream.writeDeadline.Set(time.Now().Add(-time.Second))
		} else {
			close(stream.done)
		}
		select {
		case <-s.done:
		case <-time.After(time.Second):
			t.Error("canceled write did not close its physical session")
		}
		stop()
		s.Close()
		c.Close()
	}
}
