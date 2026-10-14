package anytls_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/proxy/anytls"
	hyproxy "github.com/xtls/xray-core/proxy/hysteria"
	"github.com/xtls/xray-core/proxy/socks"
	vless "github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/tagged"
)

// Count successful kernel TCP connects to the relay independently of native
// Dispatch and decoded relay requests. This is not a QUIC connection counter.
type relayCountsDialer struct {
	internet.DefaultSystemDialer
	port           xnet.Port
	opened, closed atomic.Int64
}

type relayCountsConn struct {
	net.Conn
	once  sync.Once
	owner *relayCountsDialer
}

func (c *relayCountsConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.owner.closed.Add(1) })
	return err
}

func (d *relayCountsDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, opts *internet.SocketConfig) (net.Conn, error) {
	c, err := d.DefaultSystemDialer.Dial(ctx, source, dest, opts)
	if err == nil && dest.Network == xnet.Network_TCP && dest.Port == d.port {
		d.opened.Add(1)
		return &relayCountsConn{Conn: c, owner: d}, nil
	}
	return c, err
}

func TestAnyTLSOutboundTCPRelayLayerCounts(t *testing.T) {
	for _, protocol := range []string{"socks", "vless"} {
		for _, mode := range []string{"proxySettings", "dialerProxy"} {
			t.Run(mode+"/"+protocol, func(t *testing.T) {
				runAnyTLSRelayCounts(t, mode, protocol)
			})
		}
	}
}

