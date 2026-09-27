package anytls_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy/anytls"
	"github.com/xtls/xray-core/transport/internet"
)

type chainAdmissionDialer struct {
	internet.DefaultSystemDialer
	hop, remote                          xnet.Port
	phase                                string
	calls, remoteCalls, closed           atomic.Int32
	entered, exited, hello, socketClosed chan struct{}
	helloOnce                            sync.Once
	sockets                              sync.Map
	dialErr                              error // Published by exited; only the blocked dial writes it.
	firstContext                         atomic.Pointer[context.Context]
}

type chainAdmissionConn struct {
	net.Conn
	d         *chainAdmissionDialer
	blocked   bool
	closeOnce sync.Once
}

func (c *chainAdmissionConn) Read(p []byte) (int, error) {
	if c.blocked {
		close(c.d.entered)
		defer close(c.d.exited)
		<-c.d.socketClosed
		return 0, net.ErrClosed
	}
	return c.Conn.Read(p)
}

func (c *chainAdmissionConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	// Observe an actual successfully written plaintext TLS ClientHello record,
	// not a timer or the goroutine merely entering the TLS dialer.
	if c.blocked && n >= 6 && p[0] == 22 && p[5] == 1 && n >= 5+int(binary.BigEndian.Uint16(p[3:5])) {
		c.d.helloOnce.Do(func() { close(c.d.hello) })
	}
	return n, err
}

func (c *chainAdmissionConn) Close() error {
	err := c.Conn.Close()
	c.closeOnce.Do(func() {
		c.d.sockets.Delete(c)
		c.d.closed.Add(1)
		if c.blocked {
			close(c.d.socketClosed)
		}
	})
	return err
}

func (d *chainAdmissionDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, opts *internet.SocketConfig) (net.Conn, error) {
	if dest.Port == d.remote {
		d.remoteCalls.Add(1)
	}
	if dest.Port != d.hop {
		return d.DefaultSystemDialer.Dial(ctx, source, dest, opts)
	}
	first := d.calls.Add(1) == 1
	if first {
		d.firstContext.Store(&ctx)
	}
	if first && d.phase == "dial" {
		close(d.entered)
		defer close(d.exited)
		<-ctx.Done()
		d.dialErr = ctx.Err()
		return nil, ctx.Err()
	}
	c, err := d.DefaultSystemDialer.Dial(ctx, source, dest, opts)
	if err != nil {
		return nil, err
	}
	w := &chainAdmissionConn{Conn: c, d: d, blocked: first && d.phase == "tls"}
	d.sockets.Store(w, struct{}{})
	return w, nil
}

