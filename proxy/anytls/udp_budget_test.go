package anytls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

type budgetDispatcher struct {
	routing.Dispatcher
	calls  atomic.Int32
	fail   bool
	reader buf.Reader
}

type discardPackets struct{}

func (discardPackets) WriteMultiBuffer(b buf.MultiBuffer) error {
	buf.ReleaseMulti(b)
	return nil
}

func (d *budgetDispatcher) Dispatch(ctx context.Context, _ xnet.Destination) (*transport.Link, error) {
	d.calls.Add(1)
	if d.fail {
		return nil, errors.New("injected dispatch failure")
	}
	if d.reader != nil {
		return &transport.Link{Reader: d.reader, Writer: discardPackets{}}, nil
	}
	r, _ := pipe.New(pipe.OptionsFromContext(ctx)...)
	return &transport.Link{Reader: r, Writer: discardPackets{}}, nil
}

func waitBudget(t *testing.T, s *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		got := s.activeUDPLinks
		s.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("UDP links=%d want=%d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func budgetAssociation(t *testing.T, s *Server, d *budgetDispatcher) (*uot.Conn, <-chan error) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	b.SetDeadline(time.Now().Add(10 * time.Second))
	u, _ := testUser("budget", "secret").ToMemoryUser()
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{User: u})
	ctx = session.ContextWithDispatcher(ctx, d)
	done := make(chan error, 1)
	go func() {
		defer a.Close()
		done <- s.serveUDP(ctx, a)
	}()
	req := uot.Request{Destination: M.ParseSocksaddr("127.0.0.1:53")}
	if err := uot.WriteRequest(b, req); err != nil {
		t.Fatal(err)
	}
	return uot.NewConn(b, req), done
}

func awaitAssociation(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("association workers did not exit")
	}
}

func TestSharedUDPBudgetRecovery(t *testing.T) {
	s, _ := newServer(&ServerConfig{}, policy.DefaultManager{})
	defer s.Close()
	d := &budgetDispatcher{}
	for round := range 3 {
		var conns []*uot.Conn
		var done []<-chan error
		for range 2 {
			c, end := budgetAssociation(t, s, d)
			conns, done = append(conns, c), append(done, end)
			for target := range 64 {
				if err := c.WritePacket(B.As([]byte("x")), M.Socksaddr{Fqdn: fmt.Sprintf("target-%d.test", target), Port: 53}); err != nil {
					t.Fatal(err)
				}
			}
		}
		waitBudget(t, s, maxUDPLinks)
		before := d.calls.Load()
		rejected, end := budgetAssociation(t, s, d)
		_ = rejected.WritePacket(B.As([]byte("x")), M.ParseSocksaddr("extra.test:53"))
		awaitAssociation(t, end)
		rejected.Close()
		if d.calls.Load() != before {
			t.Fatal("over-budget target reached dispatcher")
		}
		waitBudget(t, s, maxUDPLinks)
		for i, c := range conns {
			c.Close()
			awaitAssociation(t, done[i])
		}
		waitBudget(t, s, 0)
		t.Logf("round %d: 128 targets admitted, overflow rejected, all credits returned", round+1)
	}
}

func TestUDPDispatchFailureReturnsCredit(t *testing.T) {
	s, _ := newServer(&ServerConfig{}, policy.DefaultManager{})
	defer s.Close()
	d := &budgetDispatcher{fail: true}
	c, done := budgetAssociation(t, s, d)
	defer c.Close()
	_ = c.WritePacket(B.As([]byte("x")), M.ParseSocksaddr("failure.test:53"))
	awaitAssociation(t, done)
	waitBudget(t, s, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.acquireUDPLink(ctx) {
		t.Fatal("canceled context acquired credit")
	}
	s.Close()
	if s.acquireUDPLink(context.Background()) {
		t.Fatal("closed server acquired credit")
	}
}

func TestBoundedAnyTLSBufferPolicy(t *testing.T) {
	for _, size := range []int32{-1, 0, 4096, 32768, 512 * 1024} {
		original := policy.Buffer{PerConnection: size}
		got := boundedBufferPolicy(original)
		want := size
		if size < 0 || size > maxPipeBuffer {
			want = maxPipeBuffer
		}
		if got.PerConnection != want || original.PerConnection != size {
			t.Fatalf("buffer policy %d -> %+v, want %d; original %+v", size, got, want, original)
		}
	}
}

type delayedUDPReader struct {
	interrupted chan struct{}
	exit        chan struct{}
	once        sync.Once
}

func (r *delayedUDPReader) Interrupt() { r.once.Do(func() { close(r.interrupted) }) }

func (r *delayedUDPReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	<-r.exit
	return nil, io.EOF
}

func TestUDPBudgetWaitsForWorker(t *testing.T) {
	s, _ := newServer(&ServerConfig{}, policy.DefaultManager{})
	defer s.Close()
	r := &delayedUDPReader{interrupted: make(chan struct{}), exit: make(chan struct{})}
	defer close(r.exit)
	c, done := budgetAssociation(t, s, &budgetDispatcher{reader: r})
	_ = c.WritePacket(B.As([]byte("x")), M.ParseSocksaddr("slow.test:53"))
	waitBudget(t, s, 1)
	c.Close()
	select {
	case <-r.interrupted:
	case <-time.After(3 * time.Second):
		t.Fatal("link was not interrupted")
	}
	waitBudget(t, s, 1)
	select {
	case <-done:
		t.Fatal("association returned while worker still running")
	default:
	}
	r.exit <- struct{}{}
	awaitAssociation(t, done)
	waitBudget(t, s, 0)
}

func TestBoundedAnyTLSPipeBackpressure(t *testing.T) {
	for _, size := range []int32{-1, 512 * 1024} {
		ctx := policy.ContextWithBufferPolicy(context.Background(), boundedBufferPolicy(policy.Buffer{PerConnection: size}))
		r, w := pipe.New(pipe.OptionsFromContext(ctx)...)
		// Pipe admits one extra write batch after reaching its threshold.
		for range maxPipeBuffer/buf.Size + 1 {
			b := buf.New()
			b.Extend(buf.Size)
			if err := w.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
				t.Fatal(err)
			}
		}
		done := make(chan error, 1)
		go func() {
			b := buf.New()
			b.Extend(buf.Size)
			done <- w.WriteMultiBuffer(buf.MultiBuffer{b})
		}()
		select {
		case err := <-done:
			t.Fatalf("slow consumer bypassed buffer cap: %v", err)
		case <-time.After(30 * time.Millisecond):
		}
		r.Interrupt()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("interrupted write succeeded")
			}
		case <-time.After(time.Second):
			t.Fatal("blocked writer did not exit")
		}
	}
}
