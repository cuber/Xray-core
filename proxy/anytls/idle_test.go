package anytls

import (
	"context"
	"crypto/sha256"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/features/policy"
)

func idleFixture(t *testing.T) (*Server, *connection, context.Context) {
	t.Helper()
	s, err := newServer(&ServerConfig{}, policy.DefaultManager{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	u, _ := testUser("idle", "secret").ToMemoryUser()
	if err := s.AddUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a, b := net.Pipe()
	c := &connection{Conn: a, ctx: ctx, cancel: cancel}
	t.Cleanup(func() { c.close(); b.Close() })
	ctx = context.WithValue(ctx, connectionKey{}, c)
	hash := sha256.Sum256([]byte("secret"))
	if _, ok := s.authenticate(ctx, hash[:]); !ok {
		t.Fatal("authentication failed")
	}
	return s, c, ctx
}

func TestIdleObservations(t *testing.T) {
	s, c, ctx := idleFixture(t)
	previous := idleSnapshot{}
	if !s.openStream(ctx) {
		t.Fatal("open")
	}
	if s.retireIfIdle(c, &previous) {
		t.Fatal("retired active stream")
	}
	s.closeStream(ctx)
	if s.retireIfIdle(c, &previous) {
		t.Fatal("retired without a full idle observation")
	}
	// A complete short request between checks must also prevent retirement.
	if !s.openStream(ctx) {
		t.Fatal("reopen")
	}
	s.closeStream(ctx)
	if s.retireIfIdle(c, &previous) {
		t.Fatal("missed intervening request")
	}
	// Heartbeat I/O refreshes the transport deadline, not the business counter.
	c.timeout.Store(int64(300 * time.Second))
	c.touch()
	if !s.retireIfIdle(c, &previous) {
		t.Fatal("heartbeat kept empty session alive")
	}
	if s.openStream(ctx) {
		t.Fatal("stream admitted after retirement")
	}
	if c.user.sessions != 1 {
		t.Fatal("session credit released before teardown")
	}
}

func TestIdleAdmissionRace(t *testing.T) {
	for range 100 {
		s, c, ctx := idleFixture(t)
		var opened, retired bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); opened = s.openStream(ctx) }()
		go func() { defer wg.Done(); previous := idleSnapshot{}; retired = s.retireIfIdle(c, &previous) }()
		wg.Wait()
		if opened == retired {
			t.Fatalf("open=%v retired=%v", opened, retired)
		}
		if opened {
			s.closeStream(ctx)
		}
	}
}

func TestIdleMonitorLifecycle(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		s, c, _ := idleFixture(t)
		ticks := make(chan time.Time)
		done := make(chan struct{})
		go func() { defer close(done); s.monitorIdle(c, ticks) }()
		if cancelFirst {
			c.cancel()
		} else {
			ticks <- time.Now()
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("monitor leaked")
		}
		if c.ctx.Err() == nil {
			t.Fatal("idle connection not canceled")
		}
	}
}

func TestDefaultUserSessionsExceedSixteen(t *testing.T) {
	s, _, ctx := idleFixture(t)
	hash := sha256.Sum256([]byte("secret"))
	for range 64 {
		c := &connection{ctx: ctx}
		if _, ok := s.authenticate(context.WithValue(ctx, connectionKey{}, c), hash[:]); !ok {
			t.Fatal("default per-user session limit rejected valid connection")
		}
	}
}
