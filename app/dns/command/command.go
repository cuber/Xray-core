// Package command serves the read-only DNS control-plane API (spec012 US-004).
package command

import (
	"context"

	appdns "github.com/xtls/xray-core/app/dns"
	"github.com/xtls/xray-core/common"
	core "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/dns"
	"google.golang.org/grpc"
)

type snapshotProvider interface {
	ControlSnapshot() appdns.ControlSnapshot
}

type service struct {
	UnimplementedDNSServiceServer
	dns dns.Client
}

// ListServers reports the loaded servers; a DNS feature other than app/dns means
// Core uses the system resolver.
func (s *service) ListServers(context.Context, *ListServersRequest) (*ListServersResponse, error) {
	provider, ok := s.dns.(snapshotProvider)
	if !ok {
		return &ListServersResponse{SystemResolver: true}, nil
	}
	snapshot := provider.ControlSnapshot()
	response := &ListServersResponse{
		QueryStrategy:          snapshot.QueryStrategy,
		DisableFallback:        snapshot.DisableFallback,
		DisableFallbackIfMatch: snapshot.DisableFallbackIfMatch,
		EnableParallelQuery:    snapshot.EnableParallelQuery,
		StaticHosts:            uint32(snapshot.StaticHosts),
		Script:                 snapshot.Script,
		Servers:                make([]*Server, 0, len(snapshot.Servers)),
	}
	for index, server := range snapshot.Servers {
		response.Servers = append(response.Servers, &Server{
			Index:         uint32(index),
			Name:          server.Name,
			Address:       server.Address,
			Tag:           server.Tag,
			OutboundTags:  server.OutboundTags,
			QueryStrategy: server.QueryStrategy,
			TimeoutMs:     server.TimeoutMs,
			SkipFallback:  server.SkipFallback,
			FinalQuery:    server.FinalQuery,
			DomainRules:   uint32(server.DomainRules),
			ExpectedIps:   server.ExpectedIPs,
			UnexpectedIps: server.UnexpectedIPs,
			DisableCache:  server.DisableCache,
			ServeStale:    server.ServeStale,
			CacheEntries:  uint32(server.CacheEntries),
		})
	}
	return response, nil
}

func (s *service) Register(server *grpc.Server) {
	RegisterDNSServiceServer(server, s)
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, cfg interface{}) (interface{}, error) {
		instance := core.MustFromContext(ctx)
		sv := &service{}
		err := instance.RequireFeatures(func(client dns.Client) {
			sv.dns = client
		}, false)
		if err != nil {
			return nil, err
		}
		return sv, nil
	}))
}
