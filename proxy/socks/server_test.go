package socks

import (
	"bytes"
	stdnet "net"
	"testing"

	"github.com/xtls/xray-core/common/net"
)

type handshakeReplyConn struct {
	stdnet.Conn
	reply bytes.Buffer
}

func (c *handshakeReplyConn) Write(b []byte) (int, error) { return c.reply.Write(b) }
func (*handshakeReplyConn) LocalAddr() stdnet.Addr {
	return &stdnet.UnixAddr{Name: "/tmp/socks-server", Net: "unix"}
}

func (*handshakeReplyConn) RemoteAddr() stdnet.Addr {
	return &stdnet.UnixAddr{Name: "/tmp/socks-client", Net: "unix"}
}

func TestSocksUnixUDPRejected(t *testing.T) {
	s := &ServerSession{config: &ServerConfig{UdpEnabled: true}}
	c := new(handshakeReplyConn)
	request := bytes.NewReader([]byte{0, 5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	header, udp, err := s.handshake5(1, request, c)
	if err == nil || header != nil || udp != nil {
		t.Fatalf("Unix UDP association accepted: %v, %v, %v", header, udp, err)
	}
	want := []byte{5, 0, 5, 7, 0, 1, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(c.reply.Bytes(), want) {
		t.Fatalf("unexpected rejection response: %v", c.reply.Bytes())
	}
}

type wrappedUnixReplyConn struct{ handshakeReplyConn }

func (*wrappedUnixReplyConn) RemoteAddr() stdnet.Addr {
	return &stdnet.TCPAddr{IP: stdnet.IPv4zero}
}

func TestSocksWrappedUnixUDPRejected(t *testing.T) {
	s := &ServerSession{config: &ServerConfig{UdpEnabled: true}}
	c := new(wrappedUnixReplyConn)
	request := bytes.NewReader([]byte{0, 5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	header, udp, err := s.handshake5(1, request, c)
	if err == nil || header != nil || udp != nil {
		t.Fatalf("wrapped Unix UDP association accepted: %v, %v, %v", header, udp, err)
	}
	want := []byte{5, 0, 5, 7, 0, 1, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(c.reply.Bytes(), want) {
		t.Fatalf("unexpected rejection response: %v", c.reply.Bytes())
	}
}

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
			name:       "UDP enabled still uses association sockets",
			udpEnabled: true,
			want:       []net.Network{net.Network_TCP, net.Network_UNIX},
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
		{name: "nil", want: net.LocalHostIP.String()},
		{name: "typed nil", addr: (*stdnet.TCPAddr)(nil), want: net.LocalHostIP.String()},
		{name: "empty TCP", addr: &stdnet.TCPAddr{}, want: net.LocalHostIP.String()},
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
