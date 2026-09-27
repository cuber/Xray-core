package anytls

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
	"github.com/xtls/xray-core/transport"
)

type concurrencyFixture struct {
	t                                                        *testing.T
	ctx                                                      context.Context
	cancel                                                   context.CancelFunc
	client                                                   *Client
	dialer                                                   clientTestDialer
	backend, listener                                        net.Listener
	workers                                                  sync.WaitGroup
	stopOnce                                                 sync.Once
	mu                                                       sync.Mutex
	sockets                                                  map[net.Conn]struct{}
	receipts                                                 map[uint32]int
	payloads                                                 [][]byte
	uploaded                                                 []chan struct{}
	dials, physical, sessions, sessionsClosed                atomic.Int64
	logical, logicalClosed, destinations, destinationsClosed atomic.Int64
}

func (f *concurrencyFixture) own(conn net.Conn) func() {
	f.mu.Lock()
	f.sockets[conn] = struct{}{}
	f.mu.Unlock()
	return func() {
		conn.Close()
		f.mu.Lock()
		delete(f.sockets, conn)
		f.mu.Unlock()
	}
}

func (f *concurrencyFixture) accept(listener net.Listener, serve func(net.Conn)) {
	f.workers.Add(1)
	go func() {
		defer f.workers.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			release := f.own(conn)
			f.workers.Add(1)
			go func() { defer f.workers.Done(); defer release(); serve(conn) }()
		}
	}()
}

func (f *concurrencyFixture) stop() {
	f.stopOnce.Do(func() {
		f.cancel()
		if f.listener != nil {
			f.listener.Close()
		}
		if f.backend != nil {
			f.backend.Close()
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			if f.client != nil {
				f.client.Close()
			}
			f.workers.Wait()
		}()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			f.t.Error("concurrent client/peer/backend cleanup exceeded five seconds")
			f.mu.Lock()
			for conn := range f.sockets {
				conn.Close()
			}
			f.mu.Unlock()
			timer.Reset(5 * time.Second)
			select {
			case <-done:
			case <-timer.C:
				f.t.Error("forced concurrency cleanup did not join")
			}
		}
	})
}

// Each AnyTLS logical stream creates a distinct, real destination TCP socket.
// Both copy workers finish before the service's StreamClose callback fires.
func (f *concurrencyFixture) NewConnectionEx(ctx context.Context, conn net.Conn, _, target M.Socksaddr, _ N.CloseHandlerFunc) {
	defer conn.Close()
	if target.Fqdn != "echo.test" || target.Port != 80 {
		f.t.Errorf("unexpected destination %v", target)
		return
	}
	backend, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", f.backend.Addr().String())
	if err != nil {
		f.t.Error(err)
		return
	}
	defer f.own(backend)()
	if err := N.ReportHandshakeSuccess(conn); err != nil {
		f.t.Error(err)
		return
	}
	done := make(chan struct{}, 2)
	go func() { io.Copy(backend, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, backend); done <- struct{}{} }()
	<-done
	conn.Close()
	backend.Close()
	<-done
}

func (f *concurrencyFixture) echo(conn net.Conn) {
	f.destinations.Add(1)
	defer f.destinationsClosed.Add(1)
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		f.t.Error(err)
		return
	}
	size := int(binary.BigEndian.Uint32(header[:]))
	if size < 4 || size > 1024*1024 {
		f.t.Errorf("invalid frame size %d", size)
		return
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(conn, payload); err != nil {
		f.t.Error(err)
		return
	}
	id := binary.BigEndian.Uint32(payload[:4])
	if int(id) >= len(f.payloads) || !bytes.Equal(payload, f.payloads[id]) {
		f.t.Errorf("destination payload corrupted: id=%d size=%d", id, size)
		return
	}
	f.mu.Lock()
	f.receipts[id]++
	f.mu.Unlock()
	select {
	case <-f.uploaded[id]:
	case <-f.ctx.Done():
		f.t.Error("destination upload barrier canceled")
		return
	}
	if _, err := conn.Write(append(header[:], payload...)); err != nil {
		f.t.Error(err)
		return
	}
	// Let the fixed backend listener endpoint send FIN first. This preserves
	// real destination connections without exhausting client ephemeral ports
	// through 512 new TIME-WAIT sockets per repeated acceptance round.
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		f.t.Error(err)
		return
	}
	// Detect repeated data within the same stream, not just extra connections.
	if n, err := io.Copy(io.Discard, conn); n != 0 || err != nil {
		f.t.Errorf("extra destination bytes=%d err=%v", n, err)
	}
}

