package inbound

import (
	"context"
	"io"
	stdnet "net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
)

type retirementInbound struct {
	started chan context.Context
	exited  chan struct{}
	release chan struct{}
}

func (*retirementInbound) Network() []net.Network {
	return []net.Network{net.Network_TCP, net.Network_UNIX}
}
func (*retirementInbound) Close() error { return nil }
func (p *retirementInbound) Process(ctx context.Context, _ net.Network, conn stat.Connection, _ routing.Dispatcher) error {
	p.started <- ctx
	// Deliberately ignores ctx: shutdown must close the accepted socket too.
	io.Copy(io.Discard, conn)
	<-p.release
	close(p.exited)
	return nil
}

// Non-comparable dynamic values are valid net.Conn implementations.
type nonComparableConnection struct {
	net.Conn
	padding []byte
}

func (nonComparableConnection) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.LocalHostIP.IP(), Port: 1234}
}

func (nonComparableConnection) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.LocalHostIP.IP(), Port: 5678}
}

func TestStreamWorkerCloseJoinsAcceptedCallbacks(t *testing.T) {
	for _, unix := range []bool{false, true} {
		name := "tcp"
		if unix {
			name = "unix"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := &retirementInbound{started: make(chan context.Context, 1), exited: make(chan struct{}), release: make(chan struct{})}
				type contextKey struct{}
				ctx := context.WithValue(context.Background(), contextKey{}, "request-owner")
				w := &tcpWorker{ctx: ctx, address: net.LocalHostIP, proxy: p}
				d := &dsWorker{ctx: ctx, address: net.DomainAddress("/test.sock"), proxy: p}
				callback, closeWorker := w.callback, w.Close
				if unix {
					callback, closeWorker = d.callback, d.Close
				}
				server, client := stdnet.Pipe()
				defer client.Close()
				callback(nonComparableConnection{Conn: server})
				request := <-p.started
				if request.Value(contextKey{}) != "request-owner" {
					t.Fatal("lost request context")
				}
				closed := make(chan struct{})
				go func() { closeWorker(); close(closed) }()
				synctest.Wait()
				closedAgain := make(chan struct{})
				go func() { closeWorker(); close(closedAgain) }()
				synctest.Wait()
				select {
				case <-closedAgain:
					t.Fatal("concurrent Close returned before callbacks retired")
				default:
				}
				if request.Err() == nil {
					t.Fatal("accepted request not canceled")
				}
				if _, err := client.Write([]byte("after-close")); err == nil {
					t.Fatal("accepted socket remains writable")
				}
				select {
				case <-closed:
					t.Fatal("Close did not wait for Process")
				default:
				}
				close(p.release)
				<-closed
				<-closedAgain
				select {
				case <-p.exited:
				default:
					t.Fatal("callback not retired")
				}
				late, peer := stdnet.Pipe()
				defer peer.Close()
				callback(nonComparableConnection{Conn: late})
				if _, err := peer.Write([]byte("late")); err == nil {
					t.Fatal("late admission accepted")
				}
				if err := closeWorker(); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

type retirementListener struct {
	closed chan struct{}
	addr   net.Addr
}

func (l *retirementListener) Addr() net.Addr {
	if l.addr != nil {
		return l.addr
	}
	return &net.TCPAddr{}
}
func (l *retirementListener) Close() error { close(l.closed); return nil }

func TestStreamWorkerCloseDuringListenPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s streamOwnership
		if !s.beginStart() {
			t.Fatal("start rejected")
		}
		closed := make(chan struct{})
		go func() { s.close(); close(closed) }()
		synctest.Wait()
		select {
		case <-closed:
			t.Fatal("Close did not wait for Listen")
		default:
		}
		hub := &retirementListener{closed: make(chan struct{})}
		if s.publish(hub) {
			t.Fatal("closed worker published late listener")
		}
		hub.Close()
		s.finishStart()
		<-closed
		if s.beginStart() {
			t.Fatal("closed worker restarted")
		}
	})
}

func TestStreamWorkerRejectsDuplicateStart(t *testing.T) {
	var s streamOwnership
	if !s.beginStart() {
		t.Fatal("first Start rejected")
	}
	if s.beginStart() {
		t.Fatal("concurrent Start accepted")
	}
	hub := &retirementListener{closed: make(chan struct{})}
	if !s.publish(hub) {
		t.Fatal("first listener rejected")
	}
	s.finishStart()
	if s.beginStart() {
		t.Fatal("repeated Start accepted")
	}
	s.close()
	select {
	case <-hub.closed:
	default:
		t.Fatal("listener leaked")
	}
}

