package anytls

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
)

type malformedResponseHandler struct {
	t        *testing.T
	wire     net.Conn
	frame    []byte
	received *atomic.Int64
	done     chan struct{}
}

func (h malformedResponseHandler) NewConnectionEx(_ context.Context, conn net.Conn, _, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	defer close(h.done)
	defer conn.Close()
	defer h.wire.Close()
	if err := N.ReportHandshakeSuccess(conn); err != nil {
		h.t.Error(err)
		return
	}
	var payload [8]byte
	if _, err := io.ReadFull(conn, payload[:]); err != nil || string(payload[:]) != "received" {
		h.t.Errorf("backend receipt=%q err=%v", payload, err)
		return
	}
	h.received.Add(1)
	// Authentication and a real logical request succeeded. Now corrupt only
	// the response record, after business delivery became uncertain.
	if _, err := h.wire.Write(h.frame); err != nil {
		h.t.Error(err)
	}
}

func TestClientMalformedResponseDoesNotReplay(t *testing.T) {
	for name, frame := range map[string][]byte{
		"truncated-header": {2, 0},
		// PSH stream 1 advertises ten body bytes, then ends after one byte.
		"truncated-body": {2, 0, 0, 0, 1, 0, 10, 'x'},
	} {
		t.Run(name, func(t *testing.T) {
			client, healthy, _ := clientFixture(t)
			var calls, receipts atomic.Int64
			peerDone, handlerDone := make(chan struct{}), make(chan struct{})
			dialer := clientTestDialer{dial: func(ctx context.Context) (net.Conn, error) {
				if calls.Add(1) != 1 {
					return healthy.dial(ctx)
				}
				local, peer := net.Pipe()
				peer.SetDeadline(time.Now().Add(5 * time.Second))
				service, err := engine.NewService("secret", engine.ServiceOptions{Handler: malformedResponseHandler{t, peer, frame, &receipts, handlerDone}})
				if err != nil {
					local.Close()
					peer.Close()
					return nil, err
				}
				go func() {
					defer close(peerDone)
					defer peer.Close()
					service.NewConnection(context.Background(), peer, M.Socksaddr{}, nil)
				}()
				return local, nil
			}}
			conn, stop := clientStream(t, client, dialer)
			err := clientExchange(conn, []byte("received"))
			stop()
			if err == nil {
				t.Fatal("malformed response succeeded")
			}
			for _, done := range []chan struct{}{peerDone, handlerDone} {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("malformed peer workers remain")
				}
			}
			if calls.Load() != 1 || receipts.Load() != 1 {
				t.Fatalf("uncertain business replayed: physical=%d receipts=%d", calls.Load(), receipts.Load())
			}
			next, finish := clientStream(t, client, dialer)
			err = clientExchange(next, []byte("fresh-request"))
			finish()
			if err != nil || calls.Load() != 2 {
				t.Fatalf("fresh request did not recover: dials=%d err=%v", calls.Load(), err)
			}
		})
	}
}
