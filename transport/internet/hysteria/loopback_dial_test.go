package hysteria

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
)

type loopbackSourceDialer struct {
	t     *testing.T
	want  net.Address
	err   error
	owner *core.Instance
}

func (d loopbackSourceDialer) Dial(ctx context.Context, src net.Address, _ net.Destination, _ *internet.SocketConfig) (net.Conn, error) {
	d.t.Helper()
	if !reflect.DeepEqual(src, d.want) {
		d.t.Errorf("Hy2 UDP source = %v, want %v", src, d.want)
	}
	if core.FromContext(ctx) != d.owner {
		d.t.Error("loopback binding lost request Core owner")
	}
	return nil, d.err
}

func (loopbackSourceDialer) DestIpAddress() net.IP { return nil }

func TestHy2LoopbackSourceBinding(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dest    net.Address
		gateway net.Address
		want    net.Address
	}{
		{"ipv4", net.LocalHostIP, nil, net.LocalHostIP},
		{"ipv6", net.ParseAddress("::1"), nil, net.ParseAddress("::1")},
		{"explicit", net.LocalHostIP, net.ParseAddress("127.0.0.2"), net.ParseAddress("127.0.0.2")},
		{"explicit-ipv6", net.LocalHostIPv6, net.ParseAddress("::2"), net.ParseAddress("::2")},
		{"remote", net.ParseAddress("192.0.2.1"), nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := new(core.Instance)
			ob := &session.Outbound{Gateway: tc.gateway, Tag: "preserved", Target: net.TCPDestination(net.LocalHostIP, 80)}
			ctx := session.ContextWithOutbounds(context.WithValue(context.Background(), core.XrayKey(1), owner), []*session.Outbound{ob})
			sentinel := errors.New("fixture dial stopped")
			internet.UseAlternativeSystemDialer(loopbackSourceDialer{t: t, want: tc.want, err: sentinel, owner: owner})
			defer internet.UseAlternativeSystemDialer(nil)
			c := &client{lock: make(chan struct{}, 1), dest: net.UDPDestination(tc.dest, 443)}
			if _, err := c.tcp(ctx); !errors.Is(err, sentinel) {
				t.Fatalf("dial result: %v", err)
			}
			if ob.Gateway != tc.gateway || ob.Tag != "preserved" {
				t.Fatal("source selection mutated shared outbound metadata")
			}
		})
	}
}

func TestHy2LoopbackDialContextPreservesRoutingAndCancellation(t *testing.T) {
	owner := new(core.Instance)
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), core.XrayKey(1), owner))
	defer cancel()
	c := &client{dest: net.UDPDestination(net.LocalHostIP, 443)}
	bound := c.systemDialContext(parent)
	obs := session.OutboundsFromContext(bound)
	if len(obs) != 1 || obs[0].Gateway != net.LocalHostIP || core.FromContext(bound) != owner {
		t.Fatal("empty-outbound source binding lost owner or loopback source")
	}
	if len(session.OutboundsFromContext(parent)) != 0 {
		t.Fatal("binding mutated parent context")
	}
	for _, addr := range []net.Address{net.LocalHostIP, net.LocalHostIPv6} {
		c.dest.Address = addr
		c.socketConfig = &internet.SocketConfig{DialerProxy: "via"}
		if c.systemDialContext(parent) != parent {
			t.Fatalf("loopback source binding changed dialerProxy context for %v", addr)
		}
	}
	c.socketConfig = nil
	c.dest.Address = net.DomainAddress("localhost")
	if c.systemDialContext(parent) != parent {
		t.Fatal("source binding made assumptions about unresolved domain")
	}
	c.dest.Address = net.LocalHostIP
	cancel()
	if !errors.Is(bound.Err(), context.Canceled) || !errors.Is(c.systemDialContext(parent).Err(), context.Canceled) {
		t.Fatal("source binding detached request cancellation")
	}
}
