package hysteria

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
)

// Core spec024 FR-005, FR-006: a session owner's pool over real loopback connections, with a fake clock for
// its timers. Every dial gets its own lossy socket, so one connection can be cut off alone.

type poolFixture struct {
	pool   *pool
	clock  time.Time
	mu     sync.Mutex
	socks  []*lossyPacketConn
	held   []stat.Connection
	stats  *internet.QuicStatsSet
	server stdnet.Addr

	listener     *Listener
	serverTLS    *tls.Config
	serverConfig *quic.Config
}

func newPoolFixture(t *testing.T, session *Session, serverStreams int64) *poolFixture {
	t.Helper()
	cert := httptest.NewTLSServer(nil)
	serverTLS := cert.TLS.Clone()
	serverTLS.NextProtos = []string{"h3"}
	clientTLS := cert.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	clientTLS.RootCAs = x509.NewCertPool()
	clientTLS.RootCAs.AddCert(cert.Certificate())
	clientTLS.ServerName = "example.com"
	cert.Close()

	packet, err := stdnet.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := &quic.Config{
		EnableDatagrams: true, MaxDatagramFrameSize: MaxDatagramFrameSize,
		AssumePeerMaxDatagramFrameSize: MaxDatagramFrameSize, DisablePathManager: true,
		MaxIncomingStreams: serverStreams,
	}
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
	f := &poolFixture{
		clock: time.Unix(1_000_000, 0), stats: &internet.QuicStatsSet{}, server: packet.LocalAddr(),
		listener: l, serverTLS: serverTLS, serverConfig: serverConfig,
	}
	t.Cleanup(func() { f.listener.Close() })
	internet.UseAlternativeSystemDialer(upgradeDialer{dial: func(context.Context) (net.Conn, error) {
		raw, err := stdnet.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		sock := &lossyPacketConn{PacketConn: raw}
		f.mu.Lock()
		f.socks = append(f.socks, sock)
		f.mu.Unlock()
		return &net.PacketConnWrapper{PacketConn: sock, Dest: packet.LocalAddr()}, nil
	}})
	t.Cleanup(func() { internet.UseAlternativeSystemDialer(nil) })

	dest := net.UDPDestination(net.LocalHostIP, net.Port(packet.LocalAddr().(*stdnet.UDPAddr).Port))
	params := quietParams(&internet.QuicParams{KeepAlivePeriod: 2})
	f.pool = newPool(session, f.stats, func() *client {
		return &client{
			lock: make(chan struct{}, 1), dest: dest, config: &Config{Auth: "fixture"},
			tlsConfig: clientTLS.Clone(), quicParams: params, quicStats: f.stats,
		}
	})
	f.pool.now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.clock
	}
	t.Cleanup(func() {
		for _, c := range f.held {
			c.Close()
		}
		f.pool.clean(true)
		f.mu.Lock()
		for _, s := range f.socks {
			s.Close()
		}
		f.mu.Unlock()
	})
	return f
}

// restartServer closes the listener and its connections, then listens again on the same address, as a
// restarted server does.
func (f *poolFixture) restartServer(t *testing.T) {
	t.Helper()
	f.listener.Close()
	packet, err := stdnet.ListenPacket("udp4", f.server.String())
	if err != nil {
		t.Fatal(err)
	}
	tr := &quic.Transport{Conn: packet}
	ql, err := tr.Listen(f.serverTLS, f.serverConfig)
	if err != nil {
		t.Fatal(err)
	}
	old := f.listener
	f.listener = &Listener{
		acceptDone: make(chan struct{}), pktConn: packet, tr: tr, listener: ql,
		config: old.config, quicParams: old.quicParams, masqHandler: old.masqHandler,
		quicStats: &internet.QuicStatsSet{}, addConn: old.addConn,
	}
	go f.listener.keepAccepting()
}

func (f *poolFixture) advance(d time.Duration) {
	f.mu.Lock()
	f.clock = f.clock.Add(d)
	f.mu.Unlock()
}

