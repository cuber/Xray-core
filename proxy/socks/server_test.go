package socks

import (
	stdnet "net"
	"testing"

	"github.com/xtls/xray-core/common/net"
)

func TestServerNetworks(t *testing.T) {
	tests := []struct {
		name       string
		udpEnabled bool
		want       []net.Network
	}{
		{
			name: "stream networks",
			want: []net.Network{net.Network_TCP, net.Network_UNIX},
		},
		{
			name:       "stream and udp networks",
			udpEnabled: true,
			want:       []net.Network{net.Network_TCP, net.Network_UNIX, net.Network_UDP},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{config: &ServerConfig{UdpEnabled: test.udpEnabled}}
			got := server.Network()
			if len(got) != len(test.want) {
				t.Fatalf("Network() = %v, want %v", got, test.want)
			}
			for i := range test.want {
				if got[i] != test.want[i] {
					t.Fatalf("Network() = %v, want %v", got, test.want)
				}
			}
		})
	}
}

func TestSocksLocalAddress(t *testing.T) {
	tests := []struct {
		name string
		addr stdnet.Addr
		want string
	}{
		{
			name: "tcp",
			addr: &stdnet.TCPAddr{IP: stdnet.ParseIP("192.0.2.1"), Port: 1080},
			want: "192.0.2.1",
		},
		{
			name: "unix",
			addr: &stdnet.UnixAddr{Name: "/tmp/xray-socks.sock", Net: "unix"},
			want: net.LocalHostIP.String(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := socksLocalAddress(test.addr).String(); got != test.want {
				t.Fatalf("socksLocalAddress() = %q, want %q", got, test.want)
			}
		})
	}
}
