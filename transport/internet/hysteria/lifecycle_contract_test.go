package hysteria

import (
	"context"
	"crypto/tls"
	"errors"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apernet/quic-go/http3"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
)

func TestClientContractAuthenticationFailureClosesPacketConn(t *testing.T) {
	certificateServer := httptest.NewTLSServer(nil)
	serverTLS := certificateServer.TLS.Clone()
	certificateServer.Close()
	packet, err := stdnet.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var authentications atomic.Int64
	server := &http3.Server{TLSConfig: serverTLS, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == URLPath && r.Header.Get(RequestHeaderAuth) == "test-only-wrong" {
			authentications.Add(1)
		}
		w.WriteHeader(http.StatusForbidden)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(packet) }()
	defer func() {
		server.Close()
		packet.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("HTTP3 server did not exit")
		}
	}()
	defer internet.UseAlternativeSystemDialer(nil)
	var raw *stdnet.UDPConn
	internet.UseAlternativeSystemDialer(contractSystemDialer{dial: func() (net.Conn, error) {
		var err error
		raw, err = stdnet.ListenUDP("udp4", &stdnet.UDPAddr{IP: stdnet.IPv4(127, 0, 0, 1)})
		if err != nil {
			return nil, err
		}
		return &internet.PacketConnWrapper{PacketConn: raw, Dest: packet.LocalAddr()}, nil
	}})
	c := &client{ctx: context.Background(), dest: net.UDPDestination(net.LocalHostIP, net.Port(packet.LocalAddr().(*stdnet.UDPAddr).Port)), config: &Config{Auth: "test-only-wrong"}, tlsConfig: &tls.Config{InsecureSkipVerify: true}}
	for round := 0; round < 100; round++ {
		if _, err := c.tcp(); err == nil {
			t.Fatal("HTTP 403 authentication accepted")
		} else if round == 0 {
			t.Logf("authentication rejection: %v", err)
		}
		if raw == nil {
			t.Fatal("no packet socket created")
		}
		if _, err := raw.WriteTo([]byte("closed"), packet.LocalAddr()); !errors.Is(err, stdnet.ErrClosed) {
			raw.Close()
			t.Fatalf("failed auth leaked UDP socket: %v", err)
		}
		c.clean()
		c.clean()
		if c.conn != nil || c.pktConn != nil || c.udpSM != nil {
			t.Fatal("failed authentication retained resources")
		}
	}
	if got := authentications.Load(); got != 100 {
		t.Fatalf("authentication handler requests = %d, want 100", got)
	}
	c.tlsConfig = &tls.Config{ServerName: "untrusted.invalid"}
	for round := 0; round < 100; round++ {
		if _, err := c.tcp(); err == nil {
			t.Fatal("untrusted TLS accepted")
		}
		if _, err := raw.WriteTo([]byte("closed"), packet.LocalAddr()); !errors.Is(err, stdnet.ErrClosed) {
			raw.Close()
			t.Fatalf("handshake failure leaked UDP socket: %v", err)
		}
		c.clean()
		c.clean()
	}
	if got := authentications.Load(); got != 100 {
		t.Fatalf("untrusted TLS reached authentication handler: requests=%d", got)
	}
	t.Log("100 rejected HTTP3 auth requests and 100 rejected TLS handshakes; all UDP sockets closed")
}

type contractSystemDialer struct{ dial func() (net.Conn, error) }

func (d contractSystemDialer) Dial(context.Context, net.Address, net.Destination, *internet.SocketConfig) (net.Conn, error) {
	return d.dial()
}
func (contractSystemDialer) DestIpAddress() net.IP { return nil }

func TestClientContractFailedCreationCleanup(t *testing.T) {
	defer internet.UseAlternativeSystemDialer(nil)
	for round := 0; round < 100; round++ {
		m := &clientManager{m: make(map[dialerConf]*client)}
		c := m.getOrCreate(dialerConf{}, func() *client {
			return &client{ctx: context.Background(), dest: net.UDPDestination(net.LocalHostIP, 9)}
		})
		internet.UseAlternativeSystemDialer(contractSystemDialer{dial: func() (net.Conn, error) { return nil, errors.New("injected dial failure") }})
		if _, err := c.tcp(); err == nil {
			t.Fatal("dial failure swallowed")
		}
		left, right := stdnet.Pipe()
		internet.UseAlternativeSystemDialer(contractSystemDialer{dial: func() (net.Conn, error) { return left, nil }})
		if _, err := c.tcp(); err == nil {
			left.Close()
			right.Close()
			t.Fatal("unsupported connection accepted")
		}
		right.SetReadDeadline(time.Now().Add(time.Second))
		var b [1]byte
		_, err := right.Read(b[:])
		right.Close()
		if err == nil {
			t.Fatal("failed setup left connection open")
		}
		if e, ok := err.(stdnet.Error); ok && e.Timeout() {
			t.Fatal("failed setup leaked raw connection")
		}
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); m.clean() }()
		}
		wg.Wait()
		if c.conn != nil || c.pktConn != nil || c.udpSM != nil {
			t.Fatal("failed setup retained resources")
		}
		if got := m.getOrCreate(dialerConf{}, func() *client { t.Error("cached client unexpectedly replaced"); return nil }); got != c {
			t.Fatal("failed client not reusable")
		}
	}
}

func TestClientContractUDPFailureDoesNotPanic(t *testing.T) {
	defer internet.UseAlternativeSystemDialer(nil)
	internet.UseAlternativeSystemDialer(contractSystemDialer{dial: func() (net.Conn, error) { return nil, errors.New("injected hop failure") }})
	// A disconnected client must reject a hop before attempting a system dial.
	c := &client{}
	if _, err := c.udphopDialer(&net.UDPAddr{IP: net.IP{127, 0, 0, 1}, Port: 9}); err == nil {
		t.Fatal("inactive client accepted hop")
	}
}

func TestClientContractOtherConfigurationProgress(t *testing.T) {
	for _, operation := range []string{"setCtx", "clean"} {
		t.Run(operation, func(t *testing.T) {
			c := &client{}
			c.mutex.Lock()
			var release sync.Once
			unlock := func() { release.Do(c.mutex.Unlock) }
			defer unlock()
			m := &clientManager{m: map[dialerConf]*client{{}: c}}
			entered, done := make(chan struct{}), make(chan struct{})
			go func() {
				close(entered)
				if operation == "clean" {
					m.clean()
				} else {
					m.getOrCreate(dialerConf{}, func() *client { return nil }).setCtx(context.Background())
				}
				close(done)
			}()
			<-entered
			other := make(chan *client, 1)
			go func() {
				other <- m.getOrCreate(dialerConf{Destination: net.UDPDestination(net.LocalHostIP, 42)}, func() *client { return &client{} })
			}()
			select {
			case got := <-other:
				if got == nil {
					t.Fatal("missing other client")
				}
			case <-time.After(time.Second):
				t.Fatal("other configuration blocked by client lock")
			}
			unlock()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("client operation did not finish")
			}
		})
	}
}
