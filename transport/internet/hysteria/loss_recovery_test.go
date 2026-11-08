package hysteria

import (
	"bytes"
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
	"github.com/apernet/quic-go/quicvarint"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/quicrecovery"
	"github.com/xtls/xray-core/transport/internet/stat"
	"google.golang.org/protobuf/proto"
)

// spec021 FR-006: a Hy2 connection whose client loses chosen datagrams on the way to the server.

// lossyPacketConn drops the short-header datagrams its drop function picks, as a lossy path would, and can
// record the size of each distinct one it is asked to send (a copy is the same datagram).
type lossyPacketConn struct {
	stdnet.PacketConn
	mu     sync.Mutex
	drop   func(b []byte) bool
	record map[string]int
}

func (p *lossyPacketConn) setDrop(drop func([]byte) bool) {
	p.mu.Lock()
	p.drop = drop
	p.mu.Unlock()
}

// startRecording records the short-header datagrams from now on.
func (p *lossyPacketConn) startRecording() {
	p.mu.Lock()
	p.record = map[string]int{}
	p.mu.Unlock()
}

// recorded returns the sizes of the distinct short-header datagrams sent since startRecording.
func (p *lossyPacketConn) recorded() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	sizes := make([]int, 0, len(p.record))
	for _, size := range p.record {
		sizes = append(sizes, size)
	}
	return sizes
}

func (p *lossyPacketConn) WriteTo(b []byte, addr stdnet.Addr) (int, error) {
	p.mu.Lock()
	drop := p.drop
	if p.record != nil && len(b) > 0 && b[0]&0x80 == 0 {
		p.record[string(b)] = len(b)
	}
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
	// With lossyOptions.headers: the server's own lossy socket and its connections' counters.
	serverLoss  *lossyPacketConn
	serverStats *internet.QuicStatsSet
}

// lossyOptions vary the fixture for spec025 FR-006.
type lossyOptions struct {
	// The two ends' Hy2 settings; Auth "fixture" alone when nil.
	client, server *Config
	// headers: each request is a lone one on a new stream, with Hy2's TCP request and response headers as the
	// proxy writes them (serveHeaders), and the server sends through a lossy socket too. No stream is opened
	// before the test's first request.
	headers bool
}

// newLossyHy2 starts a Hy2 listener that echoes each stream back, and connects a client to it.
func newLossyHy2(t *testing.T, clientParams, serverParams *internet.QuicParams) *lossyHy2 {
	return newLossyHy2With(t, clientParams, serverParams, lossyOptions{})
}

