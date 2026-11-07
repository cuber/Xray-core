package hysteria

import (
	"context"
	"crypto/x509"
	"io"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/quicrecovery"
	"github.com/xtls/xray-core/transport/internet/stat"
	"google.golang.org/protobuf/proto"
)

// spec021 FR-006: a Hy2 connection whose client loses chosen datagrams on the way to the server.

// lossyPacketConn drops the short-header datagrams its drop function picks, as a lossy path would.
type lossyPacketConn struct {
	stdnet.PacketConn
	mu   sync.Mutex
	drop func(b []byte) bool
}

func (p *lossyPacketConn) setDrop(drop func([]byte) bool) {
	p.mu.Lock()
	p.drop = drop
	p.mu.Unlock()
}

func (p *lossyPacketConn) WriteTo(b []byte, addr stdnet.Addr) (int, error) {
	p.mu.Lock()
	drop := p.drop
	p.mu.Unlock()
	if len(b) > 0 && b[0]&0x80 == 0 && drop != nil && drop(b) {
		return len(b), nil
	}
	return p.PacketConn.WriteTo(b, addr)
}

type lossyHy2 struct {
	conn   stat.Connection
	client *client
	loss   *lossyPacketConn
}

// newLossyHy2 starts a Hy2 listener that echoes each stream back, and connects a client to it.
func newLossyHy2(t *testing.T, clientParams, serverParams *internet.QuicParams) *lossyHy2 {
	t.Helper()
	cert := httptest.NewTLSServer(nil)
	serverTLS := cert.TLS.Clone()
	serverTLS.NextProtos = []string{"h3"}
	clientTLS := cert.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	clientTLS.RootCAs = x509.NewCertPool()
	clientTLS.RootCAs.AddCert(cert.Certificate())
	clientTLS.ServerName = "example.com"
	cert.Close()

	// No MTU probes, as on the fleet's Hy2 paths: the drop functions pick the next datagram the client sends.
	clientParams, serverParams = quietParams(clientParams), quietParams(serverParams)
	packet, err := stdnet.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := &quic.Config{
		EnableDatagrams: true, MaxDatagramFrameSize: MaxDatagramFrameSize,
		AssumePeerMaxDatagramFrameSize: MaxDatagramFrameSize, DisablePathManager: true,
	}
	quicrecovery.Apply(serverConfig, serverParams)
	tr := &quic.Transport{Conn: packet}
	ql, err := tr.Listen(serverTLS, serverConfig)
	if err != nil {
		tr.Close()
		packet.Close()
		t.Fatal(err)
	}
	l := &Listener{
		acceptDone: make(chan struct{}), pktConn: packet, tr: tr, listener: ql,
		config: &Config{Auth: "fixture"}, quicParams: &internet.QuicParams{}, masqHandler: http.NotFoundHandler(),
		quicStats: &internet.QuicStatsSet{},
		addConn: func(conn stat.Connection) {
			go func() {
				defer conn.Close()
				io.Copy(conn, conn)
			}()
		},
	}
	go l.keepAccepting()
	t.Cleanup(func() { l.Close() })

	raw, err := stdnet.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	loss := &lossyPacketConn{PacketConn: raw}
	internet.UseAlternativeSystemDialer(upgradeDialer{dial: func(context.Context) (net.Conn, error) {
		return &net.PacketConnWrapper{PacketConn: loss, Dest: packet.LocalAddr()}, nil
	}})
	t.Cleanup(func() { internet.UseAlternativeSystemDialer(nil) })

	c := &client{
		lock: make(chan struct{}, 1), dest: net.UDPDestination(net.LocalHostIP, net.Port(packet.LocalAddr().(*stdnet.UDPAddr).Port)),
		config: &Config{Auth: "fixture"}, tlsConfig: clientTLS, quicParams: clientParams, quicStats: &internet.QuicStatsSet{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, err := c.tcp(ctx)
	if err != nil {
		c.clean(true)
		loss.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		c.clean(true)
		loss.Close()
	})
	h := &lossyHy2{conn: conn, client: c, loss: loss}
	h.warmUp(t)
	return h
}

func (h *lossyHy2) roundTrip(t *testing.T, b byte) time.Duration {
	t.Helper()
	start := time.Now()
	if _, err := h.conn.Write([]byte{b}); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 1)
	if _, err := io.ReadFull(h.conn, got); err != nil {
		t.Fatal(err)
	}
	if got[0] != b {
		t.Fatalf("echo %d, want %d", got[0], b)
	}
	return time.Since(start)
}

