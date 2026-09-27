package anytls

import (
	"bytes"
	"net"
	"os"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
)

func TestClientDefaultIdleTimer(t *testing.T) {
	if os.Getenv("ANYTLS_STRICT") != "1" {
		t.Skip("set ANYTLS_STRICT=1 for real-time 30s idle acceptance")
	}
	c, err := NewClient(ClientOptions{Password: "test", CancelableWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := newClientSession(c, &finiteConn{Reader: bytes.NewReader(nil)})
	c.sessions[s] = struct{}{}
	started := time.Now()
	c.releaseSession(s)
	select {
	case <-s.done:
		if time.Since(started) < 30*time.Second {
			t.Fatal("default idle timeout fired early")
		}
	case <-time.After(65 * time.Second):
		t.Fatal("default 30s check/30s timeout did not close idle session")
	}
}

type noWriteDeadlineConn struct{ net.Conn }

func (c noWriteDeadlineConn) SetWriteDeadline(time.Time) error { return nil }
func (c noWriteDeadlineConn) SetDeadline(time.Time) error      { return nil }

func TestClientControlFrameTimeoutWithoutTransportDeadline(t *testing.T) {
	c, err := NewClient(ClientOptions{Password: "test", CancelableWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	local, remote := net.Pipe()
	defer remote.Close()
	s := newClientSession(c, noWriteDeadlineConn{local})
	c.sessions[s] = struct{}{}
	done := make(chan error, 1)
	go func() { done <- s.writeFrame(commandFIN, 1, nil) }()
	select {
	case err := <-done:
		if err == nil || !s.IsClosed() {
			t.Fatal("blocked control write did not retire broken transport")
		}
	case <-time.After(controlFrameWriteTimeout + 2*time.Second):
		s.Close()
		<-done
		t.Fatal("control frame ignored timeout on dispatch-backed transport")
	}
	if c.takeIdleSession() != nil {
		t.Fatal("broken session was reusable")
	}
}

func TestClientReservedSessionCannotReturnToIdle(t *testing.T) {
	c, err := NewClient(ClientOptions{Password: "test", CancelableWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := newClientSession(c, &finiteConn{Reader: bytes.NewReader(nil)})
	c.sessions[s] = struct{}{}
	c.releaseSession(s)
	if c.takeIdleSession() != s {
		t.Fatal("idle session not selected")
	}
	// A delayed FIN write from the previous flow finishes in the take/open gap.
	c.releaseSession(s)
	if next := c.takeIdleSession(); next != nil {
		t.Fatal("reserved session handed to a second request")
	}
	stream, err := s.openStream(M.Socksaddr{Fqdn: "test.invalid", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	c.finishReservation(s)
	c.releaseSession(s)
	if c.takeIdleSession() != nil {
		t.Fatal("active session handed to another request")
	}
	stream.Close()
	if c.takeIdleSession() != s {
		t.Fatal("completed session not reusable")
	}
}

func TestClientQueuedControlCannotReturnToIdle(t *testing.T) {
	c, err := NewClient(ClientOptions{Password: "test", CancelableWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := newClientSession(c, &finiteConn{Reader: bytes.NewReader(nil)})
	c.sessions[s] = struct{}{}
	c.beginControl(s)
	c.beginControl(s)
	c.releaseSession(s)
	if c.takeIdleSession() != nil {
		t.Fatal("queued FIN published idle session")
	}
	c.endControl(s)
	if c.takeIdleSession() != nil {
		t.Fatal("one completion released another control's ownership")
	}
	c.endControl(s)
	if c.takeIdleSession() != s {
		t.Fatal("completed controls did not release idle session")
	}
}

func TestClientDisableReuseCloseWithActiveStream(t *testing.T) {
	c, err := NewClient(ClientOptions{Password: "test", CancelableWrites: true, DisableReuse: true})
	if err != nil {
		t.Fatal(err)
	}
	s := newClientSession(c, &finiteConn{Reader: bytes.NewReader(nil)})
	c.sessions[s] = struct{}{}
	if _, err := s.openStream(M.Socksaddr{Fqdn: "test.invalid", Port: 80}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close recursively entered session shutdown")
	}
}

func TestClientIdleEvictionAndMinimum(t *testing.T) {
	for _, minimum := range []int{0, 1, 3} {
		c, err := NewClient(ClientOptions{Password: "test", MinIdleSession: minimum, IdleSessionCheckInterval: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		if len(c.sessions) != 0 {
			t.Fatal("min idle pre-dialed sessions")
		}
		var idle []*session
		for range 3 {
			s := newClientSession(c, &finiteConn{Reader: bytes.NewReader(nil)})
			c.sessions[s] = struct{}{}
			c.releaseSession(s)
			s.idleSince = time.Now().Add(-time.Hour)
			idle = append(idle, s)
		}
		active := newClientSession(c, &finiteConn{Reader: bytes.NewReader(nil)})
		c.sessions[active] = struct{}{}
		stream, err := active.openStream(M.Socksaddr{Fqdn: "test.invalid", Port: 80})
		if err != nil {
			t.Fatal(err)
		}
		c.releaseSession(active)
		if active.element != nil {
			t.Fatal("active session entered idle list")
		}
		c.cleanupIdleSessions()
		if c.idleSessions.Len() != minimum || active.IsClosed() {
			t.Fatalf("min=%d idle=%d activeClosed=%v", minimum, c.idleSessions.Len(), active.IsClosed())
		}
		closed := 0
		for _, s := range idle {
			if s.IsClosed() {
				closed++
			}
		}
		if closed != 3-minimum {
			t.Fatal("eviction count", closed)
		}
		c.SetKeepIdleConnections(false)
		if c.idleSessions.Len() != 0 || active.IsClosed() {
			t.Fatal("retirement idle cleanup affected active session")
		}
		stream.Close()
		if !active.IsClosed() {
			t.Fatal("retirement retained returned session")
		}
		if err := c.Close(); err != nil && err != net.ErrClosed {
			t.Fatal(err)
		}
		if len(c.sessions) != 0 || c.idleTimer != nil {
			t.Fatal("closed pool retained state")
		}
	}
}