func TestStreamWorkerTransparentLoopbackUsesListener(t *testing.T) {
	for _, mode := range []internet.SocketConfig_TProxyMode{internet.SocketConfig_Redirect, internet.SocketConfig_TProxy} {
		t.Run(mode.String(), func(t *testing.T) {
			w := &tcpWorker{address: net.LocalHostIP, port: 1080, stream: &internet.MemoryStreamConfig{SocketSettings: &internet.SocketConfig{Tproxy: mode}}}
			w.ownership.beginStart()
			w.ownership.publish(&retirementListener{closed: make(chan struct{}), addr: &net.TCPAddr{IP: net.LocalHostIP.IP(), Port: 1080}})
			w.ownership.finishStart()
			defer w.Close()
			// Both original-destination mechanisms feed the same loopback predicate.
			if w.isLoopback(context.Background(), net.TCPDestination(net.LocalHostIP, 1234)) {
				t.Fatal("original destination treated as listener")
			}
			if !w.isLoopback(context.Background(), net.TCPDestination(net.LocalHostIP, 1080)) {
				t.Fatal("self-loop accepted")
			}
		})
	}
	synctest.Test(t, func(t *testing.T) {
		p := &retirementInbound{started: make(chan context.Context, 1), exited: make(chan struct{}), release: make(chan struct{})}
		w := &tcpWorker{ctx: context.Background(), address: net.LocalHostIP, port: 1080, proxy: p, recvOrigDest: true, stream: &internet.MemoryStreamConfig{SocketSettings: &internet.SocketConfig{Tproxy: internet.SocketConfig_TProxy}}}
		w.ownership.beginStart()
		w.ownership.publish(&retirementListener{closed: make(chan struct{}), addr: &net.TCPAddr{IP: net.LocalHostIP.IP(), Port: 1080}})
		w.ownership.finishStart()
		conn, peer := stdnet.Pipe()
		defer peer.Close()
		w.callback(nonComparableConnection{Conn: conn})
		synctest.Wait()
		select {
		case ctx := <-p.started:
			if target := session.OutboundsFromContext(ctx)[0].Target; target.Port != 1234 {
				t.Fatalf("lost TPROXY target: %v", target)
			}
		default:
			t.Fatal("TPROXY accepted LocalAddr incorrectly rejected as self-loop")
		}
		close(p.release)
		w.Close()
	})
}

func TestStreamWorkerConcurrentAdmissionAndClose(t *testing.T) {
	for round := 0; round < 100; round++ {
		var s streamOwnership
		var wg sync.WaitGroup
		for n := 0; n < 16; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				conn, peer := stdnet.Pipe()
				defer peer.Close()
				ctx, entry := s.accept(context.Background(), nonComparableConnection{Conn: conn})
				if entry != nil {
					<-ctx.Done()
					s.retire(entry)
				}
			}()
		}
		s.close()
		wg.Wait()
	}
}

type sharedRetirementInbound struct {
	started chan context.Context
	release chan struct{}
	closes  atomic.Int32
	exits   atomic.Int32
}

func (*sharedRetirementInbound) Network() []net.Network {
	return []net.Network{net.Network_TCP, net.Network_UNIX, net.Network_UDP}
}

func (p *sharedRetirementInbound) Close() error {
	if p.closes.Add(1) == 1 {
		close(p.release)
	}
	return nil
}

func (p *sharedRetirementInbound) Process(ctx context.Context, _ net.Network, conn stat.Connection, _ routing.Dispatcher) error {
	p.started <- ctx
	io.Copy(io.Discard, conn)
	// Model protocol-owned sessions which need proxy.Close as well as conn.Close.
	<-p.release
	p.exits.Add(1)
	return nil
}

func TestStreamMultiWorkerHandlerOwnsProxyClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &sharedRetirementInbound{started: make(chan context.Context, 2), release: make(chan struct{})}
		tcp := &tcpWorker{ctx: context.Background(), address: net.LocalHostIP, proxy: p}
		unix := &dsWorker{ctx: context.Background(), address: net.DomainAddress("/shared.sock"), proxy: p}
		udp := &udpWorker{ctx: context.Background(), proxy: p}
		unstarted := &tcpWorker{ctx: context.Background(), proxy: p}
		h := &AlwaysOnInboundHandler{proxy: p, workers: []worker{tcp, unix, udp, unstarted}}
		c1, p1 := stdnet.Pipe()
		defer p1.Close()
		c2, p2 := stdnet.Pipe()
		defer p2.Close()
		tcp.callback(nonComparableConnection{Conn: c1})
		unix.callback(nonComparableConnection{Conn: c2})
		ctx1, ctx2 := <-p.started, <-p.started
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); h.Close() }()
		}
		wg.Wait()
		if p.closes.Load() != 1 {
			t.Fatalf("shared proxy closed %d times", p.closes.Load())
		}
		if p.exits.Load() != 2 {
			t.Fatal("handler returned before Process exit")
		}
		if ctx1.Err() == nil || ctx2.Err() == nil {
			t.Fatal("requests not canceled")
		}
		// Neither individually closing a worker nor repeated handler Close owns
		// another reference to the shared protocol.
		for _, w := range h.workers {
			w.Close()
		}
		h.Close()
		if p.closes.Load() != 1 {
			t.Fatal("worker closed shared proxy")
		}
	})
}
