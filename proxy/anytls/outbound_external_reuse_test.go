package anytls_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	handler "github.com/xtls/xray-core/app/proxyman/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport/internet"
)

type externalReuseDialer struct {
	internet.DefaultSystemDialer
	port   xnet.Port
	opened atomic.Int64
	closed atomic.Int64
}

type externalReuseConn struct {
	net.Conn
	once  sync.Once
	owner *externalReuseDialer
}

func (c *externalReuseConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.owner.closed.Add(1) })
	return err
}

func (d *externalReuseDialer) Dial(ctx context.Context, source xnet.Address, destination xnet.Destination, config *internet.SocketConfig) (net.Conn, error) {
	c, err := d.DefaultSystemDialer.Dial(ctx, source, destination, config)
	if err == nil && destination.Port == d.port {
		d.opened.Add(1)
		return &externalReuseConn{Conn: c, owner: d}, nil
	}
	return c, err
}

// Called only for direct external servers, before any business uses this pool.
// The external process and TLS verification remain owned by the parent fixture.
func assertExternalSequentialReuse(t *testing.T, f *integrationFixture, config *core.OutboundHandlerConfig, port int) {
	t.Helper()
	d := &externalReuseDialer{port: xnet.Port(port)}
	internet.UseAlternativeSystemDialer(d)
	defer internet.UseAlternativeSystemDialer(nil)
	ledger := &chainCountsLedger{requests: make(map[uint64]*chainCountsReceipt)}
	listener, destinations, stop := chainCountsDestination(t, ledger)
	t.Cleanup(func() { stop(true) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	old := m.GetHandler("client")
	alias := &chainCountsAlias{Handler: old, tag: "external-reuse", done: make(chan struct{}, 16), ledger: ledger}
	if err := m.AddHandler(ctx, alias); err != nil {
		t.Fatal(err)
	}
	destination, err := xnet.ParseDestination("tcp:" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		wire := ledger.register(fmt.Sprintf("external-reuse-%d", i))
		request, abort := context.WithCancel(ctx)
		conn, err := core.Dial(session.SetForcedOutboundTagToContext(request, alias.Tag()), f.instance, destination)
		if err != nil {
			abort()
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, err = conn.Write(wire)
		got := make([]byte, len(wire))
		if err == nil {
			_, err = io.ReadFull(conn, got)
		}
		abort()
		conn.Close()
		obCountsWait(t, alias.done, "external sequential native Dispatch")
		if err != nil || !bytes.Equal(got, wire) {
			t.Fatalf("request %d: err=%v exact=%v", i, err, bytes.Equal(got, wire))
		}
		if d.opened.Load() != 1 || d.closed.Load() != 0 {
			t.Fatalf("request %d physical opened/closed=%d/%d want=1/0", i, d.opened.Load(), d.closed.Load())
		}
	}
	if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
		t.Fatal(err)
	}
	obCountsWait(t, old.(outbound.RetiringHandler).Retirement().Retire(), "external reuse pool drain")
	if err := m.RemoveHandler(ctx, alias.Tag()); err != nil {
		t.Fatal(err)
	}
	stop(false)
	if d.opened.Load() != 1 || d.closed.Load() != 1 || alias.opened.Load() != 10 || alias.closed.Load() != 10 || destinations.Load() != 10 {
		t.Fatalf("external reuse counts physical=%d/%d logical=%d/%d destinations=%d", d.opened.Load(), d.closed.Load(), alias.opened.Load(), alias.closed.Load(), destinations.Load())
	}
	ledger.mu.Lock()
	for id, receipt := range ledger.requests {
		if receipt.count != 1 {
			t.Errorf("external request %d receipts=%d", id, receipt.count)
		}
	}
	ledger.mu.Unlock()
	// Restore the parent fixture's fresh pool for its other interoperability cases.
	if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
		t.Fatal(err)
	}
	t.Log("external server: 10 exact requests/10 destinations, one native physical TLS connection, joined retirement, no retries")
}
