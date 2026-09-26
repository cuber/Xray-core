package dns_test

import (
	"context"
	"testing"
	"time"

	. "github.com/xtls/xray-core/app/dns"
	"github.com/xtls/xray-core/features/dns"
)

func TestPublicLocalNameServer(t *testing.T) {
	requirePublicDNS(t)
	s := NewLocalNameServer()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	ips, _, err := s.QueryIP(ctx, "google.com", dns.IPOption{
		IPv4Enable: true,
		IPv6Enable: true,
		FakeEnable: false,
	})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) == 0 {
		t.Error("expect some ips, but got 0")
	}
}
