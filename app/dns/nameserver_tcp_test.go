package dns_test

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/xtls/xray-core/app/dns"
	"github.com/xtls/xray-core/common/net"
	dns_feature "github.com/xtls/xray-core/features/dns"
)

func TestPublicTCPLocalNameServer(t *testing.T) {
	requirePublicDNS(t)
	url, err := url.Parse("tcp+local://8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewTCPLocalNameServer(url, false, false, 0, net.IP(nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	ips, _, err := s.QueryIP(ctx, "cloudflare.com", dns_feature.IPOption{
		IPv4Enable: true,
		IPv6Enable: true,
	})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) == 0 {
		t.Error("expect some ips, but got 0")
	}
}

func TestPublicTCPLocalNameServerWithCache(t *testing.T) {
	requirePublicDNS(t)
	url, err := url.Parse("tcp+local://8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewTCPLocalNameServer(url, false, false, 0, net.IP(nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	ips, _, err := s.QueryIP(ctx, "cloudflare.com", dns_feature.IPOption{
		IPv4Enable: true,
		IPv6Enable: true,
	})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) == 0 {
		t.Error("expect some ips, but got 0")
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second*5)
	ips2, _, err := s.QueryIP(ctx2, "cloudflare.com", dns_feature.IPOption{
		IPv4Enable: true,
		IPv6Enable: true,
	})
	cancel2()
	if err != nil {
		t.Fatal(err)
	}
	if r := cmp.Diff(ips2, ips); r != "" {
		t.Fatal(r)
	}
}

func TestPublicTCPLocalNameServerWithIPv4Override(t *testing.T) {
	requirePublicDNS(t)
	url, err := url.Parse("tcp+local://8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewTCPLocalNameServer(url, false, false, 0, net.IP(nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	ips, _, err := s.QueryIP(ctx, "cloudflare.com", dns_feature.IPOption{
		IPv4Enable: true,
		IPv6Enable: false,
	})
	cancel()
	if err != nil {
		t.Fatal(err)
	}

	if len(ips) == 0 {
		t.Error("expect some ips, but got 0")
	}

	for _, ip := range ips {
		if len(ip) != net.IPv4len {
			t.Error("expect only IPv4 response from DNS query")
		}
	}
}

func TestPublicTCPLocalNameServerWithIPv6Override(t *testing.T) {
	requirePublicDNS(t)
	url, err := url.Parse("tcp+local://8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewTCPLocalNameServer(url, false, false, 0, net.IP(nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	ips, _, err := s.QueryIP(ctx, "cloudflare.com", dns_feature.IPOption{
		IPv4Enable: false,
		IPv6Enable: true,
	})
	cancel()
	if err != nil {
		t.Fatal(err)
	}

	if len(ips) == 0 {
		t.Error("expect some ips, but got 0")
	}

	for _, ip := range ips {
		if len(ip) != net.IPv6len {
			t.Error("expect only IPv6 response from DNS query")
		}
	}
}
