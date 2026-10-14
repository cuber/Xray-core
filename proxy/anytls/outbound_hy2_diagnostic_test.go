package anytls_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	applog "github.com/xtls/xray-core/app/log"
	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	corelog "github.com/xtls/xray-core/common/log"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport/internet"
)

// Return the exact original connection: wrapping PacketConn (even inside a
// PacketConnWrapper) can hide UDPConn/OOB interfaces and change QUIC's path.
type hy2DiagnosticDialer struct {
	internet.DefaultSystemDialer
	mu          sync.Mutex
	started     time.Time
	records     []string
	occupied    map[int]*net.UDPConn
	workers     sync.WaitGroup
	connections []net.Conn
	closed      bool
}

func (d *hy2DiagnosticDialer) record(s string) {
	d.mu.Lock()
	d.records = append(d.records, fmt.Sprintf("%s %s", time.Since(d.started), s))
	d.mu.Unlock()
}

func (d *hy2DiagnosticDialer) closeConnections() {
	d.mu.Lock()
	d.closed = true
	connections := append([]net.Conn(nil), d.connections...)
	d.connections = nil
	d.mu.Unlock()
	for _, conn := range connections {
		conn.Close()
	}
}

func (d *hy2DiagnosticDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, options *internet.SocketConfig) (net.Conn, error) {
	c, err := d.DefaultSystemDialer.Dial(ctx, source, dest, options)
	if c != nil && dest.Network == xnet.Network_UDP {
		d.mu.Lock()
		if d.closed {
			c.Close()
		} else {
			d.connections = append(d.connections, c)
		}
		d.mu.Unlock()
	}
	s := fmt.Sprintf("dial network=%s source=%v destination=%s error=%v", dest.Network, source, dest, err)
	if c != nil {
		s += fmt.Sprintf(" local=%s remote=%s type=%T", c.LocalAddr(), c.RemoteAddr(), c)
		if packet, ok := c.(*internet.PacketConnWrapper); ok {
			s += fmt.Sprintf(" packetType=%T", packet.PacketConn)
		}
		if local, ok := c.LocalAddr().(*net.UDPAddr); ok {
			if occupied := d.occupied[local.Port]; occupied != nil {
				s += fmt.Sprintf(" COLLISION existingOwnedIPv4=%s", occupied.LocalAddr())
			}
		}
	}
	d.record(s)
	return c, err
}

type hy2DiagnosticLog struct {
	corelog.Handler
	dialer *hy2DiagnosticDialer
}

func (l *hy2DiagnosticLog) Handle(message corelog.Message) {
	if _, ok := message.(*corelog.GeneralMessage); ok {
		l.dialer.record(message.String())
	}
	l.Handler.Handle(message)
}

// This fixture must not run in parallel: both hooks are process-global. Core
// is closed before restoring them, including on a failed first packet.
func installHy2Diagnostic(t *testing.T, f *integrationFixture, endpoint net.Addr) func() {
	t.Helper()
	// A/B control changes only the real native hop's source binding, before
	// its first dial. The boundary fixture saves this config for recreation.
	if os.Getenv("ANYTLS_HY2_BIND_IPV4") != "0" {
		manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
		hop := manager.GetHandler("hop")
		sender, err := hop.SenderSettings().GetInstance()
		if err != nil {
			t.Fatal(err)
		}
		sender.(*proxyman.SenderConfig).Via = xnet.NewIPOrDomain(xnet.ParseAddress("127.0.0.1"))
		config := &core.OutboundHandlerConfig{Tag: "hop", SenderSettings: serial.ToTypedMessage(sender), ProxySettings: hop.ProxySettings()}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "hop"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
			t.Fatal(err)
		}
	}
	d := &hy2DiagnosticDialer{started: time.Now(), occupied: make(map[int]*net.UDPConn)}
	d.record(fmt.Sprintf("business UDP endpoint=%s hopIPv4Control=%s", endpoint, os.Getenv("ANYTLS_HY2_BIND_IPV4")))
	logger := f.instance.GetFeature((*applog.Instance)(nil)).(*applog.Instance)
	internet.UseAlternativeSystemDialer(d)
	corelog.RegisterHandler(&hy2DiagnosticLog{Handler: logger, dialer: d})
	t.Cleanup(func() {
		f.instance.Close()
		// Hy2's process-global client manager is not owned by this Core
		// instance. Do not let its delayed QUIC packets reach a later fixture
		// reusing the server port. This is test isolation, not a shutdown oracle.
		d.closeConnections()
		internet.UseAlternativeSystemDialer(nil)
		corelog.RegisterHandler(logger)
		for _, socket := range d.occupied {
			socket.Close()
		}
		d.workers.Wait()
		if t.Failed() || os.Getenv("ANYTLS_HY2_DIAGNOSTIC") != "" {
			d.mu.Lock()
			records := append([]string(nil), d.records...)
			d.mu.Unlock()
			for _, record := range records {
				t.Log(record)
			}
		}
	})
	// Opt-in collision pressure, never a business retry. These sockets bind
	// only loopback and record datagrams without echoing or forwarding them.
	if value := os.Getenv("ANYTLS_HY2_OCCUPIED_PORTS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 64 || runtime.GOOS != "darwin" {
			t.Fatal("ANYTLS_HY2_OCCUPIED_PORTS requires Darwin and a count in [1,64]")
		}
		for range n {
			socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			d.occupied[socket.LocalAddr().(*net.UDPAddr).Port] = socket
			d.workers.Add(1)
			go func() {
				defer d.workers.Done()
				packet := make([]byte, 65535)
				for {
					n, source, err := socket.ReadFromUDP(packet)
					if err != nil {
						return
					}
					d.record(fmt.Sprintf("OWNED-IPv4-RECEIVE local=%s source=%s bytes=%d prefix=%x", socket.LocalAddr(), source, n, packet[:min(n, 16)]))
				}
			}()
		}
		d.record(fmt.Sprintf("owned loopback UDP listeners=%d", n))
	}
	return d.closeConnections
}
