package inbound

import (
	"context"
	"io"
	stdnet "net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/mux"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet/hysteria"
	"github.com/xtls/xray-core/transport/internet/stat"
)

// Core spec026 R1, R3: the stream worker's registry, user binding and closing.

var carrierDest = net.TCPDestination(net.DomainAddress("carrier.test"), 1)

// fakeDispatcher records dispatches and plays an inbound's mux.Server for carrierDest.
type fakeDispatcher struct {
	dispatched atomic.Int64
}

func (*fakeDispatcher) Type() interface{} { return routing.DispatcherType() }
func (*fakeDispatcher) Start() error      { return nil }
func (*fakeDispatcher) Close() error      { return nil }
func (d *fakeDispatcher) Dispatch(context.Context, net.Destination) (*transport.Link, error) {
	d.dispatched.Add(1)
	return nil, nil
}
func (d *fakeDispatcher) DispatchLink(context.Context, net.Destination, *transport.Link) error {
	d.dispatched.Add(1)
	return nil
}
func (*fakeDispatcher) Carrier(dest net.Destination) mux.Carrier {
	if dest == carrierDest {
		return mux.SingMuxCarrier
	}
	return mux.NoCarrier
}

// userConn tells userProxy which user it authenticates as and where it dispatches.
type userConn struct {
	stdnet.Conn
	email   string
	carrier bool
}

func (*userConn) LocalAddr() net.Addr  { return &net.TCPAddr{IP: net.LocalHostIP.IP(), Port: 1234} }
func (*userConn) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.LocalHostIP.IP(), Port: 5678} }

// userProxy authenticates a userConn as its email, dispatches once, then serves until the
// connection closes; an empty email never dispatches.
type userProxy struct {
	started chan struct{}
}

func (*userProxy) Network() []net.Network { return []net.Network{net.Network_TCP} }
func (*userProxy) Close() error           { return nil }
func (p *userProxy) Process(ctx context.Context, _ net.Network, conn stat.Connection, d routing.Dispatcher) error {
	c := conn.(*userConn)
	if c.email != "" {
		session.InboundFromContext(ctx).User = &protocol.MemoryUser{Email: c.email}
		dest := net.TCPDestination(net.DomainAddress("example.test"), 443)
		if c.carrier {
			dest = carrierDest
		}
		d.Dispatch(ctx, dest)
	}
	if p.started != nil {
		p.started <- struct{}{}
	}
	io.Copy(io.Discard, conn)
	return nil
}

type registryFixture struct {
	worker  *tcpWorker
	handler *AlwaysOnInboundHandler
	proxy   *userProxy
	clients []stdnet.Conn
	mu      sync.Mutex
}

func newRegistryFixture(t *testing.T) *registryFixture {
	p := &userProxy{started: make(chan struct{}, 1024)}
	w := &tcpWorker{ctx: context.Background(), address: net.LocalHostIP, proxy: p, tag: "in", dispatcher: &fakeDispatcher{}}
	f := &registryFixture{worker: w, handler: &AlwaysOnInboundHandler{workers: []worker{w}, proxy: p, tag: "in"}, proxy: p}
	t.Cleanup(func() {
		f.mu.Lock()
		for _, c := range f.clients {
			c.Close()
		}
		f.mu.Unlock()
		w.Close()
	})
	return f
}

// open accepts a connection for email and waits until the proxy has dispatched it.
func (f *registryFixture) open(t *testing.T, email string, carrier bool) stdnet.Conn {
	server, client := stdnet.Pipe()
	f.mu.Lock()
	f.clients = append(f.clients, client)
	f.mu.Unlock()
	f.worker.callback(&userConn{Conn: server, email: email, carrier: carrier})
	select {
	case <-f.proxy.started:
	case <-time.After(5 * time.Second):
		t.Fatal("connection not processed")
	}
	return client
}

func only(emails ...string) func(string) bool {
	return func(email string) bool {
		for _, e := range emails {
			if strings.EqualFold(e, email) {
				return true
			}
		}
		return false
	}
}

// waitClosed reads from a client until its server end closes.
func waitClosed(t *testing.T, client stdnet.Conn) {
	t.Helper()
	client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil || strings.Contains(err.Error(), "timeout") {
		t.Fatalf("connection not closed: %v", err)
	}
}

func TestBindAtFirstDispatch(t *testing.T) {
	f := newRegistryFixture(t)
	f.open(t, "Alice@test", false)
	f.open(t, "alice@test", false)
	f.open(t, "bob@test", true)
	f.open(t, "", false)

	r := f.handler.Connections()
	if r.Counts.TCP != 4 || r.Counts.SingMuxCarriers != 1 || r.Unbound != 1 || r.Closing != 0 {
		t.Fatalf("counts %+v unbound %d closing %d", r.Counts, r.Unbound, r.Closing)
	}
	if len(r.Users) != 2 || r.Users["alice@test"].TCP != 2 || r.Users["alice@test"].Email != "Alice@test" && r.Users["alice@test"].Email != "alice@test" {
		t.Fatalf("users matched case-insensitively: %+v", r.Users)
	}
	if bob := r.Users["bob@test"]; bob.TCP != 1 || bob.SingMuxCarriers != 1 {
		t.Fatalf("bob %+v", bob)
	}

	// a later dispatch with another user does not rebind
	var e acceptedConnection
	e.bind("first@test")
	e.bind("second@test")
	if e.user() != "first@test" {
		t.Fatalf("rebound to %s", e.user())
	}
}

