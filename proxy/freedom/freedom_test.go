package freedom

import (
	"context"
	stdnet "net"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet/stat"
	"github.com/xtls/xray-core/transport/pipe"
)

func TestIsStreamNetwork(t *testing.T) {
	tests := []struct {
		network net.Network
		want    bool
	}{
		{network: net.Network_TCP, want: true},
		{network: net.Network_UNIX, want: true},
		{network: net.Network_UDP, want: false},
		{network: net.Network_Unknown, want: false},
	}

	for _, tt := range tests {
		if got := isStreamNetwork(tt.network); got != tt.want {
			t.Fatalf("isStreamNetwork(%s) = %v, want %v", tt.network, got, tt.want)
		}
	}
}

func TestProcessRedirectsToUnixDestination(t *testing.T) {
	const socketPath = "/run/xray-sidecar/probe.sock"

	handler := new(Handler)
	if err := handler.Init(&Config{
		DestinationOverride: &DestinationOverride{
			Network: net.Network_UNIX,
			Server: &protocol.ServerEndpoint{
				Address: net.NewIPOrDomain(net.DomainAddress(socketPath)),
			},
		},
	}, testPolicyManager{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	requestReader, requestWriter := pipe.New()
	responseReader, responseWriter := pipe.New()
	_ = requestWriter.Close()
	defer responseReader.Interrupt()

	dialer := &captureDialer{dest: make(chan net.Destination, 1)}
	ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{
		Target: net.TCPDestination(net.LocalHostIP, 8080),
	}})

	done := make(chan error, 1)
	go func() {
		done <- handler.Process(ctx, &transport.Link{
			Reader: requestReader,
			Writer: responseWriter,
		}, dialer)
	}()

	select {
	case got := <-dialer.dest:
		if got.Network != net.Network_UNIX {
			t.Fatalf("dial network = %s, want unix", got.Network)
		}
		if got.Address.String() != socketPath {
			t.Fatalf("dial address = %q, want %q", got.Address.String(), socketPath)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dial")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Process() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Process did not finish")
	}
}

type testPolicyManager struct{}

func (testPolicyManager) Type() interface{} {
	return policy.ManagerType()
}

func (testPolicyManager) Start() error {
	return nil
}

func (testPolicyManager) Close() error {
	return nil
}

func (testPolicyManager) ForLevel(uint32) policy.Session {
	p := policy.SessionDefault()
	p.Timeouts.ConnectionIdle = time.Second
	p.Timeouts.UplinkOnly = time.Millisecond
	p.Timeouts.DownlinkOnly = time.Millisecond
	return p
}

func (testPolicyManager) ForSystem() policy.System {
	return policy.System{}
}

type captureDialer struct {
	dest chan net.Destination
}

func (d *captureDialer) Dial(_ context.Context, dest net.Destination) (stat.Connection, error) {
	d.dest <- dest
	client, server := stdnet.Pipe()
	_ = server.Close()
	return testConn{Conn: client}, nil
}

func (d *captureDialer) DestIpAddress() net.IP {
	return nil
}

func (d *captureDialer) SetOutboundGateway(context.Context, *session.Outbound) {}

type testConn struct {
	stdnet.Conn
}

func (testConn) LocalAddr() stdnet.Addr {
	return &stdnet.UnixAddr{Name: "/tmp/local.sock", Net: "unix"}
}

func (testConn) RemoteAddr() stdnet.Addr {
	return &stdnet.UnixAddr{Name: "/tmp/remote.sock", Net: "unix"}
}
