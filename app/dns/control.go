package dns

import (
	"net/url"
	"sort"
	"strings"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/dns"
)

// ControlServer is one loaded client as the control plane reports it (spec012 FR-010).
type ControlServer struct {
	Name          string
	Address       string
	Tag           string
	OutboundTags  []string
	QueryStrategy string
	TimeoutMs     uint32
	SkipFallback  bool
	FinalQuery    bool
	DomainRules   int
	ExpectedIPs   bool
	UnexpectedIPs bool
	DisableCache  bool
	ServeStale    bool
	CacheEntries  int
}

// ControlSnapshot is the loaded DNS module: global options and servers in order.
type ControlSnapshot struct {
	QueryStrategy          string
	DisableFallback        bool
	DisableFallbackIfMatch bool
	EnableParallelQuery    bool
	StaticHosts            int
	Script                 bool
	Servers                []ControlServer
}

// ControlSnapshot reads only immutable client fields and each cache under its read lock.
func (s *DNS) ControlSnapshot() ControlSnapshot {
	rules := make(map[uint16]int, len(s.clients))
	for _, info := range s.matcherInfos {
		rules[info.clientIdx]++
	}
	snapshot := ControlSnapshot{
		QueryStrategy:          queryStrategyName(s.ipOption, s.checkSystem),
		DisableFallback:        s.disableFallback,
		DisableFallbackIfMatch: s.disableFallbackIfMatch,
		EnableParallelQuery:    s.enableParallelQuery,
		Script:                 s.script != nil,
		Servers:                make([]ControlServer, 0, len(s.clients)),
	}
	if s.hosts != nil {
		snapshot.StaticHosts = len(s.hosts.responses)
	}
	for index, client := range s.clients {
		server := ControlServer{
			Address:       client.address,
			Tag:           client.tag,
			QueryStrategy: queryStrategyName(client.ipOption, client.checkSystem),
			TimeoutMs:     uint32(client.timeoutMs.Milliseconds()),
			SkipFallback:  client.skipFallback,
			FinalQuery:    client.finalQuery,
			DomainRules:   rules[uint16(index)],
			ExpectedIPs:   client.expectedIPs != nil,
			UnexpectedIPs: client.unexpectedIPs != nil,
		}
		for tag := range client.outboundTags {
			server.OutboundTags = append(server.OutboundTags, tag)
		}
		sort.Strings(server.OutboundTags)
		if client.server != nil {
			server.Name = client.server.Name()
			server.DisableCache = client.server.IsDisableCache()
			if cached, ok := client.server.(CachedNameserver); ok {
				controller := cached.getCacheController()
				server.ServeStale = controller.serveStale
				server.CacheEntries = controller.size()
			}
		}
		snapshot.Servers = append(snapshot.Servers, server)
	}
	return snapshot
}

func queryStrategyName(option *dns.IPOption, system bool) string {
	switch {
	case system:
		return "UseSystem"
	case option == nil:
		return ""
	case option.IPv4Enable && option.IPv6Enable:
		return "UseIP"
	case option.IPv4Enable:
		return "UseIPv4"
	case option.IPv6Enable:
		return "UseIPv6"
	}
	return ""
}

// controlAddress renders a configured server address without URL userinfo.
func controlAddress(endpoint *net.Endpoint) string {
	if endpoint == nil || endpoint.Address == nil {
		return ""
	}
	dest := endpoint.AsDestination()
	if dest.Address.Family().IsDomain() {
		domain := dest.Address.Domain()
		if parsed, err := url.Parse(domain); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			parsed.User = nil
			return parsed.String()
		}
		if strings.EqualFold(domain, "localhost") || strings.EqualFold(domain, "fakedns") {
			return strings.ToLower(domain)
		}
	}
	if dest.Network == net.Network_Unknown {
		dest.Network = net.Network_UDP
	}
	if dest.Port == 0 {
		dest.Port = 53 // as the classic name server applies it
	}
	return strings.ToLower(dest.Network.SystemString()) + "://" + dest.NetAddr()
}
