package hysteria

import (
	"context"
	"crypto/x509"
	"fmt"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
)

type handshakeLossPacketConn struct {
	stdnet.PacketConn
	dropped atomic.Bool
}

func TestHy2ChromeParrotFreshListenerHandshake(t *testing.T) {
	cert := httptest.NewTLSServer(nil)
	serverTLS := cert.TLS.Clone()
	serverTLS.NextProtos = []string{"h3"}
	clientTLS := cert.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	clientTLS.RootCAs = x509.NewCertPool()
	clientTLS.RootCAs.AddCert(cert.Certificate())
	clientTLS.ServerName = "127.0.0.1"
	cert.Close()
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
		tr.Close()
		packet.Close()
		t.Fatal(err)
	}
	l := &Listener{
		acceptDone: make(chan struct{}), pktConn: packet, tr: tr, listener: ql,
		config: &Config{Auth: "fixture"}, quicParams: &internet.QuicParams{}, masqHandler: http.NotFoundHandler(),
	}
	go l.keepAccepting()
	defer l.Close()
	c := &client{lock: make(chan struct{}, 1), dest: net.UDPDestination(net.LocalHostIP, net.Port(packet.LocalAddr().(*stdnet.UDPAddr).Port)), config: &Config{Auth: "fixture"}, tlsConfig: clientTLS}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, err := c.tcp(ctx)
	defer c.clean(true)
	if err != nil {
		t.Fatal(err)
	}
	local := c.pktConn.LocalAddr().(*stdnet.UDPAddr)
	if !local.IP.Equal(stdnet.IPv4(127, 0, 0, 1)) || local.Port == packet.LocalAddr().(*stdnet.UDPAddr).Port {
		t.Errorf("ambiguous loopback endpoints: local=%s remote=%s", local, packet.LocalAddr())
	}
	conn.Close()
}

func (p *handshakeLossPacketConn) ReadFrom(b []byte) (int, stdnet.Addr, error) {
	for {
		n, addr, err := p.PacketConn.ReadFrom(b)
		if err != nil {
			return n, addr, err
		}
		// QUIC v1 Initial: long header, fixed bit, packet type 00.
		if n > 0 && b[0]&0xf0 == 0xc0 && p.dropped.CompareAndSwap(false, true) {
			continue
		}
		return n, addr, nil
	}
}

func TestHy2ChromeParrotLostServerInitial(t *testing.T) {
	cert := httptest.NewTLSServer(nil)
	serverTLS := cert.TLS.Clone()
	clientTLS := cert.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	clientTLS.RootCAs = x509.NewCertPool()
	clientTLS.RootCAs.AddCert(cert.Certificate())
	clientTLS.ServerName = "example.com"
	cert.Close()
	packet, err := stdnet.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http3.Server{TLSConfig: serverTLS, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(StatusAuthOK)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(packet) }()
	defer func() { server.Close(); packet.Close(); <-done }()
	raw, err := stdnet.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	loss := &handshakeLossPacketConn{PacketConn: raw}
	defer loss.Close()
	internet.UseAlternativeSystemDialer(upgradeDialer{dial: func(context.Context) (net.Conn, error) {
		return &net.PacketConnWrapper{PacketConn: loss, Dest: packet.LocalAddr()}, nil
	}})
	defer internet.UseAlternativeSystemDialer(nil)
	c := &client{lock: make(chan struct{}, 1), dest: net.UDPDestination(net.LocalHostIP, net.Port(packet.LocalAddr().(*stdnet.UDPAddr).Port)), config: &Config{Auth: "fixture"}, tlsConfig: clientTLS}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, err := c.tcp(ctx)
	defer c.clean(true)
	if err != nil {
		t.Fatal(fmt.Errorf("auth after one lost server Initial: %w", err))
	}
	defer conn.Close()
	if !loss.dropped.Load() {
		t.Fatal("fixture did not drop the server Initial")
	}
}
