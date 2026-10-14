package inbound

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
)

func retirementWorker() *udpWorker {
	return &udpWorker{ctx: context.Background(), address: net.LocalHostIP, activeConn: make(map[connID]*udpConn)}
}

func assertUDPConnRetired(t *testing.T, conn *udpConn) {
	t.Helper()
	select {
	case <-conn.ctx.Done():
	default:
		t.Error("retired connection context is live")
	}
	if !conn.done.Done() {
		t.Error("retired connection is not marked done")
	}
	read := make(chan error, 1)
	go func() {
		mb, err := conn.ReadMultiBuffer()
		buf.ReleaseMulti(mb)
		read <- err
	}()
	select {
	case err := <-read:
		if err == nil {
			t.Error("retired pipe still delivers payload")
		}
	case <-time.After(time.Second):
		t.Fatal("retired pipe retains its reader")
	}
}

func TestUDPWorkerCloseReleasesActivePipes(t *testing.T) {
	w := retirementWorker()
	conn, _ := w.getConnection(connID{src: net.UDPDestination(net.LocalHostIP, 1234)})
	b := buf.New()
	b.Write([]byte("queued payload"))
	if err := conn.writer.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	assertUDPConnRetired(t, conn)
	if len(w.activeConn) != 0 {
		t.Fatal("closed worker retains connections")
	}
	if got, _ := w.getConnection(connID{}); got != nil {
		t.Fatal("closed worker accepted a new connection")
	}
	w.Close()
}

func TestUDPWorkerRetiredHandlerCannotRemoveReplacement(t *testing.T) {
	w := retirementWorker()
	defer w.Close()
	id := connID{src: net.UDPDestination(net.LocalHostIP, 1234)}
	old, _ := w.getConnection(id)
	old.Close()
	fresh, existing := w.getConnection(id)
	if existing || old == fresh {
		t.Fatal("closed connection was reused")
	}
	w.removeConn(id, old)
	if w.activeConn[id] != fresh {
		t.Fatal("retired handler removed its replacement")
	}
	atomic.StoreInt64(&fresh.lastActivityTime, time.Now().Add(-3*time.Minute).Unix())
	if err := w.clean(); err != nil {
		t.Fatal(err)
	}
	assertUDPConnRetired(t, fresh)
}

func TestUDPWorkerConcurrentAdmissionAndClose(t *testing.T) {
	for round := 0; round < 100; round++ {
		w := retirementWorker()
		var wg sync.WaitGroup
		accepted := make(chan *udpConn, 16)
		start := make(chan struct{})
		for n := 0; n < 16; n++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				<-start
				conn, _ := w.getConnection(connID{src: net.UDPDestination(net.LocalHostIP, net.Port(1234+n))})
				if conn != nil {
					accepted <- conn
				}
			}(n)
		}
		close(start)
		w.Close()
		wg.Wait()
		close(accepted)
		for conn := range accepted {
			assertUDPConnRetired(t, conn)
		}
	}
}
