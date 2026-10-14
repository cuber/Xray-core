package core

import (
	"context"

	"github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport/internet"
)

func init() {
	internet.ResolveDNSClient = func(ctx context.Context) dns.Client {
		if instance := FromContext(ctx); instance != nil {
			client, _ := instance.GetFeature(dns.ClientType()).(dns.Client)
			return client
		}
		return nil
	}
	internet.ResolveOutboundManager = func(ctx context.Context) outbound.Manager {
		if instance := FromContext(ctx); instance != nil {
			manager, _ := instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
			return manager
		}
		return nil
	}
}
