package hysteria

import (
	"context"
	"crypto/x509"
	"errors"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apernet/quic-go/http3"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
)

type upgradeDialer struct {
	dial func(context.Context) (net.Conn, error)
}

func (d upgradeDialer) Dial(ctx context.Context, _ net.Address, _ net.Destination, _ *internet.SocketConfig) (net.Conn, error) {
	return d.dial(ctx)
}
func (upgradeDialer) DestIpAddress() net.IP { return nil }

func TestUpgradeHy2FailedAuthenticationReleasesSockets(t *testing.T) {
	for _, mode := range []string{"forbidden", "untrusted", "canceled"} {
		t.Run(mode, func(t *testing.T) { testUpgradeHy2FailedAuthentication(t, mode) })
	}
}

func testUpgradeHy2FailedAuthentication(t *testing.T, mode string) {
	t.Helper()
	cert := httptest.NewTLSServer(nil)
	serverTLS := cert.TLS.Clone()
	clientTLS := cert.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	clientTLS.RootCAs = x509.NewCertPool()
	clientTLS.RootCAs.AddCert(cert.Certificate())
	if mode == "untrusted" {
		clientTLS.RootCAs = x509.NewCertPool()
	}
	clientTLS.ServerName = "example.com"
	cert.Close()
	packet, err := stdnet.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	server := &http3.Server{TLSConfig: serverTLS, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if mode == "canceled" {
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(packet) }()
	t.Cleanup(func() {
		server.Close()
		packet.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("HTTP3 fixture did not stop")
		}
	})
	defer internet.UseAlternativeSystemDialer(nil)
	var raw *stdnet.UDPConn
	internet.UseAlternativeSystemDialer(upgradeDialer{dial: func(context.Context) (net.Conn, error) {
		var err error
		raw, err = stdnet.ListenUDP("udp4", &stdnet.UDPAddr{IP: stdnet.IPv4(127, 0, 0, 1)})
		if err != nil {
			return nil, err
		}
		return &net.PacketConnWrapper{PacketConn: raw, Dest: packet.LocalAddr()}, nil
	}})
	c := &client{lock: make(chan struct{}, 1), dest: net.UDPDestination(net.LocalHostIP, net.Port(packet.LocalAddr().(*stdnet.UDPAddr).Port)), config: &Config{Auth: "invalid"}, tlsConfig: clientTLS, quicParams: &internet.QuicParams{DisableChromeParrot: true}}
	for i := 0; i < 100; i++ {
		timeout := time.Second
		if mode == "canceled" {
			timeout = 100 * time.Millisecond
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_, err := c.tcp(ctx)
		ctxErr := ctx.Err()
		cancel()
		if err == nil {
			t.Fatal("invalid authentication accepted")
		}
		if mode == "canceled" && !errors.Is(ctxErr, context.DeadlineExceeded) {
			t.Fatalf("authentication ended before cancellation: %v (context %v)", err, ctxErr)
		}
		if raw == nil {
			t.Fatal("no socket opened")
		}
		if _, err := raw.WriteTo([]byte("closed"), packet.LocalAddr()); !errors.Is(err, stdnet.ErrClosed) {
			raw.Close()
			t.Fatalf("attempt %d retained UDP socket: %v", i, err)
		}
		c.clean(false)
		if c.conn != nil || c.tr != nil || c.pktConn != nil || c.udpSM != nil {
			t.Fatal("failed authentication retained transport")
		}
	}
	want := int64(100)
	if mode == "untrusted" {
		want = 0
	}
	if requests.Load() != want {
		t.Fatalf("authenticated TLS requests %d, want %d", requests.Load(), want)
	}
}

func TestUpgradeHy2LockCancellationAndManagerProgress(t *testing.T) {
	c := &client{lock: make(chan struct{}, 1)}
	c.lock <- struct{}{}
	for _, datagram := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		var err error
		if datagram {
			_, err = c.udp(ctx)
		} else {
			_, err = c.tcp(ctx)
		}
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("pending admission: %v", err)
		}
	}
	m := &clientManager{m: map[dialerConf]*client{{}: c}}
	done := make(chan struct{})
	go func() { m.cleanOnce(); close(done) }()
	acquired := make(chan struct{})
	go func() { m.Lock(); m.Unlock(); close(acquired) }()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Error("per-client cleanup blocked registry")
	}
	c.unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not finish")
	}
}

func TestUpgradeHy2StoppedInstanceRetiresClient(t *testing.T) {
	instance, err := core.New(&core.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	c := &client{lock: make(chan struct{}, 1), instance: instance}
	m := &clientManager{m: map[dialerConf]*client{{}: c}}
	m.cleanOnce()
	if len(m.m) != 0 || !c.forced {
		t.Fatal("stopped instance retained dialable client")
	}
	if _, err := c.tcp(context.Background()); err == nil {
		t.Fatal("retired client accepted connection")
	}
}

func TestUpgradeHy2UnsupportedConnectionClosed(t *testing.T) {
	defer internet.UseAlternativeSystemDialer(nil)
	left, right := stdnet.Pipe()
	defer left.Close()
	defer right.Close()
	internet.UseAlternativeSystemDialer(upgradeDialer{dial: func(context.Context) (net.Conn, error) { return left, nil }})
	c := &client{lock: make(chan struct{}, 1), dest: net.UDPDestination(net.LocalHostIP, 9)}
	if _, err := c.tcp(context.Background()); err == nil {
		t.Fatal("unsupported connection accepted")
	}
	right.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := right.Read(b[:]); err == nil {
		t.Fatal("raw connection remains open")
	} else if e, ok := err.(stdnet.Error); ok && e.Timeout() {
		t.Fatal("unsupported raw connection leaked")
	}
}
