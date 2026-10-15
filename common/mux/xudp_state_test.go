package mux

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

type xudpStateDispatcher struct{ link *transport.Link }

func (d *xudpStateDispatcher) Dispatch(context.Context, net.Destination) (*transport.Link, error) {
	return d.link, nil
}

func (*xudpStateDispatcher) DispatchLink(context.Context, net.Destination, *transport.Link) error {
	return nil
}

func (*xudpStateDispatcher) Start() error      { return nil }
func (*xudpStateDispatcher) Close() error      { return nil }
func (*xudpStateDispatcher) Type() interface{} { return routing.DispatcherType() }

type xudpEndWriter struct{ done chan struct{} }

func (w *xudpEndWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	buf.ReleaseMulti(mb)
	close(w.done)
	return nil
}

func TestXUDPActivationConcurrentManagerScan(t *testing.T) {
	ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{}})
	stop := make(chan struct{})
	var scanner sync.WaitGroup
	scanner.Add(1)
	go func() {
		defer scanner.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			XUDPManager.Lock()
			for _, x := range XUDPManager.Map {
				// Match the janitor's guarded read, without changing its minute TTL.
				if x.Status == Expiring && time.Now().After(x.Expire) {
					t.Error("new test entry expired before its retention window")
				}
			}
			XUDPManager.Unlock()
		}
	}()
	t.Cleanup(func() { close(stop); scanner.Wait() })
	for i := 1; i <= 512; i++ {
		input, output := pipe.New(pipe.WithoutSizeLimit())
		end := &xudpEndWriter{done: make(chan struct{})}
		worker := &ServerWorker{
			dispatcher:     &xudpStateDispatcher{link: &transport.Link{Reader: input, Writer: buf.Discard}},
			link:           &transport.Link{Writer: end},
			sessionManager: NewSessionManager(),
		}
		var id [8]byte
		copy(id[:], []byte{0xf1, 0x52, 0x93, 0xa4})
		binary.BigEndian.PutUint32(id[4:], uint32(i))
		meta := &FrameMetadata{
			GlobalID: id, SessionID: 1, Target: net.UDPDestination(net.LocalHostIP, 12345),
		}
		reader := &buf.BufferedReader{Reader: buf.NewReader(bytes.NewReader([]byte{0, 1, 7}))}
		if err := worker.handleStatusNew(ctx, meta, reader); err != nil {
			t.Fatal(err)
		}
		XUDPManager.Lock()
		x := XUDPManager.Map[id]
		valid := x != nil && x.Status == Active && x.Mux != nil && x.Mux.input == input
		XUDPManager.Unlock()
		if !valid {
			t.Fatal("active XUDP entry was incompletely published")
		}
		if err := worker.sessionManager.Close(); err != nil {
			t.Fatal(err)
		}
		XUDPManager.Lock()
		retained := x.Status == Expiring && time.Until(x.Expire) > 50*time.Second
		delete(XUDPManager.Map, id)
		XUDPManager.Unlock()
		if !retained {
			t.Fatal("session retirement did not preserve the one-minute XUDP expiry")
		}
		input.Interrupt()
		_ = output.Close()
		select {
		case <-end.done:
		case <-time.After(time.Second):
			t.Fatal("XUDP response worker did not exit after explicit cleanup")
		}
	}
}