// hold opens a stream and keeps it open, one byte echoed to make it real on both ends.
func (f *poolFixture) hold(t *testing.T, ctx context.Context) stat.Connection {
	t.Helper()
	conn, err := f.tryHold(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func (f *poolFixture) tryHold(ctx context.Context) (stat.Connection, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := f.pool.open(ctx, false)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	b := []byte{1}
	_, err = conn.Write(b)
	if err == nil {
		_, err = io.ReadFull(conn, b)
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	f.held = append(f.held, conn)
	return conn, nil
}

func (f *poolFixture) release() {
	for _, c := range f.held {
		c.Close()
	}
	f.held = nil
}

// eventually waits for the pool's background dials.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *poolFixture) states() (active, draining, spare, live int) {
	for _, m := range f.pool.snapshot() {
		f.pool.mu.Lock()
		state := m.state
		f.pool.mu.Unlock()
		switch state {
		case memberActive:
			active++
		case memberDraining:
			draining++
		case memberSpare:
			spare++
		}
		if m.live() {
			live++
		}
	}
	return
}

func TestPoolScalesUpByStreamsAndSpreadsThem(t *testing.T) {
	f := newPoolFixture(t, &Session{MinConnections: 1, MaxConnections: 3, StreamsPerConnection: 16, ScaleUpStreams: 4}, 100)
	for i := 0; i < 4; i++ {
		f.hold(t, context.Background())
	}
	eventually(t, "a second connection at 4 streams", func() bool { _, _, _, live := f.states(); return live == 2 })
	for i := 0; i < 8; i++ {
		f.hold(t, context.Background())
	}
	eventually(t, "the third and last connection", func() bool { _, _, _, live := f.states(); return live == 3 })
	for i := 0; i < 8; i++ {
		f.hold(t, context.Background())
	}
	if active, _, _, _ := f.states(); active != 3 {
		t.Fatalf("maxConnections 3, got %d active", active)
	}
	for _, m := range f.pool.snapshot() {
		if m.load() < 3 {
			t.Errorf("streams go to the least loaded connection: one carries %d of 20", m.load())
		}
	}
	report := f.stats.Report(true)
	if report.Session == nil || report.Session.Active != 3 || report.Session.StreamsOpen != 20 {
		t.Errorf("session report %+v", report.Session)
	}
	for _, row := range report.Connections {
		if row.State != "active" || row.StreamsOpen == 0 {
			t.Errorf("row %+v", row.QuicConnDetail)
		}
	}
}

func TestPoolDrainsToSpareAndCloses(t *testing.T) {
	f := newPoolFixture(t, &Session{MinConnections: 1, MaxConnections: 2, StreamsPerConnection: 16, ScaleUpStreams: 2}, 100)
	f.hold(t, context.Background())
	f.hold(t, context.Background())
	eventually(t, "a second connection", func() bool { _, _, _, live := f.states(); return live == 2 })
	f.hold(t, context.Background())
	f.hold(t, context.Background())
	f.release()

	f.pool.maintain() // low load starts
	f.advance(59 * time.Second)
	f.pool.maintain()
	if _, draining, _, _ := f.states(); draining != 0 {
		t.Fatal("drained before 60 s of low load")
	}
	f.advance(2 * time.Second)
	f.pool.maintain() // drains one, which has no stream left
	f.pool.maintain() // and makes it a spare
	if active, _, spare, _ := f.states(); active != 1 || spare != 1 {
		t.Fatalf("after 61 s: %d active, %d spare", active, spare)
	}
	f.advance(4 * time.Minute)
	f.pool.maintain()
	if len(f.pool.snapshot()) != 2 {
		t.Fatal("a spare stays 5 minutes")
	}
	f.advance(time.Minute)
	f.pool.maintain()
	if active, _, spare, live := f.states(); active != 1 || spare != 0 || live != 1 {
		t.Fatalf("after the spare's 5 minutes: %d active, %d spare, %d live", active, spare, live)
	}
}

func TestPoolReactivatesBeforeDialing(t *testing.T) {
	f := newPoolFixture(t, &Session{MinConnections: 1, MaxConnections: 2, StreamsPerConnection: 16, ScaleUpStreams: 2}, 100)
	f.hold(t, context.Background())
	f.hold(t, context.Background())
	eventually(t, "a second connection", func() bool { _, _, _, live := f.states(); return live == 2 })
	f.release()
	f.pool.maintain()
	f.advance(61 * time.Second)
	f.pool.maintain()
	f.pool.maintain()
	if _, _, spare, _ := f.states(); spare != 1 {
		t.Fatal("no spare to reactivate")
	}
	dialsBefore := f.stats.Report(false).ConnectionsTotal
	f.hold(t, context.Background())
	f.hold(t, context.Background())
	if active, _, spare, _ := f.states(); active != 2 || spare != 0 {
		t.Fatalf("load back: %d active, %d spare", active, spare)
	}
	if got := f.stats.Report(false).ConnectionsTotal; got != dialsBefore {
		t.Errorf("reactivation dialed: %d connections, before %d", got, dialsBefore)
	}
}

func TestPoolWaitsForStreamCredit(t *testing.T) {
	f := newPoolFixture(t, &Session{MinConnections: 1, MaxConnections: 1, StreamsPerConnection: 16, ScaleUpStreams: 16}, 4)
	for i := 0; i < 4; i++ {
		f.hold(t, context.Background())
	}
	short, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := f.pool.open(short, false); err == nil {
		t.Fatal("a fifth stream opened past the server's limit of 4")
	}
	first := f.held[0]
	go func() {
		time.Sleep(200 * time.Millisecond)
		first.Close()
	}()
	start := time.Now()
	conn, err := f.pool.open(context.Background(), false)
	if err != nil {
		t.Fatalf("no stream after one closed: %v", err)
	}
	conn.Close()
	if waited := time.Since(start); waited < 150*time.Millisecond {
		t.Errorf("opened after %v, before the credit came back", waited)
	}
}

func TestPoolAvoidsAStalledConnection(t *testing.T) {
	f := newPoolFixture(t, &Session{MinConnections: 1, MaxConnections: 2, StreamsPerConnection: 16, ScaleUpStreams: 1}, 100)
	stalled := f.hold(t, context.Background())
	eventually(t, "a second connection", func() bool { _, _, _, live := f.states(); return live == 2 })
	f.pool.maintain()

	f.mu.Lock()
	cut := f.socks[0] // the first member's socket carries the first stream
	f.mu.Unlock()
	cut.setDrop(func([]byte) bool { return true })
	stalled.Write([]byte{2}) // sends, nothing comes back
	time.Sleep(100 * time.Millisecond)
	f.pool.maintain() // takes in what arrived before the cut
	f.advance(1500 * time.Millisecond)
	stalled.Write([]byte{3}) // in flight, never acknowledged
	time.Sleep(100 * time.Millisecond)
	f.pool.maintain()

	suspects := 0
	for _, m := range f.pool.snapshot() {
		if m.suspect.Load() {
			suspects++
		}
	}
	if suspects != 1 {
		t.Fatalf("%d suspect connections, want the cut one", suspects)
	}
	for i := 0; i < 3; i++ {
		f.hold(t, context.Background()) // would fail on the cut connection
	}
	if report := f.stats.Report(false).Session; report.Suspect != 1 {
		t.Errorf("session report %+v", report)
	}
}

func TestPoolReplacesAClosedConnection(t *testing.T) {
	f := newPoolFixture(t, &Session{MinConnections: 1, MaxConnections: 1, StreamsPerConnection: 16, ScaleUpStreams: 16}, 100)
	f.hold(t, context.Background())
	f.release()
	m := f.pool.snapshot()[0]
	m.c.live.Load().CloseWithError(0, "gone")
	eventually(t, "the connection to close", func() bool { return !m.live() })
	f.hold(t, context.Background())
	if got := f.stats.Report(false).ConnectionsTotal; got != 2 {
		t.Errorf("the closed connection was not replaced: %d connections", got)
	}
}

func TestPoolRotatesProbes(t *testing.T) {
	f := newPoolFixture(t, &Session{MinConnections: 1, MaxConnections: 2, StreamsPerConnection: 16, ScaleUpStreams: 1}, 100)
	f.hold(t, context.Background())
	eventually(t, "a second connection", func() bool { _, _, _, live := f.states(); return live == 2 })
	probe := session.ContextWithProbe(context.Background())
	seen := make(map[*quic.Conn]int)
	for i := 0; i < 4; i++ {
		conn := f.hold(t, probe).(*interConn)
		for _, m := range f.pool.snapshot() {
			if m.c.live.Load() != nil && m.c.live.Load().LocalAddr().String() == conn.local.String() {
				seen[m.c.live.Load()]++
			}
		}
	}
	if len(seen) != 2 {
		t.Fatalf("probes on %d connections, want both", len(seen))
	}
	for _, n := range seen {
		if n != 2 {
			t.Errorf("probes not in turn: %v", seen)
		}
	}
}