func runAnyTLSRelayCounts(t *testing.T, mode, protocol string) {
	f, config := outboundFixture(t, mode+"/"+protocol, func(config map[string]any) {
		rules := config["routing"].(map[string]any)["rules"].([]any)
		rules[1].(map[string]any)["outboundTag"] = "counted-exit"
	})
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), core.XrayKey(1), f.instance), 30*time.Second)
	defer cancel()
	manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	hopConfig, err := manager.GetHandler("hop").ProxySettings().GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	physical := &relayCountsDialer{}
	switch c := hopConfig.(type) {
	case *socks.ClientConfig:
		physical.port = xnet.Port(c.Server.Port)
	case *vless.Config:
		physical.port = xnet.Port(c.Vnext.Port)
	case *hyproxy.ClientConfig:
		// QUIC connections are counted by the independent listener below.
	default:
		t.Fatalf("unexpected relay config %T", c)
	}
	internet.UseAlternativeSystemDialer(physical)
	defer internet.UseAlternativeSystemDialer(nil)
	ledger := &chainCountsLedger{requests: make(map[uint64]*chainCountsReceipt)}
	backend, destinations, stopBackend := chainCountsDestination(t, ledger)
	cert := f.inbound["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)["certificates"].([]any)[0].(map[string]any)
	pair, err := tls.X509KeyPair([]byte(strings.Join(cert["certificate"].([]string), "\n")), []byte(strings.Join(cert["key"].([]string), "\n")))
	if err != nil {
		t.Fatal(err)
	}
	peer := newChainCountsPeer(t, pair, "remote-secret", func(ctx context.Context, conn net.Conn, target M.Socksaddr) {
		defer conn.Close()
		if target.Fqdn != "counts.test" || target.Port != 80 {
			t.Errorf("destination=%v", target)
			return
		}
		upstream, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", backend.Addr().String())
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		if err := N.ReportHandshakeSuccess(conn); err != nil {
			t.Error(err)
			return
		}
		done := make(chan struct{}, 2)
		copyAndClose := func(dst, src net.Conn) {
			io.Copy(dst, src)
			conn.Close()
			upstream.Close()
			done <- struct{}{}
		}
		go copyAndClose(conn, upstream)
		go copyAndClose(upstream, conn)
		<-done
		<-done
	})
	alias := func(tag, original string) *chainCountsAlias {
		h := &chainCountsAlias{Handler: manager.GetHandler(original), tag: tag, done: make(chan struct{}, 32)}
		if original == "client" {
			h.ledger = ledger
		}
		if err := manager.AddHandler(ctx, h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	hop, exit := alias("counted-hop", "hop"), alias("counted-exit", "chain-exit")
	var hy2 *hy2CountsPeer
	if protocol == "hysteria" {
		hy2 = newHy2CountsPeer(t, pair, peer.listener.Addr().String())
		h := manager.GetHandler("hop")
		settings := hopConfig.(*hyproxy.ClientConfig)
		settings.Server.Port = uint32(hy2.listener.Addr().(*net.UDPAddr).Port)
		if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "hop"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: &core.OutboundHandlerConfig{Tag: "hop", ProxySettings: serial.ToTypedMessage(settings), SenderSettings: h.SenderSettings()}}); err != nil {
			t.Fatal(err)
		}
		hop.Handler = manager.GetHandler("hop")
	}
	value, err := config.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	value.(*anytls.ClientConfig).Server.Port = uint32(peer.listener.Addr().(*net.TCPAddr).Port)
	config.ProxySettings = serial.ToTypedMessage(value)
	value, err = config.SenderSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	sender := value.(*proxyman.SenderConfig)
	if mode == "proxySettings" {
		sender.ProxySettings.Tag = hop.Tag()
	} else {
		sender.StreamSettings.SocketSettings.DialerProxy = hop.Tag()
	}
	config.SenderSettings = serial.ToTypedMessage(sender)
	if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
		t.Fatal(err)
	}
	top := alias("counted-client", "client")
	dispatcher := f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher)
	dial := func() net.Conn {
		t.Helper()
		conn, err := tagged.Dialer(ctx, dispatcher, xnet.TCPDestination(xnet.DomainAddress("counts.test"), 80), top.Tag())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	exchange := func(conn net.Conn, id string) {
		t.Helper()
		requestCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		closed := make(chan struct{})
		abort := context.AfterFunc(requestCtx, func() { conn.Close(); close(closed) })
		defer func() {
			if !abort() {
				<-closed
			}
		}()
		wire := ledger.register(id)
		if _, err := conn.Write(wire); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(wire))
		if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, wire) {
			t.Fatalf("ID=%s exact echo failed: %v", id, err)
		}
	}
	join := func() {
		obCountsWait(t, top.done, "top Dispatch")
		obCountsWait(t, peer.streamDone, "peer logical stream")
	}
	check := func(stage string, sessions, logical, released int64) {
		t.Helper()
		got := [10]int64{physical.opened.Load(), physical.closed.Load(), hop.opened.Load(), hop.closed.Load(), exit.opened.Load(), exit.closed.Load(), peer.accepted.Load(), peer.physical.Load(), peer.logical.Load(), peer.released.Load()}
		want := [10]int64{sessions, 0, sessions, 0, sessions, 0, sessions, sessions, logical, released}
		if hy2 != nil {
			got[0], got[1] = hy2.accepted.Load(), hy2.closed.Load()
			got[4], got[5] = hy2.streams.Load(), hy2.streamsClosed.Load()
			want[0] = 1
		}
		if got != want {
			t.Fatalf("%s TCPopen/closed,hop open/closed,relay decoded open/closed,TLS accept/ready,logical/released=%v want=%v", stage, got, want)
		}
		if destinations.Load() != logical || top.opened.Load() != logical || top.closed.Load() != released || peer.ended.Load() != 0 {
			t.Fatalf("%s destination/dispatch/session lifetime mismatch", stage)
		}
	}
	for i := range 3 {
		c := dial()
		exchange(c, fmt.Sprintf("seq-%d", i))
		c.Close()
		join()
	}
	check("sequential", 1, 3, 3)
	// Keep four business streams simultaneously alive. Creating them in
	// order makes the exact reservation count independent of dial races.
	held := make([]net.Conn, 4)
	for i := range held {
		held[i] = dial()
		exchange(held[i], fmt.Sprintf("held-%d", i))
	}
	check("four held", 4, 7, 3)
	for i, c := range held {
		exchange(c, fmt.Sprintf("released-%d", i))
		c.Close()
		join()
	}
	c := dial()
	exchange(c, "last")
	c.Close()
	join()
	check("drained", 4, 8, 8)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		if err := f.instance.Close(); err != nil {
			t.Error(err)
		}
	}()
	obCountsWait(t, closed, "Core shutdown")
	for range 4 {
		obCountsWait(t, hop.done, "relay client Dispatch")
		if hy2 == nil {
			obCountsWait(t, exit.done, "relay server Dispatch")
		} else {
			obCountsWait(t, hy2.streamDone, "Hy2 stream relay")
		}
	}
	peer.close(t, false)
	stopBackend(false)
	if hy2 == nil && (physical.closed.Load() != 4 || exit.closed.Load() != 4) {
		t.Fatal("TCP relay did not close")
	}
	if hy2 != nil {
		hy2.stop()
		hy2.assertCounts(4)
	}
	if peer.ended.Load() != 4 || hop.closed.Load() != 4 {
		t.Fatal("relay/native sessions did not all close")
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if len(ledger.requests) != 12 {
		t.Fatalf("receipts=%d want=12", len(ledger.requests))
	}
	for id, r := range ledger.requests {
		if r.count != 1 {
			t.Errorf("ID=%d received %d times", id, r.count)
		}
	}
	if hy2 == nil {
		t.Log("3 sequential + 4 held(two messages) + 1 sequential: relay TCP=4, decoded relay requests=4, native TLS=4, logical=8, destinations=8, unique receipts=12; all closed/joined")
	} else {
		t.Log("independent Hy2 fixture: real QUIC connections=1, TCP streams=4; native AnyTLS TLS=4, logical=8, destinations=8, receipts=12; business relays joined before fixture QUIC shutdown")
	}
}
