package core

import (
	"context"
	"testing"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/transport/internet"
)

type ownedDNS struct{ ip net.IP }

func (*ownedDNS) Type() interface{} { return dns.ClientType() }
func (*ownedDNS) Start() error      { return nil }
func (*ownedDNS) Close() error      { return nil }
func (d *ownedDNS) LookupIP(string, dns.IPOption) ([]net.IP, uint32, error) {
	return []net.IP{d.ip}, 60, nil
}

func TestDialerDNSRequestOwner(t *testing.T) {
	first, second := new(Instance), new(Instance)
	a, b := &ownedDNS{net.ParseIP("192.0.2.1")}, &ownedDNS{net.ParseIP("192.0.2.2")}
	first.AddFeature(a)
	second.AddFeature(b)
	internet.InitSystemDialer(b, nil)
	defer internet.InitSystemDialer(nil, nil)
	for _, tc := range []struct {
		s    *Instance
		want net.IP
	}{{first, a.ip}, {second, b.ip}, {first, a.ip}} {
		ips, err := internet.LookupForIPContext(toContext(context.Background(), tc.s), "same.invalid", internet.DomainStrategy_USE_IP, nil)
		if err != nil || len(ips) != 1 || !ips[0].Equal(tc.want) {
			t.Fatalf("owner DNS: %v, %v; want %v", ips, err, tc.want)
		}
	}
	if _, err := internet.LookupForIPContext(context.Background(), "same.invalid", internet.DomainStrategy_USE_IP, nil); err == nil {
		t.Fatal("ownerless lookup borrowed global DNS")
	}
	ips, err := internet.LookupForIP("same.invalid", internet.DomainStrategy_USE_IP, nil)
	if err != nil || len(ips) != 1 || !ips[0].Equal(b.ip) {
		t.Fatalf("legacy lookup compatibility: %v %v", ips, err)
	}
}
