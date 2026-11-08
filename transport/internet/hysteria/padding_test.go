package hysteria

import (
	"context"
	gotls "crypto/tls"
	"crypto/x509"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/apernet/quic-go/quicvarint"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
)

// Core spec025: the Hy2 padding switch on the authentication messages and on the streams the proxy writes its
// TCP headers on.

func paddingTLS(t *testing.T) (server, client *gotls.Config) {
	t.Helper()
	cert := httptest.NewTLSServer(nil)
	server = cert.TLS.Clone()
	server.NextProtos = []string{"h3"}
	client = cert.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	client.RootCAs = x509.NewCertPool()
	client.RootCAs.AddCert(cert.Certificate())
	client.ServerName = "example.com"
	cert.Close()
	return server, client
}

// checkPadding fails unless value is a padding of p: its length in p's half-open range, its characters Hy2's.
func checkPadding(t *testing.T, what, value string, p padding) {
	t.Helper()
	if len(value) < p.Min || len(value) >= p.Max || strings.Trim(value, paddingChars) != "" {
		t.Errorf("%s: a padding of %d characters, want %d to %d of %q", what, len(value), p.Min, p.Max-1, paddingChars)
	}
}

// FR-001: the authentication paddings keep today's ranges, reaching within 5 of both ends over 10 000 headers;
// with disablePadding the header is left out, not sent empty.
func TestAuthPaddingRanges(t *testing.T) {
	for name, p := range map[string]padding{"request": AuthRequestPadding, "response": AuthResponsePadding} {
		low, high := p.Max, 0
		for range 10000 {
			h := http.Header{}
			setPadding(h, p, &Config{})
			value := h.Get(CommonHeaderPadding)
			checkPadding(t, name, value, p)
			low, high = min(low, len(value)), max(high, len(value))
		}
		if p.Min != 256 || p.Max != 2048 || low > p.Min+5 || high < p.Max-1-5 {
			t.Errorf("%s: lengths %d to %d over 10000 headers, range [%d, %d)", name, low, high, p.Min, p.Max)
		}
		h := http.Header{}
		setPadding(h, p, &Config{DisablePadding: true})
		if _, ok := h[CommonHeaderPadding]; ok || len(h) != 0 {
			t.Errorf("%s with disablePadding: %v", name, h)
		}
	}
	if (TcpRequestPadding != padding{Min: 64, Max: 512}) || (TcpResponsePadding != padding{Min: 128, Max: 1024}) {
		t.Errorf("TCP header ranges %v, %v", TcpRequestPadding, TcpResponsePadding)
	}
}

// rawAuthServer answers Hy2 authentication over HTTP/3 and records each request's headers.
type rawAuthServer struct {
	mu      sync.Mutex
	headers []http.Header
	addr    *stdnet.UDPAddr
}

func newRawAuthServer(t *testing.T, serverTLS *gotls.Config) *rawAuthServer {
	t.Helper()
	packet, err := stdnet.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ql, err := quic.Listen(packet, serverTLS, &quic.Config{EnableDatagrams: true})
	if err != nil {
		packet.Close()
		t.Fatal(err)
	}
	s := &rawAuthServer{addr: packet.LocalAddr().(*stdnet.UDPAddr)}
	var servers sync.WaitGroup
	var conns []*quic.Conn
	t.Cleanup(func() {
		ql.Close()
		s.mu.Lock()
		for _, conn := range conns {
			conn.CloseWithError(0, "")
		}
		s.mu.Unlock()
		servers.Wait()
		packet.Close()
	})
	go func() {
		for {
			conn, err := ql.Accept(context.Background())
			if err != nil {
				return
			}
			s.mu.Lock()
			conns = append(conns, conn)
			s.mu.Unlock()
			servers.Add(1)
			go func() {
				defer servers.Done()
				h3 := &http3.Server{EnableDatagrams: true, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					s.mu.Lock()
					s.headers = append(s.headers, r.Header.Clone())
					s.mu.Unlock()
					w.Header().Set(CommonHeaderCCRX, "0")
					w.WriteHeader(StatusAuthOK)
				})}
				h3.ServeQUICConn(conn)
			}()
		}
	}()
	return s
}

func (s *rawAuthServer) recorded() []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.headers...)
}

