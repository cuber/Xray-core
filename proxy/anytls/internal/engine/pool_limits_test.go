package anytls

import (
	"bytes"
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
)

func TestPoolLimitsValidation(t *testing.T) {
	for _, opts := range []ClientOptions{
		{MaxSessions: -1}, {MaxSessions: 4097}, {MaxIdleSessions: -1}, {MaxConcurrentDials: -1},
		{MinIdleSession: -1}, {MaxSessions: 2, MaxIdleSessions: 3},
		{MaxSessions: 2, MaxConcurrentDials: 3}, {MaxIdleSessions: 1, MinIdleSession: 2},
	} {
		if opts.ValidateLimits() == nil {
			t.Fatal("invalid limits accepted", opts)
		}
	}
	opts := ClientOptions{}
	if err := opts.ValidateLimits(); err != nil {
		t.Fatal(err)
	}
	if opts.MaxSessions != 256 || opts.MaxIdleSessions != 64 || opts.MaxConcurrentDials != 64 {
		t.Fatal("unbounded defaults")
	}
}

func TestPoolConcurrentDialLimitAndClose(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 20)
	c, err := NewClient(ClientOptions{Password: "test", MaxSessions: 3, MaxConcurrentDials: 2, DialOut: func(ctx context.Context) (net.Conn, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var workers sync.WaitGroup
	results := make(chan error, 20)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := c.DialContext(context.Background(), M.Socksaddr{Fqdn: "test.invalid", Port: 80})
			results <- err
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("dial not started")
		}
	}
	for range 18 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := c.DialContext(context.Background(), M.Socksaddr{Fqdn: "test.invalid", Port: 80})
			results <- err
		}()
	}
	for range 18 {
		select {
		case err := <-results:
			if err == nil {
				t.Fatal("overlimit accepted")
			}
		case <-time.After(time.Second):
			t.Fatal("overlimit queued instead of refusing")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("dial limit bypassed", calls.Load())
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel dial owners")
	}
	workers.Wait()
	c.access.Lock()
	defer c.access.Unlock()
	if c.creating != 0 || len(c.sessions) != 0 {
		t.Fatal("close leaked capacity")
	}
}

func TestPoolDialFailureReleasesCapacity(t *testing.T) {
	var calls int
	c, _ := NewClient(ClientOptions{Password: "test", MaxSessions: 1, DialOut: func(context.Context) (net.Conn, error) { calls++; return nil, net.ErrClosed }})
	defer c.Close()
	for range 5 {
		if _, err := c.DialContext(context.Background(), M.Socksaddr{Fqdn: "test.invalid", Port: 80}); err == nil {
			t.Fatal("failure accepted")
		}
	}
	if calls != 5 || c.creating != 0 {
		t.Fatal("failed dial retained reservation")
	}
}

func TestPoolSessionAndIdleCaps(t *testing.T) {
	c, _ := NewClient(ClientOptions{Password: "test", MaxSessions: 2, MaxIdleSessions: 1, DialOut: func(context.Context) (net.Conn, error) {
		t.Error("overlimit reached dialer")
		return nil, net.ErrClosed
	}})
	defer c.Close()
	a := newClientSession(c, &finiteConn{Reader: bytes.NewReader(nil)})
	b := newClientSession(c, &finiteConn{Reader: bytes.NewReader(nil)})
	c.sessions[a], c.sessions[b] = struct{}{}, struct{}{}
	if _, err := c.DialContext(context.Background(), M.Socksaddr{Fqdn: "test.invalid", Port: 80}); err == nil {
		t.Fatal("session cap ignored")
	}
	c.releaseSession(a)
	c.releaseSession(b)
	if !b.IsClosed() || a.IsClosed() || len(c.sessions) != 1 || c.idleSessions.Len() != 1 {
		t.Fatal("idle cap failed")
	}
	stream, err := c.DialContext(context.Background(), M.Socksaddr{Fqdn: "test.invalid", Port: 80})
	if err != nil {
		t.Fatal("healthy idle reuse failed", err)
	}
	stream.Close()
	c.SetKeepIdleConnections(false)
	if len(c.sessions) != 0 || c.idleSessions.Len() != 0 {
		t.Fatal("retirement retained idle capacity")
	}
}
