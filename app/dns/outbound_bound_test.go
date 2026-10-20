package dns_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"
	"github.com/xtls/xray-core/app/dispatcher"
	. "github.com/xtls/xray-core/app/dns"
	"github.com/xtls/xray-core/app/policy"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	feature_dns "github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy/blackhole"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/testing/servers/dnsfixture"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/pipe"

	_ "github.com/xtls/xray-core/transport/internet/tcp"
)

// fixedHandler answers every A query with one address and counts queries.
type fixedHandler struct {
	ip      net.IP
	queries atomic.Int32
}

func (h *fixedHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	h.queries.Add(1)
	ans := new(dns.Msg)
	ans.SetReply(r)
	for _, q := range r.Question {
		if q.Qtype == dns.TypeA {
			ans.Answer = append(ans.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300}, A: h.ip})
		}
	}
	_ = w.WriteMsg(ans)
}

type fixture struct {
	handler *fixedHandler
	stop    func()
	server  *NameServer
}

func startFixture(t *testing.T, ip net.IP, tags ...string) *fixture {
	t.Helper()
	handler := &fixedHandler{ip: ip}
	address, stop := dnsfixture.StartWithStop(t, "udp", handler)
	_, portText, err := net.SplitHostPort(address)
	common.Must(err)
	port, err := net.PortFromString(portText)
	common.Must(err)
	return &fixture{handler: handler, stop: stop, server: &NameServer{
		Address: &net.Endpoint{
			Network: net.Network_UDP,
			Address: &net.IPOrDomain{Address: &net.IPOrDomain_Ip{Ip: []byte{127, 0, 0, 1}}},
			Port:    uint32(port),
		},
		OutboundTag: tags,
	}}
}

func newBoundDNS(t *testing.T, servers ...*NameServer) feature_dns.Client {
	t.Helper()
	v, err := core.New(&core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&Config{NameServer: servers}),
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&policy.Config{}),
		},
		Outbound: []*core.OutboundHandlerConfig{{
			ProxySettings: serial.ToTypedMessage(&freedom.Config{
				FinalRules: []*freedom.FinalRuleConfig{{Action: freedom.RuleAction_Allow}},
			}),
		}},
	})
	common.Must(err)
	t.Cleanup(func() { _ = v.Close() })
	return v.GetFeature(feature_dns.ClientType()).(feature_dns.Client)
}

func lookup(client feature_dns.Client, domain, tag string) (net.IP, error) {
	ips, _, err := client.LookupIP(domain, feature_dns.IPOption{IPv4Enable: true, OutboundTag: tag})
	if err != nil || len(ips) == 0 {
		return nil, err
	}
	return ips[0], nil
}

func TestOutboundBoundServersIsolateAnswersAndCaches(t *testing.T) {
	a := startFixture(t, net.IP{10, 0, 0, 1}, "exit-a")
	b := startFixture(t, net.IP{10, 0, 0, 2}, "exit-b")
	plain := startFixture(t, net.IP{10, 0, 0, 3})
	client := newBoundDNS(t, a.server, b.server, plain.server)

	for round := 0; round < 2; round++ {
		for tag, want := range map[string]net.IP{"exit-a": {10, 0, 0, 1}, "exit-b": {10, 0, 0, 2}, "": {10, 0, 0, 3}, "unbound": {10, 0, 0, 3}} {
			got, err := lookup(client, "same.example", tag)
			if err != nil || !got.Equal(want) {
				t.Fatalf("round %d tag %q = %v, %v; want %v", round, tag, got, err, want)
			}
		}
	}
	// The second round came from each server's own cache.
	for name, f := range map[string]*fixture{"a": a, "b": b, "plain": plain} {
		if got := f.handler.queries.Load(); got != 1 {
			t.Fatalf("server %s answered %d queries, want 1 (cached once, never shared)", name, got)
		}
	}
}

func TestOutboundBoundServersFailClosed(t *testing.T) {
	a := startFixture(t, net.IP{10, 0, 0, 1}, "exit-a")
	plain := startFixture(t, net.IP{10, 0, 0, 3})
	a.server.TimeoutMs = 300
	client := newBoundDNS(t, a.server, plain.server)
	a.stop()

	if got, err := lookup(client, "down.example", "exit-a"); err == nil || got != nil {
		t.Fatalf("exit-a lookup with its only server down = %v, %v; want failure without fallback", got, err)
	}
	if got := plain.handler.queries.Load(); got != 0 {
		t.Fatalf("unrestricted server answered %d bound lookups, want 0", got)
	}
	if got, err := lookup(client, "down.example", ""); err != nil || !got.Equal(net.IP{10, 0, 0, 3}) {
		t.Fatalf("untagged lookup = %v, %v; want unrestricted answer", got, err)
	}
}