// FR-001, FR-003 (client end): a real dialer's authentication request carries Hysteria-Padding as today, and
// none with disablePadding; the stream it opens tells the proxy to pad its TCP request, or not.
func TestAuthRequestPadding(t *testing.T) {
	serverTLS, clientTLS := paddingTLS(t)
	for _, off := range []bool{false, true} {
		server := newRawAuthServer(t, serverTLS)
		raw, err := stdnet.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		internet.UseAlternativeSystemDialer(upgradeDialer{dial: func(context.Context) (net.Conn, error) {
			return &net.PacketConnWrapper{PacketConn: raw, Dest: server.addr}, nil
		}})
		c := &client{
			lock: make(chan struct{}, 1), dest: net.UDPDestination(net.LocalHostIP, net.Port(server.addr.Port)),
			config: &Config{Auth: "fixture", DisablePadding: off}, tlsConfig: clientTLS.Clone(),
			quicParams: quietParams(nil), quicStats: &internet.QuicStatsSet{},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		conn, err := c.tcp(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if got := conn.(*interConn).HysteriaPadding(); got == off {
			t.Errorf("disablePadding %v: the stream says padding %v", off, got)
		}
		conn.Close()
		c.clean(true)
		internet.UseAlternativeSystemDialer(nil)
		raw.Close()

		headers := server.recorded()
		if len(headers) != 1 || headers[0].Get(RequestHeaderAuth) != "fixture" {
			t.Fatalf("disablePadding %v: requests %v", off, headers)
		}
		values, sent := headers[0][CommonHeaderPadding]
		switch {
		case off && sent:
			t.Errorf("disablePadding: the request carries %s (%d characters)", CommonHeaderPadding, len(strings.Join(values, "")))
		case !off && !sent:
			t.Errorf("default: the request has no %s", CommonHeaderPadding)
		case !off:
			checkPadding(t, "the authentication request", values[0], AuthRequestPadding)
		}
	}
}

// FR-001, FR-003 (server end): a real listener answers the first and a repeated authentication with
// Hysteria-Padding as today, and without it with disablePadding, read by a raw HTTP/3 client; the streams it
// hands to the proxy follow the inbound's setting.
func TestAuthResponsePadding(t *testing.T) {
	serverTLS, clientTLS := paddingTLS(t)
	for _, off := range []bool{false, true} {
		packet, err := stdnet.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		tr := &quic.Transport{Conn: packet}
		ql, err := tr.Listen(serverTLS, &quic.Config{
			EnableDatagrams: true, MaxDatagramFrameSize: MaxDatagramFrameSize,
			AssumePeerMaxDatagramFrameSize: MaxDatagramFrameSize, DisablePathManager: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		streams := make(chan bool, 4)
		l := &Listener{
			acceptDone: make(chan struct{}), pktConn: packet, tr: tr, listener: ql,
			config: &Config{Auth: "fixture", DisablePadding: off}, quicParams: &internet.QuicParams{}, masqHandler: http.NotFoundHandler(),
			quicStats: &internet.QuicStatsSet{},
			addConn: func(conn stat.Connection) {
				streams <- conn.(*interConn).HysteriaPadding()
				conn.Close()
			},
		}
		go l.keepAccepting()

		var qconn *quic.Conn
		dials := 0
		rt := &http3.Transport{
			TLSClientConfig: clientTLS.Clone(), QUICConfig: &quic.Config{EnableDatagrams: true},
			Dial: func(ctx context.Context, _ string, tlsCfg *gotls.Config, cfg *quic.Config) (*quic.Conn, error) {
				dials++
				qc, err := quic.DialAddrEarly(ctx, packet.LocalAddr().String(), tlsCfg, cfg)
				qconn = qc
				return qc, err
			},
		}
		for i := range 2 {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			req := (&http.Request{
				Method: http.MethodPost, URL: &url.URL{Scheme: "https", Host: URLHost, Path: URLPath},
				Header: http.Header{RequestHeaderAuth: []string{"fixture"}},
			}).WithContext(ctx)
			resp, err := rt.RoundTrip(req)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			values, sent := resp.Header[CommonHeaderPadding]
			what := []string{"the first authentication", "a repeated authentication"}[i]
			switch {
			case resp.StatusCode != StatusAuthOK:
				t.Fatalf("%s: status %d", what, resp.StatusCode)
			case off && sent:
				t.Errorf("disablePadding: %s answered with %s (%d characters)", what, CommonHeaderPadding, len(strings.Join(values, "")))
			case !off && !sent:
				t.Errorf("default: %s answered without %s", what, CommonHeaderPadding)
			case !off:
				checkPadding(t, what, values[0], AuthResponsePadding)
			}
		}
		if dials != 1 {
			t.Fatalf("the repeated authentication took %d connections", dials)
		}
		stream, err := qconn.OpenStream()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write(quicvarint.Append(nil, FrameTypeTCPRequest)); err != nil {
			t.Fatal(err)
		}
		select {
		case padded := <-streams:
			if padded == off {
				t.Errorf("disablePadding %v: the server's stream says padding %v", off, padded)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("no stream reached the inbound")
		}
		rt.Close()
		l.Close()
	}
}
