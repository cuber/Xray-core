package hysteria

import (
	"context"
	"crypto/x509"
	"errors"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/apernet/quic-go/http3"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
)

// Dispatch-backed packet connections cannot implement read deadlines.
type retirementPacketConn struct{ stdnet.PacketConn }

func (*retirementPacketConn) SetReadDeadline(time.Time) error { return nil }
func (*retirementPacketConn) SetDeadline(time.Time) error     { return nil }

func TestHy2OwnerCloseCancelsPendingDial(t *testing.T) {
	owner := new(core.Instance)
	c := &client{lock: make(chan struct{}, 1), dest: net.UDPDestination(net.LocalHostIP, 9)}
	c.ownerContext, c.ownerCancel = context.WithCancel(context.Background())
	owner.OnClose(func() { c.clean(true) })
	entered := make(chan struct{})
	internet.UseAlternativeSystemDialer(upgradeDialer{dial: func(ctx context.Context) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	defer internet.UseAlternativeSystemDialer(nil)
	finished := make(chan error, 1)
	go func() { _, err := c.tcp(context.Background()); finished <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("dial did not enter")
	}
	closed := make(chan struct{})
	go func() { owner.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		c.ownerCancel()
		t.Fatal("retirement waited for admission without canceling it")
	}
	if err := <-finished; err == nil {
		t.Fatal("pending dial succeeded after retirement")
	}
}

func TestHy2OwnerCloseJoinsNoDeadlineTransport(t *testing.T) {
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
	server := &http3.Server{TLSConfig: serverTLS, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(StatusAuthOK) })}
	done := make(chan error, 1)
	go func() { done <- server.Serve(packet) }()
	defer func() { server.Close(); packet.Close(); <-done }()
	raw, err := stdnet.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	internet.UseAlternativeSystemDialer(upgradeDialer{dial: func(context.Context) (net.Conn, error) {
		return &net.PacketConnWrapper{PacketConn: &retirementPacketConn{raw}, Dest: packet.LocalAddr()}, nil
	}})
	defer internet.UseAlternativeSystemDialer(nil)
	owner := new(core.Instance)
	c := &client{lock: make(chan struct{}, 1), dest: net.UDPDestination(net.LocalHostIP, net.Port(packet.LocalAddr().(*stdnet.UDPAddr).Port)), config: &Config{Auth: "fixture"}, tlsConfig: clientTLS, quicParams: &internet.QuicParams{DisableChromeParrot: true}}
	c.ownerContext, c.ownerCancel = context.WithCancel(context.Background())
	owner.OnClose(func() { c.clean(true) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := c.tcp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	closed := make(chan struct{})
	go func() { owner.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		raw.Close()
		<-closed
		t.Fatal("owner Close blocked waiting for reader without deadlines")
	}
	if _, err := raw.WriteTo([]byte("closed"), packet.LocalAddr()); !errors.Is(err, stdnet.ErrClosed) {
		t.Fatalf("owner Close retained socket: %v", err)
	}
	if c.conn != nil || c.tr != nil || c.pktConn != nil || c.udpSM != nil || !c.forced {
		t.Fatal("owner Close retained transport state")
	}
}
