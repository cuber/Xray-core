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

const (
	aliDNSDoHURL            = "https+local://dns.alidns.com/dns-query"
	aliDNSDoHIPv6TestDomain = "dns.alidns.com"
)

func TestPublicDOHNameServer(t *testing.T) {
	requirePublicDNS(t)
	url, err := url.Parse(aliDNSDoHURL)
	if err != nil {
		t.Fatal(err)
	}

	s := NewDoHNameServer(url, nil, false, false, false, 0, net.IP(nil))
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

func TestPublicDOHNameServerWithCache(t *testing.T) {
	requirePublicDNS(t)
	url, err := url.Parse(aliDNSDoHURL)
	if err != nil {
		t.Fatal(err)
	}

	s := NewDoHNameServer(url, nil, false, false, false, 0, net.IP(nil))
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

func TestPublicDOHNameServerWithIPv4Override(t *testing.T) {
	requirePublicDNS(t)
	url, err := url.Parse(aliDNSDoHURL)
	if err != nil {
		t.Fatal(err)
	}

	s := NewDoHNameServer(url, nil, false, false, false, 0, net.IP(nil))
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

func TestPublicDOHNameServerWithIPv6Override(t *testing.T) {
	requirePublicDNS(t)
	url, err := url.Parse(aliDNSDoHURL)
	if err != nil {
		t.Fatal(err)
	}

	s := NewDoHNameServer(url, nil, false, false, false, 0, net.IP(nil))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	ips, _, err := s.QueryIP(ctx, aliDNSDoHIPv6TestDomain, dns_feature.IPOption{
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