func newLossyHy2With(t *testing.T, clientParams, serverParams *internet.QuicParams, opts lossyOptions) *lossyHy2 {
	t.Helper()
	if opts.client == nil {
		opts.client = &Config{Auth: "fixture"}
	}
	if opts.server == nil {
		opts.server = &Config{Auth: "fixture"}
	}
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
	var serverConn stdnet.PacketConn = packet
	var serverLoss *lossyPacketConn
	serve := func(conn stat.Connection) {
		go func() {
			defer conn.Close()
			io.Copy(conn, conn)
		}()
	}
	if opts.headers {
		serverLoss = &lossyPacketConn{PacketConn: packet}
		serverConn, serve = serverLoss, serveHeaders
		// No MTU probes from the server either: a probe is no datagram of the exchange the test measures.
		serverConfig.DisablePathMTUDiscovery = true
	}
	tr := &quic.Transport{Conn: serverConn}
	ql, err := tr.Listen(serverTLS, serverConfig)
	if err != nil {
		tr.Close()
		packet.Close()
		t.Fatal(err)
	}
	l := &Listener{
		acceptDone: make(chan struct{}), pktConn: packet, tr: tr, listener: ql,
		config: opts.server, quicParams: &internet.QuicParams{}, masqHandler: http.NotFoundHandler(),
		quicStats: &internet.QuicStatsSet{},
		addConn:   serve,
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
		config: opts.client, tlsConfig: clientTLS, quicParams: clientParams, quicStats: &internet.QuicStatsSet{},
	}
	if opts.headers {
		t.Cleanup(func() {
			c.clean(true)
			loss.Close()
		})
		return &lossyHy2{client: c, loss: loss, serverLoss: serverLoss, serverStats: l.quicStats}
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

// tcpHeader is a Hy2 TCP request header as proxy/hysteria writes it (Core spec025 FR-001 holds the two equal).
func tcpHeader(addr, padding string) []byte {
	b := quicvarint.Append(nil, uint64(len(addr)))
	b = append(b, addr...)
	b = quicvarint.Append(b, uint64(len(padding)))
	return append(b, padding...)
}

// serveHeaders answers each stream as the Hy2 inbound's proxy does: it reads the TCP request header, writes the
// response header alone, padded as the stream says, and echoes.
func serveHeaders(conn stat.Connection) {
	go func() {
		defer conn.Close()
		r := quicvarint.NewReader(conn)
		for range 2 { // the address, then the padding
			n, err := quicvarint.Read(r)
			if err != nil {
				return
			}
			if _, err := io.CopyN(io.Discard, conn, int64(n)); err != nil {
				return
			}
		}
		padding := ""
		if conn.(*interConn).HysteriaPadding() {
			padding = TcpResponsePadding.String()
		}
		response := append(quicvarint.Append([]byte{0, 0}, uint64(len(padding))), padding...)
		if _, err := conn.Write(response); err != nil {
			return
		}
		io.Copy(conn, conn)
	}()
}

// lone sends a lone 64-byte request on a new stream, as the burst observatory's probe does: the TCP request header
// alone, padded as the stream says, then the payload; it reads the response header and the echo.
func (h *lossyHy2) lone(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, err := h.client.tcpOpen(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(4 * time.Second))
	padding := ""
	if conn.HysteriaPadding() {
		padding = TcpRequestPadding.String()
	}
	if _, err := conn.Write(tcpHeader("example.com:443", padding)); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte{'q'}, 64)
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	r := quicvarint.NewReader(conn)
	head := make([]byte, 2) // status and an empty message
	if _, err := io.ReadFull(conn, head); err != nil || head[0] != 0 || head[1] != 0 {
		t.Fatalf("response %x, %v", head, err)
	}
	n, err := quicvarint.Read(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, conn, int64(n)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("echo %q, %v", got, err)
	}
}

// Core spec025 FR-006, on spec021 FR-006's loss relay with parroting off, as on the rollout path. With padding off
// at both ends, every 1-RTT datagram of a lone 64-byte request and its echoed response on an established connection
// is at most 512 bytes; with duplicateMaxSize 512 at both ends and the first instance of each datagram dropped in
// both directions, 50 such requests complete without a PTO. With padding on, the datagrams above 512 bytes are
// counted and logged, not asserted (without a copy they would be lost every time, so that run drops nothing).
func TestHy2PaddingOffFitsCopies(t *testing.T) {
	params := &internet.QuicParams{DisableChromeParrot: true, DuplicateMaxSize: 512}
	for _, off := range []bool{true, false} {
		h := newLossyHy2With(t, params, params, lossyOptions{
			client:  &Config{Auth: "fixture", DisablePadding: off},
			server:  &Config{Auth: "fixture", DisablePadding: off},
			headers: true,
		})
		for range 10 {
			h.lone(t)
		}
		time.Sleep(50 * time.Millisecond) // the delayed ACKs of the warm-up
		h.loss.startRecording()
		h.serverLoss.startRecording()
		for range 50 {
			h.lone(t)
		}
		time.Sleep(50 * time.Millisecond)
		count := func(sizes []int) (over, largest int) {
			for _, size := range sizes {
				if size > 512 {
					over++
				}
				largest = max(largest, size)
			}
			return over, largest
		}
		up, down := h.loss.recorded(), h.serverLoss.recorded()
		upOver, upLargest := count(up)
		downOver, downLargest := count(down)
		t.Logf("disablePadding %v, 50 lone requests: client %d datagrams, %d above 512 B (largest %d B); server %d, %d above 512 B (largest %d B)",
			off, len(up), upOver, upLargest, len(down), downOver, downLargest)
		if !off {
			continue
		}
		if upOver != 0 || downOver != 0 {
			t.Fatalf("with padding off, datagrams above 512 B: client %d (largest %d), server %d (largest %d)", upOver, upLargest, downOver, downLargest)
		}

		client, server := h.stats(), h.serverStats.Report(false).Totals
		h.loss.setDrop(dropOriginals())
		h.serverLoss.setDrop(dropOriginals())
		start := time.Now()
		for range 50 {
			h.lone(t)
		}
		elapsed := time.Since(start)
		clientAfter, serverAfter := h.stats(), h.serverStats.Report(false).Totals
		t.Logf("50 lone requests with every original dropped took %s; copies sent: client %d, server %d", elapsed,
			clientAfter.CopiesSent-client.CopiesSent, serverAfter.CopiesSent-server.CopiesSent)
		if clientAfter.PTOCount != client.PTOCount || serverAfter.PTOCount != server.PTOCount ||
			clientAfter.PacketsLost != client.PacketsLost || serverAfter.PacketsLost != server.PacketsLost {
			t.Errorf("the copies carry every datagram, no PTO and no loss: client %+v -> %+v, server %+v -> %+v", client, clientAfter, server, serverAfter)
		}
	}
}