func newConcurrencyFixture(t *testing.T, payloads [][]byte) *concurrencyFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	f := &concurrencyFixture{t: t, ctx: ctx, cancel: cancel, payloads: payloads, sockets: map[net.Conn]struct{}{}, receipts: map[uint32]int{}}
	f.uploaded = make([]chan struct{}, len(payloads))
	for i := range f.uploaded {
		f.uploaded[i] = make(chan struct{})
	}
	t.Cleanup(f.stop)
	var err error
	f.backend, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.accept(f.backend, f.echo)
	service, err := engine.NewService("secret", engine.ServiceOptions{
		Handler:       f,
		SessionReady:  func(context.Context) { f.sessions.Add(1) },
		SessionClosed: func(context.Context) { f.sessionsClosed.Add(1) },
		StreamOpen:    func(context.Context) bool { f.logical.Add(1); return true },
		StreamClose:   func(context.Context) { f.logicalClosed.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.accept(f.listener, func(conn net.Conn) {
		f.physical.Add(1)
		if err := service.NewConnection(f.ctx, conn, M.Socksaddr{}, nil); err != nil {
			t.Error(err)
		}
	})
	f.client, err = newClient(ctx, validClientConfig(), policy.DefaultManager{})
	if err != nil {
		t.Fatal(err)
	}
	f.dialer = clientTestDialer{dial: func(ctx context.Context) (net.Conn, error) {
		f.dials.Add(1)
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", f.listener.Addr().String())
	}}
	return f
}

func (f *concurrencyFixture) exchange(payload []byte) (result error) {
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Tag: "concurrent", Target: xnet.TCPDestination(xnet.DomainAddress("echo.test"), 80)}})
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame, uint32(len(payload)))
	copy(frame[4:], payload)
	uploaded, done := f.uploaded[binary.BigEndian.Uint32(payload[:4])], make(chan error, 1)
	conn := transport.NewDispatchConn(ctx, nil, transport.DispatchConnOutputStream, func(ctx context.Context, link *transport.Link) {
		link.Reader = &uploadedReader{Reader: link.Reader, remaining: int32(len(frame)), done: uploaded}
		done <- f.client.Process(ctx, link, f.dialer)
	})
	// Dispatch-backed CNC ignores socket deadlines; cancellation must close
	// its owned pipes even if admission failed before Process installed cleanup.
	closed := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() { defer close(closed); conn.Close() })
	defer func() {
		cancel()
		conn.Close()
		if !stopCancel() {
			<-closed
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			result = fmt.Errorf("native Process failed to join")
		}
	}()
	if err := clientExchange(conn, frame); err != nil {
		return err
	}
	select {
	case <-uploaded:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("upload completion: %w", ctx.Err())
	}
}

func runCountedClientConcurrency(t *testing.T) {
	const workers, rounds, seed = 32, 16, 80303216
	t.Logf("seed=%d workers=%d rounds=%d sizes=32,8192,65535,65536,98304", seed, workers, rounds)
	random := rand.New(rand.NewSource(seed))
	payloads := make([][]byte, workers*rounds)
	for id := range payloads {
		payloads[id] = make([]byte, []int{32, 8192, 65535, 65536, 98304}[id%5])
		random.Read(payloads[id])
		binary.BigEndian.PutUint32(payloads[id], uint32(id))
	}
	f := newConcurrencyFixture(t, payloads)
	ready, start, done := make(chan struct{}, workers), make(chan struct{}), make(chan struct{})
	var group sync.WaitGroup
	var release sync.Once
	errors := make(chan error, workers)
	t.Cleanup(func() {
		f.cancel()
		release.Do(func() { close(start) })
		f.stop()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("owned load workers failed to join")
		}
	})
	for worker := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			ready <- struct{}{}
			select {
			case <-start:
			case <-f.ctx.Done():
				return
			}
			for iteration := range rounds {
				id := worker*rounds + iteration
				if err := f.exchange(payloads[id]); err != nil {
					errors <- fmt.Errorf("worker=%d iteration=%d id=%d: %w", worker, iteration, id, err)
					return
				}
			}
		}()
	}
	go func() { group.Wait(); close(done) }()
	for range workers {
		select {
		case <-ready:
		case <-f.ctx.Done():
			t.Fatal("workers failed readiness barrier")
		}
	}
	release.Do(func() { close(start) })
	select {
	case <-done:
	case <-f.ctx.Done():
		t.Fatal("concurrency workload exceeded deadline")
	}
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	f.stop()
	if got := [4]int64{f.logical.Load(), f.logicalClosed.Load(), f.destinations.Load(), f.destinationsClosed.Load()}; got != [4]int64{512, 512, 512, 512} {
		t.Errorf("logical/closed/destinations/closed=%v want all512", got)
	}
	physical := f.physical.Load()
	if physical < 1 || physical > 512 || f.dials.Load() != physical || f.sessions.Load() != physical || f.sessionsClosed.Load() != physical {
		t.Errorf("physical dials/accepted/sessions/closed=%d/%d/%d/%d", f.dials.Load(), physical, f.sessions.Load(), f.sessionsClosed.Load())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sockets) != 0 || len(f.receipts) != 512 {
		t.Errorf("owned sockets=%d distinct receipts=%d", len(f.sockets), len(f.receipts))
	}
	for id := range payloads {
		if f.receipts[uint32(id)] != 1 {
			t.Errorf("id=%d receipts=%d want1", id, f.receipts[uint32(id)])
		}
	}
	t.Logf("physical=%d logical=%d destination sockets=%d unique receipts=%d; client, peer, relay copies and backend joined", physical, f.logical.Load(), f.destinations.Load(), len(f.receipts))
}
