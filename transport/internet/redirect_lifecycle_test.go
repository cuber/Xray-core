package internet

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
)

type redirectLifecycleHandler struct {
	outbound.Handler
	run func(context.Context, *transport.Link)
}

func (h redirectLifecycleHandler) Dispatch(ctx context.Context, link *transport.Link) {
	h.run(ctx, link)
}

func redirectLifecycleJoin(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("connection Close did not join pending redirect handler")
	}
}

func TestRedirectCloseOwnsPendingContext(t *testing.T) {
	for _, network := range []net.Network{net.Network_TCP, net.Network_UDP} {
		t.Run(network.String(), func(t *testing.T) {
			type key struct{}
			value := new(int)
			parent, cancel := context.WithTimeout(context.WithValue(context.Background(), key{}, value), time.Hour)
			defer cancel()
			parent = session.ContextWithOutbounds(parent, []*session.Outbound{{Tag: "outer"}})
			entered := make(chan context.Context, 1)
			done, force := make(chan struct{}), make(chan struct{})
			h := redirectLifecycleHandler{run: func(ctx context.Context, _ *transport.Link) {
				defer close(done)
				entered <- ctx
				// Pending dial/handshake does not read either dispatch pipe yet.
				select {
				case <-ctx.Done():
				case <-force:
				}
			}}
			c := redirect(parent, net.Destination{Network: network, Address: net.LocalHostIP, Port: 443}, "relay", h)
			t.Cleanup(func() { c.Close(); close(force); redirectLifecycleJoin(t, done) })
			var child context.Context
			select {
			case child = <-entered:
			case <-time.After(time.Second):
				t.Fatal("handler did not start")
			}
			cancel()
			if child.Err() != nil {
				t.Fatal("original request cancellation reached independent redirect", child.Err())
			}
			if _, ok := child.Deadline(); ok {
				t.Fatal("redirect inherited original request deadline")
			}
			if child.Value(key{}) != value {
				t.Fatal("redirect lost context value")
			}
			obs := session.OutboundsFromContext(child)
			if len(obs) != 2 || obs[0].Tag != "outer" || obs[1].Tag != "relay" || obs[1].Target.Port != 443 {
				t.Fatalf("route ancestry=%v", obs)
			}
			if _, ok := c.(buf.Reader); !ok {
				t.Fatal("redirect lost multi-buffer reader")
			}
			if _, ok := c.(buf.Writer); !ok {
				t.Fatal("redirect lost multi-buffer writer")
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			redirectLifecycleJoin(t, done)
			if child.Err() != context.Canceled {
				t.Fatalf("closed connection context=%v", child.Err())
			}
		})
	}
}

func TestRedirectRemainsUsableAfterRequestCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, force := make(chan struct{}), make(chan struct{})
	entered := make(chan context.Context, 1)
	h := redirectLifecycleHandler{run: func(ctx context.Context, link *transport.Link) {
		defer close(done)
		entered <- ctx
		mb, err := link.Reader.ReadMultiBuffer()
		if err != nil {
			buf.ReleaseMulti(mb)
			return
		}
		if err := link.Writer.WriteMultiBuffer(mb); err != nil {
			return
		}
		select {
		case <-ctx.Done():
		case <-force:
		}
	}}
	c := redirect(parent, net.TCPDestination(net.LocalHostIP, 443), "relay", h)
	t.Cleanup(func() { c.Close(); close(force); redirectLifecycleJoin(t, done) })
	var child context.Context
	select {
	case child = <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	payload := []byte("pooled connection remains independent")
	b := buf.New()
	b.Write(payload)
	if err := c.(buf.Writer).WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
		t.Fatal(err)
	}
	readDone := make(chan struct{})
	var reply buf.MultiBuffer
	var readErr error
	go func() { defer close(readDone); reply, readErr = c.(buf.Reader).ReadMultiBuffer() }()
	t.Cleanup(func() { c.Close(); redirectLifecycleJoin(t, readDone); buf.ReleaseMulti(reply) })
	redirectLifecycleJoin(t, readDone)
	if readErr != nil || len(reply) != 1 || !bytes.Equal(reply[0].Bytes(), payload) {
		t.Fatalf("post-cancel reply=%v err=%v", reply, readErr)
	}
	if child.Err() != nil {
		t.Fatal("request cancellation invalidated redirect context")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	redirectLifecycleJoin(t, done)
}

func TestRedirectRunnerReturnDoesNotCancelConnection(t *testing.T) {
	done := make(chan struct{})
	var child context.Context
	h := redirectLifecycleHandler{run: func(ctx context.Context, _ *transport.Link) { child = ctx; close(done) }}
	c := redirect(context.Background(), net.TCPDestination(net.LocalHostIP, 443), "relay", h)
	t.Cleanup(func() { c.Close(); redirectLifecycleJoin(t, done) })
	redirectLifecycleJoin(t, done)
	if child.Err() != nil {
		t.Fatal("runner return canceled the connection-owned context")
	}
	if _, err := c.Write([]byte("queued after runner return")); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if child.Err() != context.Canceled {
		t.Fatal("explicit Close did not cancel child", child.Err())
	}
}
