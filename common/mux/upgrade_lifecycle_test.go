package mux

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport"
)

type upgradeReader struct {
	stop chan struct{}
	once sync.Once
}

func (r *upgradeReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	<-r.stop
	return nil, io.EOF
}
func (r *upgradeReader) Interrupt() { r.once.Do(func() { close(r.stop) }) }

type upgradeWriter struct {
	stop chan struct{}
	once sync.Once
}

func (w *upgradeWriter) WriteMultiBuffer(b buf.MultiBuffer) error {
	buf.ReleaseMulti(b)
	return io.ErrClosedPipe
}

func (w *upgradeWriter) Close() error {
	w.once.Do(func() { close(w.stop) })
	return nil
}
func (w *upgradeWriter) Interrupt() { _ = w.Close() }

func upgradeWait(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not close", name)
	}
}

// Exercise admission versus immediate peer EOF and SessionManager.Close. Owned
// reader/writer completion is the oracle, not a process-wide goroutine count.
func TestUpgradeMuxDispatchCloseOwnership(t *testing.T) {
	for round := 0; round < 100; round++ {
		peerReader := &upgradeReader{stop: make(chan struct{})}
		peerWriter := &upgradeWriter{stop: make(chan struct{})}
		worker, err := NewClientWorker(transport.Link{Reader: peerReader, Writer: peerWriter}, ClientStrategy{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: net.TCPDestination(net.LocalHostIP, 80)}})
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := &upgradeReader{stop: make(chan struct{})}
				w := &upgradeWriter{stop: make(chan struct{})}
				<-start
				if !worker.Dispatch(ctx, &transport.Link{Reader: r, Writer: w}) {
					r.Interrupt()
					w.Close()
				}
				upgradeWait(t, r.stop, "session reader")
				upgradeWait(t, w.stop, "session writer")
			}()
		}
		close(start)
		peerReader.Interrupt()
		worker.sessionManager.Close()
		worker.Close()
		wg.Wait()
		cancel()
		upgradeWait(t, peerWriter.stop, "physical writer")
		upgradeWait(t, worker.WaitClosed(), "worker")
		if worker.sessionManager.Size() != 0 || !worker.sessionManager.Closed() {
			t.Fatal("closed worker retained admitted sessions")
		}
	}
}
