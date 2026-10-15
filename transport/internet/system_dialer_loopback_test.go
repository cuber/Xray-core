package internet

import (
	"context"
	"net"
	"testing"

	xnet "github.com/xtls/xray-core/common/net"
)

func TestSystemDialerLoopbackUDPSourcePolicy(t *testing.T) {
	v4 := xnet.ParseAddress("127.0.0.2")
	v6 := xnet.ParseAddress("::1")
	for _, tc := range []struct {
		name string
		src  xnet.Address
		dest xnet.Destination
		opts *SocketConfig
		want xnet.Address
	}{
		{name: "literal-v4", dest: xnet.UDPDestination(xnet.LocalHostIP, 9), want: xnet.LocalHostIP},
		{name: "other-v4-loopback", dest: xnet.UDPDestination(v4, 9), want: xnet.LocalHostIP},
		{name: "non-loopback", dest: xnet.UDPDestination(xnet.ParseAddress("198.18.0.1"), 9)},
		{name: "ipv6-loopback", dest: xnet.UDPDestination(v6, 9)},
		{name: "domain", dest: xnet.UDPDestination(xnet.DomainAddress("localhost"), 9)},
		{name: "tcp", dest: xnet.TCPDestination(xnet.LocalHostIP, 9)},
		{name: "explicit-v4", src: v4, dest: xnet.UDPDestination(xnet.LocalHostIP, 9), want: v4},
		{name: "explicit-v6", src: v6, dest: xnet.UDPDestination(xnet.LocalHostIP, 9), want: v6},
		{name: "explicit-wildcard", src: xnet.AnyIP, dest: xnet.UDPDestination(xnet.LocalHostIP, 9), want: xnet.AnyIP},
		{name: "interface", dest: xnet.UDPDestination(xnet.LocalHostIP, 9), opts: &SocketConfig{Interface: "test-interface"}},
		{name: "dialer-proxy", dest: xnet.UDPDestination(xnet.LocalHostIP, 9), opts: &SocketConfig{DialerProxy: "relay"}},
		{name: "other-socket-options", dest: xnet.UDPDestination(xnet.LocalHostIP, 9), opts: &SocketConfig{Mark: 123}, want: xnet.LocalHostIP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := systemUDPLoopbackSource(tc.src, tc.dest, tc.opts)
			if got != tc.want {
				t.Fatalf("source=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestSystemDialerLoopbackExplicitSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, source := range []xnet.Address{nil, xnet.LocalHostIP} {
		c, err := (&DefaultSystemDialer{}).Dial(ctx, source, xnet.UDPDestination(xnet.LocalHostIP, 9), nil)
		if err != nil {
			t.Fatal(err)
		}
		local := c.LocalAddr().(*net.UDPAddr)
		if !local.IP.Equal(net.IPv4(127, 0, 0, 1)) {
			c.Close()
			t.Fatalf("unbound source=%s", local)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