func TestOutboundBoundServersNeverServeOtherLookups(t *testing.T) {
	a := startFixture(t, net.IP{10, 0, 0, 1}, "exit-a")
	a.server.Domain = []*geodata.DomainRule{{Value: &geodata.DomainRule_Custom{Custom: &geodata.Domain{Type: geodata.Domain_Full, Value: "only.example"}}}}
	client := newBoundDNS(t, a.server)

	if got, err := lookup(client, "only.example", ""); err == nil || got != nil {
		t.Fatalf("untagged lookup reached a bound server through its domain rule: %v, %v", got, err)
	}
	if got := a.handler.queries.Load(); got != 0 {
		t.Fatalf("bound server answered %d untagged queries, want 0", got)
	}
	if got, err := lookup(client, "only.example", "exit-a"); err != nil || !got.Equal(net.IP{10, 0, 0, 1}) {
		t.Fatalf("bound lookup = %v, %v; want the bound answer", got, err)
	}
}

func TestOutboundBoundServersInOrder(t *testing.T) {
	first := startFixture(t, net.IP{10, 0, 0, 1}, "exit-a")
	second := startFixture(t, net.IP{10, 0, 0, 2}, "exit-a", "exit-b")
	first.server.TimeoutMs = 300
	client := newBoundDNS(t, first.server, second.server)
	first.stop()

	if got, err := lookup(client, "next.example", "exit-a"); err != nil || !got.Equal(net.IP{10, 0, 0, 2}) {
		t.Fatalf("exit-a with first server down = %v, %v; want the second bound server", got, err)
	}
	if got, err := lookup(client, "next.example", "exit-b"); err != nil || !got.Equal(net.IP{10, 0, 0, 2}) {
		t.Fatalf("exit-b = %v, %v; want its only server", got, err)
	}
}

// TestTargetStrategyUsesItsOutboundServers dispatches through two real outbound
// handlers with targetStrategy and checks which bound server resolved each target.
func TestTargetStrategyUsesItsOutboundServers(t *testing.T) {
	a := startFixture(t, net.IP{10, 0, 0, 1}, "exit-a")
	b := startFixture(t, net.IP{10, 0, 0, 2}, "exit-b")
	plain := startFixture(t, net.IP{10, 0, 0, 3})
	sender := serial.ToTypedMessage(&proxyman.SenderConfig{TargetStrategy: internet.DomainStrategy_FORCE_IP4})
	v, err := core.New(&core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&Config{NameServer: []*NameServer{a.server, b.server, plain.server}}),
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&policy.Config{}),
		},
		Outbound: []*core.OutboundHandlerConfig{
			// Default outbound: carries the DNS queries themselves to the fixtures.
			{Tag: "direct", ProxySettings: serial.ToTypedMessage(&freedom.Config{
				FinalRules: []*freedom.FinalRuleConfig{{Action: freedom.RuleAction_Allow}},
			})},
			{Tag: "exit-a", SenderSettings: sender, ProxySettings: serial.ToTypedMessage(&blackhole.Config{})},
			{Tag: "exit-b", SenderSettings: sender, ProxySettings: serial.ToTypedMessage(&blackhole.Config{})},
		},
	})
	common.Must(err)
	common.Must(v.Start())
	t.Cleanup(func() { _ = v.Close() })
	manager := v.GetFeature(outbound.ManagerType()).(outbound.Manager)

	for _, tag := range []string{"exit-a", "exit-b"} {
		ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{
			Target: net.TCPDestination(net.DomainAddress("target.example"), 443),
		}})
		reader, writer := pipe.New()
		manager.GetHandler(tag).Dispatch(ctx, &transport.Link{Reader: reader, Writer: writer})
	}
	if a.handler.queries.Load() != 1 || b.handler.queries.Load() != 1 || plain.handler.queries.Load() != 0 {
		t.Fatalf("queries a=%d b=%d plain=%d; want each outbound resolved by its own server",
			a.handler.queries.Load(), b.handler.queries.Load(), plain.handler.queries.Load())
	}
}
