package anytls

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet/stat"
	"github.com/xtls/xray-core/transport/pipe"
)

type clientTestDialer struct {
	dial func(context.Context) (net.Conn, error)
}

func (d clientTestDialer) Dial(ctx context.Context, _ xnet.Destination) (stat.Connection, error) {
	return d.dial(ctx)
}
func (clientTestDialer) DestIpAddress() xnet.IP                                { return nil }
func (clientTestDialer) SetOutboundGateway(context.Context, *session.Outbound) {}

type clientEchoHandler struct{}

func (clientEchoHandler) NewConnectionEx(_ context.Context, conn net.Conn, _, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	defer conn.Close()
	if N.ReportHandshakeSuccess(conn) == nil {
		io.Copy(conn, conn)
	}
}

func clientFixture(t *testing.T, handlers ...N.TCPConnectionHandlerEx) (*Client, clientTestDialer, *atomic.Int32) {
	t.Helper()
	var handler N.TCPConnectionHandlerEx = clientEchoHandler{}
	if len(handlers) > 0 {
		handler = handlers[0]
	}
	service, err := engine.NewService("secret", engine.ServiceOptions{Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				service.NewConnection(context.Background(), conn, M.Socksaddr{}, nil)
			}()
		}
	}()
	c, err := newClient(context.Background(), validClientConfig(), policy.DefaultManager{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); listener.Close(); workers.Wait() })
	count := &atomic.Int32{}
	dialer := clientTestDialer{dial: func(ctx context.Context) (net.Conn, error) {
		if session.InboundFromContext(ctx) != nil {
			t.Error("physical connection inherited business identity")
		}
		if session.ContentFromContext(ctx).Attributes != nil {
			t.Error("physical connection inherited mutable content")
		}
		count.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}
	return c, dialer, count
}

type clientReplyHandler struct{ reply []byte }

func (h clientReplyHandler) NewConnectionEx(_ context.Context, conn net.Conn, _, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	defer conn.Close()
	N.ReportHandshakeSuccess(conn)
	var request [5]byte
	if _, err := io.ReadFull(conn, request[:]); err == nil {
		conn.Write(h.reply)
	}
}

func TestClientGracefulEOFPreservesQueuedResponse(t *testing.T) {
	reply := bytes.Repeat([]byte("reply"), 10000)
	c, dialer, _ := clientFixture(t, clientReplyHandler{reply: reply})
	reader, writer := pipe.New(pipe.WithSizeLimit(1024 * 1024))
	ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{Tag: "test", Target: xnet.TCPDestination(xnet.DomainAddress("echo.test"), 80)}})
	done := make(chan error, 1)
	go func() {
		done <- c.Process(ctx, &transport.Link{Reader: buf.NewReader(bytes.NewReader([]byte("hello"))), Writer: writer}, dialer)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("successful stream failed to terminate")
	}
	got, err := io.ReadAll(&buf.BufferedReader{Reader: reader})
	if err != nil || !bytes.Equal(got, reply) {
		t.Fatalf("queued response discarded: bytes=%d err=%v", len(got), err)
	}
}

// Dispatch-backed connections may not implement deadlines. Cancellation must
// still interrupt an in-flight physical record, rather than only its queue.
type stalledClientConn struct {
	net.Conn
	writes  int
	blockAt int
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (c *stalledClientConn) Write(p []byte) (int, error) {
	c.writes++
	if c.writes == c.blockAt {
		close(c.entered)
		<-c.closed
		return 0, net.ErrClosed
	}
	return len(p), nil
}
func (c *stalledClientConn) Read([]byte) (int, error)         { <-c.closed; return 0, net.ErrClosed }
func (c *stalledClientConn) Close() error                     { c.once.Do(func() { close(c.closed) }); return nil }
func (c *stalledClientConn) SetDeadline(time.Time) error      { return nil }
func (c *stalledClientConn) SetWriteDeadline(time.Time) error { return nil }
func (c *stalledClientConn) SetReadDeadline(time.Time) error  { return nil }

func TestClientCancellationInterruptsPhysicalWrites(t *testing.T) {
	for _, blockAt := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(blockAt), func(t *testing.T) {
			c, err := newClient(context.Background(), validClientConfig(), policy.DefaultManager{})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			physical := &stalledClientConn{blockAt: blockAt, entered: make(chan struct{}), closed: make(chan struct{})}
			dialer := clientTestDialer{dial: func(context.Context) (net.Conn, error) { return physical, nil }}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: xnet.TCPDestination(xnet.DomainAddress("test.invalid"), 80)}})
			done := make(chan error, 1)
			go func() {
				done <- c.Process(ctx, &transport.Link{Reader: buf.NewReader(bytes.NewReader([]byte("hello"))), Writer: buf.NewWriter(io.Discard)}, dialer)
			}()
			select {
			case <-physical.entered:
			case <-time.After(time.Second):
				t.Fatal("write barrier not reached")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("canceled physical write leaked")
			}
		})
	}
}

