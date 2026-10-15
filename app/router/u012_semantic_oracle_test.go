package router

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/xtls/xray-core/common/geodata"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/routing"
	"google.golang.org/protobuf/proto"
)

type semanticRule struct {
	id           string
	domains, ips []string
}

type semanticContext struct {
	routing.Context
	domain string
	ips    []xnet.IP
}

func (c semanticContext) GetTargetDomain() string  { return c.domain }
func (c semanticContext) GetTargetIPs() []xnet.IP  { return c.ips }
func (c semanticContext) GetNetwork() xnet.Network { return xnet.Network_TCP }
func (c semanticContext) GetSkipDNSResolve() bool  { return true }

// This interpreter never calls geodata parsers, matchers, registries or router
// conditions. DAT decoding uses the generic protobuf decoder, not loadSite.
type semanticReference struct {
	sites     geodata.GeoSiteList
	addresses geodata.GeoIPList
}

func semanticDomain(kind geodata.Domain_Type, pattern, input string) bool {
	input = strings.ToLower(input)
	if kind == geodata.Domain_Regex {
		return regexp.MustCompile(pattern).MatchString(input)
	}
	pattern = strings.ToLower(pattern)
	switch kind {
	case geodata.Domain_Full:
		return input == pattern
	case geodata.Domain_Domain:
		return input == pattern || strings.HasSuffix(input, "."+pattern)
	case geodata.Domain_Substr:
		return strings.Contains(input, pattern)
	default:
		panic("unsupported reference domain type")
	}
}

func (ref *semanticReference) domain(rule, input string) bool {
	if input == "" {
		return false
	}
	if strings.HasPrefix(rule, "ext:") {
		parts := strings.SplitN(rule, ":", 3)
		selector := strings.Split(strings.ToLower(parts[2]), "@")
		for _, site := range ref.sites.Entry {
			if !strings.EqualFold(site.Code, selector[0]) {
				continue
			}
			for _, d := range site.Domain {
				attributes := true
				for _, attr := range selector[1:] {
					found := false
					for _, a := range d.Attribute {
						found = found || a.Key == attr
					}
					attributes = attributes && found
				}
				if attributes && semanticDomain(d.Type, d.Value, input) {
					return true
				}
			}
		}
		return false
	}
	kind, pattern, _ := strings.Cut(rule, ":")
	types := map[string]geodata.Domain_Type{"full": geodata.Domain_Full, "domain": geodata.Domain_Domain, "keyword": geodata.Domain_Substr, "regexp": geodata.Domain_Regex}
	typ, ok := types[kind]
	if !ok {
		panic("unsupported reference selector")
	}
	return semanticDomain(typ, pattern, input)
}

func (ref *semanticReference) ip(rule string, ips []xnet.IP) bool {
	var prefixes []netip.Prefix
	if strings.HasPrefix(rule, "ext:") {
		parts := strings.SplitN(rule, ":", 3)
		for _, group := range ref.addresses.Entry {
			if !strings.EqualFold(group.Code, parts[2]) {
				continue
			}
			for _, cidr := range group.Cidr {
				addr, ok := netip.AddrFromSlice(cidr.Ip)
				if !ok {
					panic("invalid reference CIDR")
				}
				prefixes = append(prefixes, netip.PrefixFrom(addr, int(cidr.Prefix)))
			}
		}
	} else {
		prefixes = append(prefixes, netip.MustParsePrefix(rule))
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		for _, prefix := range prefixes {
			if prefix.Contains(addr.Unmap()) {
				return true
			}
		}
	}
	return false
}

func (ref *semanticReference) matches(rules []semanticRule, ctx semanticContext) []string {
	var ids []string
	for _, rule := range rules {
		domain, ip := len(rule.domains) == 0, len(rule.ips) == 0
		for _, selector := range rule.domains {
			domain = domain || ref.domain(selector, ctx.domain)
		}
		for _, selector := range rule.ips {
			ip = ip || ref.ip(selector, ctx.ips)
		}
		if domain && ip {
			ids = append(ids, rule.id)
		}
	}
	return ids
}

