package reverse

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/mux"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
	"google.golang.org/protobuf/proto"
)

type drainControlWriter struct {
	state  Control_State
	writes int
	closes int
}

func (w *drainControlWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	defer buf.ReleaseMulti(mb)
	var ctl Control
	for _, b := range mb {
		if err := proto.Unmarshal(b.Bytes(), &ctl); err != nil {
			return err
		}
		w.state = ctl.State
		w.writes++
	}
	return nil
}

func (w *drainControlWriter) Close() error {
	w.closes++
	return nil
}

func drainTestWorker(t *testing.T, connections int) (*PortalWorker, *drainControlWriter) {
	t.Helper()
	r, w := pipe.New()
	client, err := mux.NewClientWorker(transport.Link{Reader: r, Writer: buf.Discard}, mux.ClientStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		r.Interrupt()
		w.Close()
	})
	ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{
		Target: net.TCPDestination(net.LocalHostIP, 80),
	}})
	for range connections {
		if !client.Dispatch(ctx, &transport.Link{
			Reader: buf.NewReader(bytes.NewReader(nil)),
			Writer: buf.Discard,
		}) {
			t.Fatal("fixture mux allocation failed")
		}
	}
	if got := client.TotalConnections(); got != uint32(connections) {
		t.Fatalf("mux allocation count = %d, want %d", got, connections)
	}
	control := &drainControlWriter{}
	controlReader, controlWriter := pipe.New()
	t.Cleanup(func() {
		controlReader.Interrupt()
		controlWriter.Close()
	})
	timer := signal.CancelAfterInactivity(context.Background(), func() {}, time.Hour)
	t.Cleanup(func() { timer.SetTimeout(0) })
	return &PortalWorker{client: client, reader: controlReader, writer: control, timer: timer}, control
}

func TestPortalHeartbeatDrainConcurrentSelection(t *testing.T) {
	draining, control := drainTestWorker(t, 257)
	fresh, _ := drainTestWorker(t, 0)
	picker := &StaticMuxPicker{workers: []*PortalWorker{draining, fresh}}
	if picked, err := picker.PickAvailable(); err != nil || picked != draining.client {
		t.Fatalf("initial selection = %p, %v; want first worker", picked, err)
	}

	start := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for range 1000 {
				if _, err := picker.PickAvailable(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	close(start)
	err := draining.heartbeat()
	readers.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if control.writes != 1 || control.state != Control_DRAIN || control.closes != 1 {
		t.Fatalf("drain control = %+v; want one DRAIN and one close", control)
	}
	if picked, err := picker.PickAvailable(); err != nil || picked != fresh.client {
		t.Fatalf("after drain selection = %p, %v; want fresh worker", picked, err)
	}
	if err := draining.heartbeat(); err == nil || control.writes != 1 || control.closes != 1 {
		t.Fatal("a drained heartbeat must not write or close again")
	}
	picker.workers = []*PortalWorker{draining}
	if picked, err := picker.PickAvailable(); err != nil || picked != draining.client {
		t.Fatalf("all-draining fallback = %p, %v; want existing fallback", picked, err)
	}
}