func TestRegistryKinds(t *testing.T) {
	server, client := stdnet.Pipe()
	defer server.Close()
	defer client.Close()
	if kindOf(server) != connTCP || kindOf(&hysteria.InterConn{}) != connHy2UDP {
		t.Fatal("kinds")
	}
	// a connection that carries its user is bound at accept
	var s streamOwnership
	_, entry := s.accept(context.Background(), userAt{Conn: server, email: "hy2@test"})
	if entry.user() != "hy2@test" {
		t.Fatalf("bound %q at accept", entry.user())
	}
	s.retire(entry)
}

type userAt struct {
	stdnet.Conn
	email string
}

func (u userAt) User() *protocol.MemoryUser { return &protocol.MemoryUser{Email: u.email} }

func TestCloseConnectionsSelectsOnlyBoundUsers(t *testing.T) {
	f := newRegistryFixture(t)
	alice := []stdnet.Conn{f.open(t, "alice@test", false), f.open(t, "alice@test", true)}
	bob := f.open(t, "bob@test", false)
	unbound := f.open(t, "", false)

	closed, pending := f.handler.CloseConnections(only("ALICE@test"), time.Second)
	if closed.TCP != 2 || closed.SingMuxCarriers != 1 || pending != 0 {
		t.Fatalf("closed %+v pending %d", closed, pending)
	}
	for _, c := range alice {
		waitClosed(t, c)
	}
	again, _ := f.handler.CloseConnections(only("alice@test"), time.Second)
	if again != (ConnectionCounts{}) {
		t.Fatalf("closing is idempotent, got %+v", again)
	}

	// every user, except the excluded, never the unbound
	all, _ := f.handler.CloseConnections(func(email string) bool { return !strings.EqualFold(email, "bob@test") }, time.Second)
	if all.TCP != 0 {
		t.Fatalf("bob excluded, the unbound left: %+v", all)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := f.handler.Connections()
		if r.Counts.TCP == 2 && r.Unbound == 1 && r.Users["alice@test"] == nil && r.Users["bob@test"].TCP == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after closing alice: %+v unbound %d users %v", r.Counts, r.Unbound, r.Users)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, c := range []stdnet.Conn{bob, unbound} {
		c.SetWriteDeadline(time.Now().Add(time.Second))
		if _, err := c.Write([]byte("x")); err != nil {
			t.Fatalf("other connections stay open: %v", err)
		}
	}
}

// blockingClose holds Close until released, as a TLS close-notify can on a full send buffer.
type blockingClose struct {
	stdnet.Conn
	release chan struct{}
}

func (b *blockingClose) Close() error {
	<-b.release
	return b.Conn.Close()
}

func TestCloseBoundedReportsPending(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	start := time.Now()
	pending := closeBounded([]func(){func() {}, func() { <-release }}, 200*time.Millisecond)
	if pending != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("pending %d after %s", pending, time.Since(start))
	}

	// a claimed entry whose close is still running is reported apart, not as live, and claimed once
	w := &tcpWorker{}
	h := &AlwaysOnInboundHandler{workers: []worker{w}}
	server, client := stdnet.Pipe()
	defer client.Close()
	conn := &blockingClose{Conn: server, release: make(chan struct{})}
	_, entry := w.ownership.accept(context.Background(), conn)
	entry.bind("slow@test")
	closed, pending := h.CloseConnections(only("slow@test"), 100*time.Millisecond)
	if closed.TCP != 1 || pending != 1 {
		t.Fatalf("closed %+v pending %d", closed, pending)
	}
	if r := h.Connections(); r.Counts.TCP != 0 || r.Closing != 1 {
		t.Fatalf("closing apart: %+v closing %d", r.Counts, r.Closing)
	}
	if again, _ := h.CloseConnections(only("slow@test"), 100*time.Millisecond); again.TCP != 0 {
		t.Fatal("an entry is claimed once")
	}
	close(conn.release)
	w.ownership.retire(entry)
}

// FR-007: accepts, natural ends, overlapping closures, reports and the worker's own close race
// without deadlock, panic or an entry claimed twice. Run with -race.
func TestCloseRaces(t *testing.T) {
	f := newRegistryFixture(t)
	f.proxy.started = nil
	users := []string{"u0@test", "u1@test", "u2@test", "u3@test", ""}
	var accepted, claimed atomic.Int64
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				server, client := stdnet.Pipe()
				f.worker.callback(&userConn{Conn: server, email: users[(i+n)%len(users)], carrier: n%3 == 0})
				accepted.Add(1)
				if n%2 == 0 {
					client.Close() // a natural end
				} else {
					f.mu.Lock()
					f.clients = append(f.clients, client)
					f.mu.Unlock()
				}
			}
		}()
	}
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				match := only(users[(i+n)%4])
				if n%5 == 0 {
					match = func(string) bool { return true }
				}
				closed, _ := f.handler.CloseConnections(match, time.Second)
				claimed.Add(int64(closed.TCP))
				f.handler.Connections()
			}
		}()
	}
	time.Sleep(2 * time.Second)
	close(stop)
	wg.Wait()

	done := make(chan struct{})
	go func() {
		f.worker.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker close deadlocked")
	}
	if claimed.Load() > accepted.Load() {
		t.Fatalf("claimed %d of %d accepted", claimed.Load(), accepted.Load())
	}
	if r := f.handler.Connections(); r.Counts != (ConnectionCounts{}) || r.Closing != 0 || r.Unbound != 0 {
		t.Fatalf("registry empty after close, got %+v", r)
	}
	t.Logf("accepted %d, claimed %d", accepted.Load(), claimed.Load())
}
