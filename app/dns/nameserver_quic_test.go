package dns

import (
	"context"
	"crypto/tls"
	"net/url"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/google/go-cmp/cmp"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/testing/networktest"
)

func publicQUICServer(t *testing.T) *QUICNameServer {
	t.Helper()
	p := networktest.Enable(t)
	u, err := url.Parse("quic://dns.adguard-dns.com")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewQUICNameServer(u, false, false, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.dial = func(ctx context.Context, address string, config *tls.Config, qconfig *quic.Config) (*quic.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		packet, err := p.ListenPacket(ctx, address)
		if err != nil {
			return nil, err
		}
		destination, err := net.ResolveUDPAddr("udp", address)
		if err != nil {
			packet.Close()
			return nil, err
		}
		connection, err := quic.Dial(ctx, packet, destination, config, qconfig)
		if err != nil {
			packet.Close()
			return nil, err
		}
		t.Cleanup(func() { connection.CloseWithError(0, "test complete"); packet.Close() })
		return connection, nil
	}
	t.Cleanup(func() { _ = s.cacheController.cacheCleanup.Close() })
	return s
}

func TestPublicQUICNameServer(t *testing.T) {
	s := publicQUICServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ips, _, err := s.QueryIP(ctx, "cloudflare.com", dns.IPOption{IPv4Enable: true, IPv6Enable: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) == 0 {
		t.Fatal("expected some IPs")
	}
	ips2, _, err := s.QueryIP(ctx, "cloudflare.com", dns.IPOption{IPv4Enable: true, IPv6Enable: true})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(ips2, ips); diff != "" {
		t.Fatal(diff)
	}
}

func TestPublicQUICNameServerWithIPv4Override(t *testing.T) {
	s := publicQUICServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ips, _, err := s.QueryIP(ctx, "cloudflare.com", dns.IPOption{IPv4Enable: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) == 0 {
		t.Fatal("expected some IPs")
	}
	for _, ip := range ips {
		if len(ip) != net.IPv4len {
			t.Error("expected only IPv4")
		}
	}
}

func TestPublicQUICNameServerWithIPv6Override(t *testing.T) {
	s := publicQUICServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ips, _, err := s.QueryIP(ctx, "cloudflare.com", dns.IPOption{IPv6Enable: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) == 0 {
		t.Fatal("expected some IPs")
	}
	for _, ip := range ips {
		if len(ip) != net.IPv6len {
			t.Error("expected only IPv6")
		}
	}
}