func clientStream(t *testing.T, c *Client, dialer clientTestDialer, wrap ...func(buf.Reader) buf.Reader) (net.Conn, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Tag: "test", Target: xnet.TCPDestination(xnet.DomainAddress("echo.test"), 80)}})
	ctx = session.ContextWithInbound(ctx, &session.Inbound{Tag: "business"})
	ctx = session.ContextWithContent(ctx, &session.Content{Attributes: map[string]string{"user": "alice"}})
	done := make(chan error, 1)
	conn := transport.NewDispatchConn(ctx, nil, transport.DispatchConnOutputStream, func(ctx context.Context, link *transport.Link) {
		for _, transform := range wrap {
			link.Reader = transform(link.Reader)
		}
		done <- c.Process(ctx, link, dialer)
	})
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn, func() {
		cancel()
		conn.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Process did not join its workers")
		}
	}
}

// Copy asks for the next buffer only after the preceding write and its watcher
// have joined. Receiving an echo alone does not establish that local boundary.
type uploadedReader struct {
	buf.Reader
	remaining int32
	done      chan struct{}
	once      sync.Once
}

func (r *uploadedReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	if r.remaining == 0 {
		r.once.Do(func() { close(r.done) })
	}
	mb, err := r.Reader.ReadMultiBuffer()
	r.remaining -= mb.Len()
	return mb, err
}

func (r *uploadedReader) Interrupt() { common.Interrupt(r.Reader) }

func clientExchange(conn net.Conn, payload []byte) error {
	if _, err := conn.Write(payload); err != nil {
		return err
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		return err
	}
	if !bytes.Equal(got, payload) {
		return io.ErrNoProgress
	}
	return nil
}

func TestClientSequentialReuseAndContextIsolation(t *testing.T) {
	c, dialer, count := clientFixture(t)
	for i := range 100 {
		payload := make([]byte, []int{1, 8192, 65535, 65536, 1024 * 1024}[i%5])
		rand.Read(payload)
		completed := make(chan struct{})
		conn, stop := clientStream(t, c, dialer, func(reader buf.Reader) buf.Reader {
			return &uploadedReader{Reader: reader, remaining: int32(len(payload)), done: completed}
		})
		err := clientExchange(conn, payload)
		if err == nil {
			select {
			case <-completed:
			case <-time.After(5 * time.Second):
				err = fmt.Errorf("upload completion barrier not reached")
			}
		}
		stop()
		if err != nil {
			t.Fatalf("stream %d: %v", i, err)
		}
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("sequential streams used %d physical connections, want 1", got)
	}
}

func TestClientConcurrentStreams(t *testing.T) {
	runCountedClientConcurrency(t)
}

func TestClientRetireDrainsAndCloseInterrupts(t *testing.T) {
	c, dialer, _ := clientFixture(t)
	conn, stop := clientStream(t, c, dialer)
	if err := clientExchange(conn, []byte("before")); err != nil {
		t.Fatal(err)
	}
	done := c.Retire()
	select {
	case <-done:
		t.Fatal("retirement completed with active business stream")
	default:
	}
	if err := clientExchange(conn, []byte("after")); err != nil {
		t.Fatal("retirement killed admitted stream", err)
	}
	ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{Target: xnet.TCPDestination(xnet.LocalHostIP, 80)}})
	if err := c.Process(ctx, &transport.Link{}, dialer); err == nil {
		t.Fatal("retired client admitted new request")
	}
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("retirement did not finish")
	}
	c.Close()
	c.Close()
}

func TestClientRetireCancelsPendingDial(t *testing.T) {
	c, _, _ := clientFixture(t)
	entered := make(chan struct{})
	dialer := clientTestDialer{dial: func(ctx context.Context) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	_, stop := clientStream(t, c, dialer)
	<-entered
	done := c.Retire()
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pending dial survived retirement")
	}
}

func TestClientCloseActiveStream(t *testing.T) {
	c, dialer, _ := clientFixture(t)
	conn, stop := clientStream(t, c, dialer)
	if err := clientExchange(conn, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on active stream")
	}
	stop()
}

func TestClientFailedStatisticalDial(t *testing.T) {
	c, _, _ := clientFixture(t)
	dialer := clientTestDialer{dial: func(context.Context) (net.Conn, error) {
		return &stat.CounterConnection{}, fmt.Errorf("dial failed")
	}}
	ctx := context.WithValue(context.Background(), clientDialKey{}, clientDial{dialer: dialer})
	if _, err := c.dial(ctx); err == nil {
		t.Fatal("failed dial accepted")
	}
}
