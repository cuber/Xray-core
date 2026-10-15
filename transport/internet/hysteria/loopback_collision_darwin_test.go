package hysteria

import (
	"context"
	"crypto/x509"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/apernet/quic-go/http3"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
)

type collisionSystemDialer struct {
	wildcard stdnet.PacketConn
	remote   stdnet.Addr
}

func (d collisionSystemDialer) Dial(ctx context.Context, src net.Address, dest net.Destination, sockopt *internet.SocketConfig) (net.Conn, error) {
	if src == nil {
		return &net.PacketConnWrapper{PacketConn: d.wildcard, Dest: d.remote}, nil
	}
	return new(internet.DefaultSystemDialer).Dial(ctx, src, dest, sockopt)
}

func (collisionSystemDialer) DestIpAddress() net.IP { return nil }

func TestHy2ChromeParrotSamePortLoopbackCollision(t *testing.T) {
	// Fix the naturally observed Darwin wildcard/specific-address collision
	// without depending on the ephemeral allocator choosing the same port.
	lc := stdnet.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var optionErr error
		err := raw.Control(func(fd uintptr) {
			optionErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
		})
		if err != nil {
			return err
		}
		return optionErr
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	wildcard, err := lc.ListenPacket(ctx, "udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer wildcard.Close()
	port := wildcard.LocalAddr().(*stdnet.UDPAddr).Port
	packet, err := lc.ListenPacket(ctx, "udp4", (&stdnet.UDPAddr{IP: stdnet.IPv4(127, 0, 0, 1), Port: port}).String())
	if err != nil {
		t.Fatal("cannot establish deterministic same-port fixture:", err)
	}
	cert := httptest.NewTLSServer(nil)
	serverTLS := cert.TLS.Clone()
	clientTLS := cert.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	clientTLS.RootCAs = x509.NewCertPool()
	clientTLS.RootCAs.AddCert(cert.Certificate())
	clientTLS.ServerName = "127.0.0.1"
	cert.Close()
	var authenticated atomic.Int64
	server := &http3.Server{TLSConfig: serverTLS, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Host != URLHost || r.URL.Path != URLPath || r.Header.Get(RequestHeaderAuth) != "fixture" {
			http.Error(w, "invalid fixture authentication", http.StatusForbidden)
			return
		}
		authenticated.Add(1)
		w.WriteHeader(StatusAuthOK)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(packet) }()
	defer func() { server.Close(); packet.Close(); <-done }()
	internet.UseAlternativeSystemDialer(collisionSystemDialer{wildcard: wildcard, remote: packet.LocalAddr()})
	defer internet.UseAlternativeSystemDialer(nil)
	c := &client{lock: make(chan struct{}, 1), dest: net.UDPDestination(net.LocalHostIP, net.Port(port)), config: &Config{Auth: "fixture"}, tlsConfig: clientTLS}
	defer c.clean(true)
	conn, err := c.tcp(ctx)
	if err != nil {
		t.Fatalf("Hy2 auth with occupied wildcard/server port %d: %v (auth requests=%d)", port, err, authenticated.Load())
	}
	defer conn.Close()
	local := c.pktConn.LocalAddr().(*stdnet.UDPAddr)
	if local.Port == port || !local.IP.Equal(stdnet.IPv4(127, 0, 0, 1)) || authenticated.Load() != 1 {
		t.Fatalf("unexpected authenticated endpoints: local=%s remote=%s requests=%d", local, packet.LocalAddr(), authenticated.Load())
	}
}
