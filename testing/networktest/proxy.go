// Package networktest configures explicitly enabled public integration tests.
package networktest

import (
	"context"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/protocol/socks"
	"github.com/xtls/xray-core/transport/internet"
)

type Proxy struct {
	client    *socks.Client
	udpClient *socks.Client
}

// Enable is for serial tests only: Core's system dialer is global.
func Enable(t testing.TB) *Proxy {
	t.Helper()
	if os.Getenv("XRAY_TEST_NETWORK") != "1" {
		t.Skip("public integration: set XRAY_TEST_NETWORK=1")
	}
	p := &Proxy{}
	if raw := os.Getenv("XRAY_TEST_PROXY"); raw != "" {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal("invalid XRAY_TEST_PROXY URL")
		}
		if u.Scheme != "socks5" || u.Hostname() == "" || u.Port() == "" {
			t.Fatal("XRAY_TEST_PROXY requires socks5://host:port")
		}
		p.client, err = socks.NewClientFromURL(N.SystemDialer, raw)
		if err != nil {
			t.Fatal("invalid SOCKS5 proxy configuration")
		}
		t.Logf("public integration via SOCKS5 %s", u.Host)
	}
	p.udpClient = p.client
	if raw := os.Getenv("XRAY_TEST_UDP_PROXY"); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "socks5" || u.Hostname() == "" || u.Port() == "" {
			t.Fatal("XRAY_TEST_UDP_PROXY requires socks5://host:port")
		}
		p.udpClient, err = socks.NewClientFromURL(N.SystemDialer, raw)
		if err != nil {
			t.Fatal("invalid UDP SOCKS5 proxy configuration")
		}
		t.Logf("public UDP integration via SOCKS5 %s", u.Host)
	}
	if p.client != nil || p.udpClient != nil {
		internet.UseAlternativeSystemDialer(internet.WithAdapter(p))
		t.Cleanup(func() { internet.UseAlternativeSystemDialer(nil) })
	}
	return p
}

func (p *Proxy) Dial(network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return p.DialContext(ctx, network, address)
}

func (p *Proxy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	client := p.client
	if network == "udp" || network == "udp4" || network == "udp6" {
		client = p.udpClient
	}
	if client == nil {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	return client.DialContext(ctx, network, M.ParseSocksaddr(address))
}

func (p *Proxy) ListenPacket(ctx context.Context, address string) (net.PacketConn, error) {
	if p.udpClient == nil {
		return (&net.ListenConfig{}).ListenPacket(ctx, "udp", ":0")
	}
	return p.udpClient.ListenPacket(ctx, M.ParseSocksaddr(address))
}
