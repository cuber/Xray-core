package anytls

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
)

func TestClosedStreamRejectsDeadlineRearming(t *testing.T) {
	for range 100 {
		stream := newStream(1, nil)
		var workers sync.WaitGroup
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 10 {
				stream.SetDeadline(time.Now().Add(time.Hour))
			}
		}()
		stream.closeLocally(net.ErrClosed)
		workers.Wait()
		for _, set := range []func(time.Time) error{stream.SetDeadline, stream.SetReadDeadline, stream.SetWriteDeadline} {
			if err := set(time.Now().Add(time.Hour)); err != net.ErrClosed {
				t.Fatalf("closed stream rearmed a deadline: %v", err)
			}
		}
	}
}

func TestSlowReaderBackpressureAndClose(t *testing.T) {
	for range 100 {
		stream := newStream(1, nil)
		// Bypass the client-side handshake: this is an admitted server stream.
		stream.handshakeDone.Store(true)
		stream.push(buf.NewSize(maxFrameSize))
		started, finished := make(chan struct{}), make(chan struct{})
		go func() {
			close(started)
			stream.push(buf.NewSize(maxFrameSize))
			close(finished)
		}()
		<-started
		stream.readAccess.Lock()
		if stream.readPending == nil || stream.readCache != nil {
			t.Fatal("slow reader has unexpected queue state")
		}
		stream.readAccess.Unlock()
		select {
		case <-finished:
			t.Fatal("second frame bypassed the single pending buffer")
		default:
		}
		stream.closeLocally(net.ErrClosed)
		stream.discardRead(net.ErrClosed)
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("session close did not release backpressure")
		}
		stream.readAccess.Lock()
		pending, cache := stream.readPending, stream.readCache
		stream.readAccess.Unlock()
		if pending != nil || cache != nil {
			t.Fatal("closed slow stream retained buffers")
		}
	}
}

func TestServerReleasesStreamReservations(t *testing.T) {
	for round := range 100 {
		var active atomic.Int32
		service, err := NewService("test-only", ServiceOptions{
			Handler: discardHandler{},
			Authenticate: func(ctx context.Context, _ []byte) (context.Context, bool) {
				return ctx, true
			},
			StreamOpen: func(context.Context) bool {
				active.Add(1)
				return true
			},
			StreamClose:      func(context.Context) { active.Add(-1) },
			HandshakeTimeout: func(context.Context) time.Duration { return time.Millisecond },
		})
		if err != nil {
			t.Fatal(err)
		}
		request := make([]byte, 34)
		frame := func(command byte, id uint32, payload []byte) {
			header := make([]byte, frameOverhead)
			header[0] = command
			binary.BigEndian.PutUint32(header[1:5], id)
			binary.BigEndian.PutUint16(header[5:7], uint16(len(payload)))
			request = append(request, header...)
			request = append(request, payload...)
		}
		frame(commandSettings, 0, []byte("v=2\n"))
		for id := uint32(1); id <= 32; id++ {
			frame(commandSYN, id, nil)
			// Truncated destinations exercise teardown during handshake.
			frame(commandPSH, id, []byte{3, 20, 'a'})
		}
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			conn := &finiteConn{Reader: bytes.NewReader(request)}
			service.NewConnection(context.Background(), conn, M.Socksaddr{}, nil)
		}()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d: handlers did not exit", round)
		}
		if got := active.Load(); got != 0 {
			t.Fatalf("round %d: %d leaked stream reservations", round, got)
		}
	}
}