// warmUp settles the RTT estimate on the lossless loopback path, then waits until the delayed ACKs
// have gone out, so that the next datagram the client sends is the next request.
func (h *lossyHy2) warmUp(t *testing.T) {
	t.Helper()
	for i := range 20 {
		h.roundTrip(t, byte(i))
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
}

func quietParams(p *internet.QuicParams) *internet.QuicParams {
	q := &internet.QuicParams{}
	if p != nil {
		q = proto.Clone(p).(*internet.QuicParams)
	}
	q.DisablePathMtuDiscovery = true
	return q
}

func (h *lossyHy2) stats() internet.QuicCounters {
	return h.client.quicStats.Report(false).Totals
}

// dropFirst drops the next short-header datagram only.
func dropFirst() func([]byte) bool {
	var dropped atomic.Bool
	return func([]byte) bool { return dropped.CompareAndSwap(false, true) }
}

// dropOriginals drops the first of each pair of identical datagrams, so that only the copies arrive.
func dropOriginals() func([]byte) bool {
	var mu sync.Mutex
	seen := make(map[string]bool)
	return func(b []byte) bool {
		mu.Lock()
		defer mu.Unlock()
		if seen[string(b)] {
			return false
		}
		seen[string(b)] = true
		return true
	}
}

// dropUntil drops everything until the deadline.
func dropUntil(deadline time.Time) func([]byte) bool {
	return func([]byte) bool { return time.Now().Before(deadline) }
}

func TestHy2LossRecoveryMaxAckDelay(t *testing.T) {
	lost := func(params *internet.QuicParams) (time.Duration, internet.QuicCounters) {
		h := newLossyHy2(t, params, params)
		h.loss.setDrop(dropFirst())
		elapsed := h.roundTrip(t, 0xfe)
		return elapsed, h.stats()
	}
	slow, slowStats := lost(nil)
	fast, fastStats := lost(&internet.QuicParams{MaxAckDelayMs: 2})
	t.Logf("a request whose first datagram was lost took %s by default, %s with maxAckDelayMs 2", slow, fast)
	if fast > 2*time.Millisecond+15*time.Millisecond {
		t.Errorf("with maxAckDelayMs 2 the PTO recovers within RTT + 2 ms + 15 ms, took %s", fast)
	}
	if slow-fast < 20*time.Millisecond {
		t.Errorf("the default 25 ms max_ack_delay costs at least 20 ms more: %s against %s", slow, fast)
	}
	for _, s := range []internet.QuicCounters{slowStats, fastStats} {
		if s.PTOCount == 0 {
			t.Errorf("a lone lost request is recovered by a PTO: %+v", s)
		}
	}
}

func TestHy2LossRecoveryCopies(t *testing.T) {
	h := newLossyHy2(t, &internet.QuicParams{DuplicateMaxSize: 1500}, nil)
	before := h.stats()
	h.loss.setDrop(dropOriginals())
	for i := range 5 {
		if elapsed := h.roundTrip(t, byte(i)); elapsed > 10*time.Millisecond {
			t.Errorf("the copy carries a request whose original was lost, took %s", elapsed)
		}
	}
	after := h.stats()
	if after.CopiesSent == before.CopiesSent || after.PacketsLost != before.PacketsLost || after.PTOCount != before.PTOCount {
		t.Errorf("copies sent, nothing lost and no PTO: before %+v, after %+v", before, after)
	}
}

func TestHy2LossRecoveryPTOBackoffLimit(t *testing.T) {
	const blackout = 300 * time.Millisecond
	lost := func(params *internet.QuicParams) time.Duration {
		h := newLossyHy2(t, params, params)
		h.loss.setDrop(dropUntil(time.Now().Add(blackout)))
		return h.roundTrip(t, 0xfd)
	}
	unlimited := lost(nil)
	limited := lost(&internet.QuicParams{MaxAckDelayMs: 2, PtoBackoffLimit: 1})
	t.Logf("a request in a %s blackout took %s with an unlimited PTO backoff, %s with ptoBackoffLimit 1", blackout, unlimited, limited)
	if limited > blackout+30*time.Millisecond {
		t.Errorf("ptoBackoffLimit 1 finishes within one PTO of the blackout's end, took %s", limited)
	}
	if unlimited < blackout+50*time.Millisecond {
		t.Errorf("the doubling PTO overshoots the blackout, took %s", unlimited)
	}
}
