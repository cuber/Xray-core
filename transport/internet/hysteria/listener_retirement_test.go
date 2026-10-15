package hysteria

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
)

func retirementQUICListener(t *testing.T, handler http.Handler) (*Listener, *quic.Conn) {
	t.Helper()
	cert := httptest.NewTLSServer(nil)
	serverTLS := cert.TLS.Clone()
	serverTLS.NextProtos = []string{"h3"}
	roots := x509.NewCertPool()
	roots.AddCert(cert.Certificate())
	cert.Close()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr := &quic.Transport{Conn: packet}
	ql, err := tr.Listen(serverTLS, &quic.Config{EnableDatagrams: true})
	if err != nil {
		packet.Close()
		t.Fatal(err)
	}
	l := &Listener{pktConn: packet, tr: tr, listener: ql, config: &Config{Auth: "fixture"}, quicParams: &internet.QuicParams{}, masqHandler: handler}
	t.Cleanup(func() { l.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(ctx, ql.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "example.com", NextProtos: []string{"h3"}}, &quic.Config{EnableDatagrams: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseWithError(0, "fixture finished") })
	return l, conn
}

func TestUpgradeHy2ListenerCloseNotifiesPeerAndJoinsHTTP3(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	l, peer := retirementQUICListener(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(exited)
	}))
	l.acceptDone = make(chan struct{})
	go l.keepAccepting()
	transport := &http3.Transport{}
	client := transport.NewClientConn(peer)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://fixture/hold", nil)
	if err != nil {
		t.Fatal(err)
	}
	response := make(chan error, 1)
	go func() {
		resp, err := client.RoundTrip(req)
		if resp != nil {
			resp.Body.Close()
		}
		response <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("HTTP3 handler did not enter")
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	select {
	case <-l.acceptDone:
	default:
		t.Error("listener returned before accept loop retirement")
	}
	select {
	case <-exited:
	default:
		t.Error("listener returned before HTTP3 request retirement")
	}
	select {
	case <-peer.Context().Done():
		var remote *quic.ApplicationError
		if !errors.As(context.Cause(peer.Context()), &remote) || !remote.Remote {
			t.Errorf("peer did not receive CONNECTION_CLOSE: %v", context.Cause(peer.Context()))
		}
	case <-ctx.Done():
		t.Error("peer still treats retired listener as live; no CONNECTION_CLOSE")
	}
	peer.CloseWithError(0, "test complete")
	<-response
}

func TestUpgradeHy2UDPFeedCallbackCloseAndLateAdmission(t *testing.T) {
	l, _ := retirementQUICListener(t, http.NotFoundHandler())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	server, err := l.listener.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer server.CloseWithError(0, "fixture finished")
	m := &udpSessionManager{conn: server, m: make(map[uint32]*InterConn)}
	called := false
	m.addConn = func(conn stat.Connection) {
		called = true
		if !m.TryLock() {
			t.Error("feed invokes callback while holding manager lock")
			return
		}
		m.Unlock()
		// Synchronous admission rejection must be permitted by the provider.
		conn.Close()
	}
	m.feed(1, []byte("first"))
	if !called || len(m.m) != 0 {
		t.Fatal("synchronous rejection did not retire session")
	}

	entered, release, finished := make(chan *InterConn, 1), make(chan struct{}), make(chan struct{})
	m.addConn = func(conn stat.Connection) {
		entered <- conn.(*InterConn)
		<-release
		conn.Close()
	}
	go func() { defer close(finished); m.feed(2, []byte("late")) }()
	conn := <-entered
	// Close wins after publication, before the external callback completes.
	m.Lock()
	m.closed = true
	for _, active := range m.m {
		m.close(active)
	}
	m.Unlock()
	close(release)
	<-finished
	if !conn.closed.Load() || len(m.m) != 0 {
		t.Fatal("late callback revived closed manager")
	}
	m.addConn = func(stat.Connection) { t.Error("closed manager admitted a session") }
	m.feed(3, []byte("rejected"))
	if len(m.m) != 0 {
		t.Fatal("closed manager retained new session")
	}
}
