package anytls

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/transport"
)

func TestClientLifecycleAtWriteBarriers(t *testing.T) {
	for _, retire := range []bool{false, true} {
		for phase := 1; phase <= 3; phase++ {
			t.Run(fmt.Sprintf("retire=%v/write=%d", retire, phase), func(t *testing.T) {
				c, err := newClient(context.Background(), validClientConfig(), policy.DefaultManager{})
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				physical := &stalledClientConn{blockAt: phase, entered: make(chan struct{}), closed: make(chan struct{})}
				dialer := clientTestDialer{dial: func(context.Context) (net.Conn, error) { return physical, nil }}
				ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{Tag: "test", Target: xnet.TCPDestination(xnet.DomainAddress("test.invalid"), 80)}})
				process := make(chan error, 1)
				go func() {
					process <- c.Process(ctx, &transport.Link{Reader: buf.NewReader(bytes.NewReader([]byte("business"))), Writer: buf.NewWriter(io.Discard)}, dialer)
				}()
				select {
				case <-physical.entered:
				case <-time.After(2 * time.Second):
					t.Fatal("write barrier not entered")
				}
				if retire {
					drained := c.Retire()
					if phase == 3 {
						// Data writes belong to admitted business. Retirement must
						// not abort them; full Close remains the escape hatch.
						select {
						case <-physical.closed:
							t.Fatal("retirement closed an active business write")
						case <-drained:
							t.Fatal("retirement reported active write drained")
						default:
						}
					}
					if phase < 3 {
						select {
						case <-drained:
						case <-time.After(2 * time.Second):
							t.Fatal("retirement did not cancel unfinished admission")
						}
					}
				}
				closed := make(chan struct{})
				go func() { c.Close(); close(closed) }()
				select {
				case <-closed:
				case <-time.After(2 * time.Second):
					t.Fatal("Close did not join blocked write")
				}
				select {
				case err := <-process:
					if err == nil {
						t.Fatal("aborted Process returned success")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("Process worker remains")
				}
				c.mu.Lock()
				pending, active := len(c.pending), len(c.active)
				c.mu.Unlock()
				if pending != 0 || active != 0 {
					t.Fatalf("closed client retained pending=%d active=%d", pending, active)
				}
			})
		}
	}
}

func TestClientLateDialCannotPublishAfterRetirement(t *testing.T) {
	c, err := newClient(context.Background(), validClientConfig(), policy.DefaultManager{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	physical := &stalledClientConn{blockAt: 1, entered: make(chan struct{}), closed: make(chan struct{})}
	dialer := clientTestDialer{dial: func(context.Context) (net.Conn, error) {
		close(entered)
		<-release
		return physical, nil
	}}
	ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{Tag: "late", Target: xnet.TCPDestination(xnet.DomainAddress("test.invalid"), 80)}})
	process := make(chan error, 1)
	go func() {
		process <- c.Process(ctx, &transport.Link{Reader: buf.NewReader(bytes.NewReader(nil)), Writer: buf.NewWriter(io.Discard)}, dialer)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("dial barrier not reached")
	}
	drained := c.Retire()
	close(release)
	select {
	case err := <-process:
		if err == nil {
			t.Fatal("late successful dial admitted a retired request")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late dial did not finish")
	}
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("late dial prevented drain")
	}
	select {
	case <-physical.closed:
	default:
		t.Fatal("late dial socket leaked")
	}
	if physical.writes != 0 {
		t.Fatal("retired late dial wrote authentication bytes")
	}
}
