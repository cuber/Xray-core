package udp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
)

type rejectedRedispatch struct {
	routing.Dispatcher
	calls atomic.Int32
}

func (d *rejectedRedispatch) Dispatch(context.Context, net.Destination) (*transport.Link, error) {
	d.calls.Add(1)
	return nil, errors.New("controlled replacement failure")
}

func TestDispatcherConcurrentEntryRetirement(t *testing.T) {
	for round := 0; round < 100; round++ {
		var cancellations atomic.Int32
		entry := &connEntry{link: &transport.Link{}, cancel: func() { cancellations.Add(1) }}
		next := &rejectedRedispatch{}
		d := &Dispatcher{conn: entry, dispatcher: next}
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			entry.terminate()
		}()
		go func() {
			defer workers.Done()
			<-start
			_, _ = d.getInboundRay(context.Background(), net.UDPDestination(net.LocalHostIP, 53))
		}()
		close(start)
		workers.Wait()
		if c, err := d.getInboundRay(context.Background(), net.UDPDestination(net.LocalHostIP, 53)); err == nil || c != nil {
			t.Fatal("retired entry reused instead of replacement failure")
		}
		if cancellations.Load() != 1 || next.calls.Load() == 0 {
			t.Fatal("retirement did not cancel and trigger replacement")
		}
	}
}