func TestAnyTLSOutboundChainAdmissionFaultFreshRecovery(t *testing.T) {
	for _, entrance := range []string{"proxySettings", "dialerProxy"} {
		for _, phase := range []string{"dial", "tls"} {
			t.Run(entrance+"/anytls/"+phase, func(t *testing.T) {
				ledger := &chainCountsLedger{requests: make(map[uint64]*chainCountsReceipt)}
				failedWire := ledger.register("failed-admission-id")
				freshWire := ledger.register("first-fresh-id")
				endpoint, accepted, stopEndpoint := chainCountsDestination(t, ledger)
				f, config := outboundFixture(t, entrance+"/anytls", func(c map[string]any) {
					for _, raw := range c["outbounds"].([]any) {
						out := raw.(map[string]any)
						if out["tag"] == "a" {
							out["settings"].(map[string]any)["redirect"] = endpoint.Addr().String()
						}
					}
				})
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
				hop := manager.GetHandler("hop")
				hopValue, err := hop.ProxySettings().GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				remoteValue, err := config.ProxySettings.GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				d := &chainAdmissionDialer{hop: xnet.Port(hopValue.(*anytls.ClientConfig).Server.Port), remote: xnet.Port(remoteValue.(*anytls.ClientConfig).Server.Port), phase: phase, entered: make(chan struct{}), exited: make(chan struct{}), hello: make(chan struct{}), socketClosed: make(chan struct{})}
				internet.UseAlternativeSystemDialer(d)
				t.Cleanup(func() {
					defer internet.UseAlternativeSystemDialer(nil)
					cancel()
					d.sockets.Range(func(key, _ any) bool { key.(*chainAdmissionConn).Close(); return true })
					done := make(chan struct{})
					go func() { f.instance.Close(); close(done) }()
					obCountsWait(t, done, "chain admission Core cleanup")
				})
				hopAlias := &chainCountsAlias{Handler: hop, tag: "admission-hop", done: make(chan struct{}, 8)}
				if err := manager.AddHandler(ctx, hopAlias); err != nil {
					t.Fatal(err)
				}
				senderValue, err := config.SenderSettings.GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				sender := senderValue.(*proxyman.SenderConfig)
				if entrance == "proxySettings" {
					sender.ProxySettings.Tag = hopAlias.Tag()
				} else {
					sender.StreamSettings.SocketSettings.DialerProxy = hopAlias.Tag()
				}
				config.SenderSettings = serial.ToTypedMessage(sender)
				selectionReplace(t, f, config)
				top := &chainCountsAlias{Handler: manager.GetHandler("client"), tag: "admission-client", done: make(chan struct{}, 8), ledger: ledger}
				if err := manager.AddHandler(ctx, top); err != nil {
					t.Fatal(err)
				}
				target := xnet.TCPDestination(xnet.DomainAddress("alpha.test"), 80)
				// One budget starts before dispatch and is shorter than the native
				// fixture's 2s handshake timeout. Timeout cannot masquerade as cancel.
				budget, stopBudget := context.WithTimeout(ctx, time.Second)
				defer stopBudget()
				await := func(ch <-chan struct{}, name string) {
					t.Helper()
					select {
					case <-ch:
					case <-budget.Done():
						var hopContextErr error
						if c := d.firstContext.Load(); c != nil {
							hopContextErr = (*c).Err()
						}
						t.Fatalf("%s did not join inside admission cancellation budget: top=%d/%d hop=%d/%d relay dials/closed=%d/%d remote dials=%d destinations=%d hopContextErr=%v", name, top.opened.Load(), top.closed.Load(), hopAlias.opened.Load(), hopAlias.closed.Load(), d.calls.Load(), d.closed.Load(), d.remoteCalls.Load(), accepted.Load(), hopContextErr)
					}
				}
				request, abort := context.WithCancel(ctx)
				defer abort()
				failed, err := core.Dial(session.SetForcedOutboundTagToContext(request, top.Tag()), f.instance, target)
				if err != nil {
					t.Fatal(err)
				}
				defer failed.Close()
				limit, _ := budget.Deadline()
				failed.SetDeadline(limit)
				if _, err := failed.Write(failedWire); err != nil {
					t.Fatal("queue failed request", err)
				}
				await(d.entered, "actual relay admission gate")
				if phase == "tls" {
					await(d.hello, "relay ClientHello record")
				}
				if top.closed.Load() != 0 || hopAlias.closed.Load() != 0 || accepted.Load() != 0 || d.remoteCalls.Load() != 0 {
					t.Fatal("gate not pending at relay, or failed admission bypassed it")
				}
				abort()
				await(d.exited, "relay dial/TLS worker")
				if phase == "tls" {
					await(d.socketClosed, "failed relay physical socket")
				} else if d.dialErr != context.Canceled {
					t.Fatalf("dial ended with %v, not explicit cancellation", d.dialErr)
				}
				await(top.done, "failed top Dispatch")
				await(hopAlias.done, "failed hop Dispatch")
				if n, err := failed.Read(make([]byte, 1)); n != 0 || err == nil {
					t.Fatalf("failed request returned data n=%d err=%v", n, err)
				} else if e, ok := err.(net.Error); ok && e.Timeout() {
					t.Fatal("deadline is not failure completion", err)
				}
				failed.Close()
				if budget.Err() != nil || d.calls.Load() != 1 || d.remoteCalls.Load() != 0 || accepted.Load() != 0 {
					t.Fatal("failed request escaped cancellation or reached downstream")
				}
				select {
				case <-ledger.requests[1].uploaded:
					t.Fatal("failed payload was uploaded")
				default:
				}
				// Exactly one fresh request on the same unchanged native pools. The
				// destination withholds its echo until the native upload has returned.
				fresh, err := core.Dial(session.SetForcedOutboundTagToContext(ctx, top.Tag()), f.instance, target)
				if err != nil {
					t.Fatal(err)
				}
				defer fresh.Close()
				fresh.SetDeadline(time.Now().Add(3 * time.Second))
				if _, err := fresh.Write(freshWire); err != nil {
					t.Fatal(err)
				}
				got := make([]byte, len(freshWire))
				if _, err := io.ReadFull(fresh, got); err != nil || !bytes.Equal(got, freshWire) {
					t.Fatalf("first fresh request failed: bytes=%x err=%v", got, err)
				}
				fresh.Close()
				obCountsWait(t, top.done, "fresh top Dispatch")
				closed := make(chan struct{})
				go func() { f.instance.Close(); close(closed) }()
				obCountsWait(t, closed, "native chain shutdown")
				obCountsWait(t, hopAlias.done, "fresh hop Dispatch")
				stopEndpoint(false)
				ledger.mu.Lock()
				bad, good := ledger.requests[1].count, ledger.requests[2].count
				ledger.mu.Unlock()
				wantClosed := int32(1)
				if phase == "tls" {
					wantClosed = 2
				}
				if bad != 0 || good != 1 || accepted.Load() != 1 || d.calls.Load() != 2 || d.remoteCalls.Load() != 1 || d.closed.Load() != wantClosed || top.opened.Load() != 2 || top.closed.Load() != 2 || hopAlias.opened.Load() != 2 || hopAlias.closed.Load() != 2 {
					t.Fatalf("receipts failed/fresh=%d/%d destinations=%d relay dials/closed=%d/%d remote dials=%d top=%d/%d hop=%d/%d", bad, good, accepted.Load(), d.calls.Load(), d.closed.Load(), d.remoteCalls.Load(), top.opened.Load(), top.closed.Load(), hopAlias.opened.Load(), hopAlias.closed.Load())
				}
				t.Logf("%s AnyTLS relay %s interrupted; failed-ID=0 first-fresh-ID=1; top/hop Dispatch=2/2 each; physical relay closed=%d", entrance, phase, wantClosed)
			})
		}
	}
}