func TestU012CombinedIndependentSemanticOracle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("xray.location.asset", dir)
	domain := func(typ geodata.Domain_Type, value string, attrs ...string) *geodata.Domain {
		d := &geodata.Domain{Type: typ, Value: value}
		for _, a := range attrs {
			d.Attribute = append(d.Attribute, &geodata.Domain_Attribute{Key: a})
		}
		return d
	}
	sites := &geodata.GeoSiteList{Entry: []*geodata.GeoSite{{Code: "MIX", Domain: []*geodata.Domain{
		domain(geodata.Domain_Full, "API.Example.TEST", "blue", "ads"),
		domain(geodata.Domain_Domain, "Example.TEST", "blue"),
		domain(geodata.Domain_Substr, "TrAcK", "ads"),
		domain(geodata.Domain_Regex, `^rx[0-9]+\.example\.test$`, "blue", "ads"),
		domain(geodata.Domain_Full, "only-ads.test", "ads"),
		domain(geodata.Domain_Regex, `^UPPER\.test$`, "blue"),
	}}}}
	addresses := &geodata.GeoIPList{Entry: []*geodata.GeoIP{{Code: "LAB", Cidr: []*geodata.CIDR{
		{Ip: []byte{192, 0, 2, 0}, Prefix: 24},
		{Ip: netip.MustParseAddr("2001:db8::").AsSlice(), Prefix: 32},
	}}}}
	ref := new(semanticReference)
	for _, fixture := range []struct {
		name          string
		input, output proto.Message
	}{
		{"sites.dat", sites, &ref.sites}, {"ips.dat", addresses, &ref.addresses},
	} {
		data, err := proto.Marshal(fixture.input)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, fixture.name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		data, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := proto.Unmarshal(data, fixture.output); err != nil {
			t.Fatal(err)
		}
		t.Logf("fixture %s bytes=%d sha256=%x", fixture.name, len(data), sha256.Sum256(data))
	}
	for _, name := range []string{"geosite.dat", "geoip.dat"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "resources", name))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("production provenance only %s bytes=%d sha256=%x", name, len(data), sha256.Sum256(data))
	}
	rules := []semanticRule{
		{"intersection", []string{"ext:sites.dat:mIx@BLUE@ADS"}, []string{"ext:ips.dat:lab"}},
		{"full", []string{"full:API.EXAMPLE.TEST"}, nil},
		{"blue", []string{"ext:sites.dat:MIX@blue"}, nil},
		{"blue-repeat", []string{"ext:sites.dat:mix@blue", "ext:sites.dat:MIX@blue"}, nil},
		{"ads", []string{"ext:sites.dat:MIX@ads"}, nil},
		{"regex", []string{`regexp:^rx[0-9]+\.example\.test$`}, nil},
		{"suffix", []string{"domain:Example.TEST"}, nil},
		{"keyword", []string{"keyword:TrAcK"}, nil},
		{"ip", nil, []string{"ext:ips.dat:LAB", "ext:ips.dat:lab"}},
		{"ip-narrow", nil, []string{"192.0.2.128/25", "2001:db8:1::/48"}},
		{"fallback", nil, nil},
	}
	domains := []string{"API.Example.TEST", "www.Example.Test", "rx12.EXAMPLE.test", "rxX.example.test", "notexample.test", "track.other.test", "ONLY-ADS.TEST", "UPPER.test", "nomatch.test", ""}
	ipSets := [][]string{nil, {"192.0.2.1"}, {"192.0.2.200"}, {"192.0.3.1"}, {"2001:db8:1::1"}, {"2001:db9::1"}, {"198.51.100.1", "2001:db8::2"}}
	// Explicit anchors guard against a vacuous or accidentally broadened oracle.
	anchor := semanticContext{domain: "API.EXAMPLE.TEST", ips: []xnet.IP{xnet.IP{192, 0, 2, 1}}}
	if got := ref.matches(rules, anchor); !slices.Equal(got, []string{"intersection", "full", "blue", "blue-repeat", "ads", "suffix", "ip", "fallback"}) {
		t.Fatalf("reference anchor: %v", got)
	}
	if got := ref.matches(rules, semanticContext{domain: "UPPER.test"}); !slices.Equal(got, []string{"fallback"}) {
		t.Fatalf("regex case anchor: %v", got)
	}
	for generation := 0; generation < 2; generation++ {
		t.Run(fmt.Sprintf("order=%d", generation), func(t *testing.T) {
			ordered := slices.Clone(rules)
			if generation == 1 {
				slices.Reverse(ordered[:len(ordered)-1])
			}
			config := &Config{DomainStrategy: Config_AsIs}
			var flatSelectors []string
			var flatRules []*geodata.DomainRule
			for _, rule := range ordered {
				d, err := geodata.ParseDomainRules(rule.domains, geodata.Domain_Domain)
				if err != nil {
					t.Fatal(err)
				}
				ip, err := geodata.ParseIPRules(rule.ips)
				if err != nil {
					t.Fatal(err)
				}
				flatSelectors = append(flatSelectors, rule.domains...)
				flatRules = append(flatRules, d...)
				config.Rule = append(config.Rule, &RoutingRule{RuleTag: rule.id, TargetTag: &RoutingRule_Tag{Tag: "out-" + rule.id}, Domain: d, Ip: ip, Networks: []xnet.Network{xnet.Network_TCP}})
			}
			matcher, err := geodata.DomainReg.BuildDomainMatcher(flatRules)
			if err != nil {
				t.Fatal(err)
			}
			router := new(Router)
			if err := router.Init(context.Background(), config, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			defer router.Close()
			for round := 0; round < 3; round++ {
				if err := router.ReloadRules(config, false); err != nil {
					t.Fatal(err)
				}
				runtime.GC()
				for _, domain := range domains {
					var wantIndices []uint32
					for i, selector := range flatSelectors {
						if ref.domain(selector, domain) {
							wantIndices = append(wantIndices, uint32(i))
						}
					}
					// Match permits duplicate indices and unspecified ordering; compare
					// sets, but retain distinct indices of repeated ext selectors.
					gotIndices := matcher.Match(strings.ToLower(domain))
					slices.Sort(gotIndices)
					gotIndices = slices.Compact(gotIndices)
					if !slices.Equal(gotIndices, wantIndices) {
						t.Fatalf("domain=%q matcher IDs=%v want=%v", domain, gotIndices, wantIndices)
					}
					for _, rawIPs := range ipSets {
						ctx := semanticContext{domain: domain}
						for _, raw := range rawIPs {
							ctx.ips = append(ctx.ips, netip.MustParseAddr(raw).AsSlice())
						}
						want := ref.matches(ordered, ctx)
						var got []string
						for _, rule := range *router.rules.Load() {
							if rule.Apply(ctx) {
								got = append(got, rule.RuleTag)
							}
						}
						if !slices.Equal(got, want) {
							t.Fatalf("round=%d domain=%q ips=%v IDs=%v want=%v", round, domain, rawIPs, got, want)
						}
						picked, err := router.PickRoute(ctx)
						if err != nil {
							t.Fatal(err)
						}
						if picked.GetRuleTag() != want[0] || picked.GetOutboundTag() != "out-"+want[0] {
							t.Fatalf("domain=%q ips=%v first=%s/%s want=%s", domain, rawIPs, picked.GetRuleTag(), picked.GetOutboundTag(), want[0])
						}
					}
				}
			}
			if generation == 0 {
				for _, mutation := range []string{"attributes", "drop-duplicate", "order", "ip-conjunction"} {
					t.Run("negative-"+mutation, func(t *testing.T) {
						changed := proto.Clone(config).(*Config)
						ctx := anchor
						switch mutation {
						case "attributes":
							changed.Rule[0].Domain[0].GetGeosite().Attrs = ""
							ctx.domain = "only-ads.test"
						case "drop-duplicate":
							changed.Rule = append(changed.Rule[:3], changed.Rule[4:]...)
						case "order":
							changed.Rule[0], changed.Rule[1] = changed.Rule[1], changed.Rule[0]
						case "ip-conjunction":
							changed.Rule[0].Ip = nil
							ctx.ips = nil
						}
						if err := router.ReloadRules(changed, false); err != nil {
							t.Fatal(err)
						}
						want := ref.matches(ordered, ctx)
						var got []string
						for _, rule := range *router.rules.Load() {
							if rule.Apply(ctx) {
								got = append(got, rule.RuleTag)
							}
						}
						if slices.Equal(got, want) {
							t.Fatalf("oracle failed to distinguish %s: %v", mutation, got)
						}
						picked, err := router.PickRoute(ctx)
						if err != nil {
							t.Fatal(err)
						}
						if mutation != "drop-duplicate" && picked.GetRuleTag() == want[0] {
							t.Fatalf("first-route oracle missed %s", mutation)
						}
					})
				}
			}
		})
	}
}
