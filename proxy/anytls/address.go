package anytls

import (
	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/common/net"
)

// Address conversion is protocol-local; transport and dispatch stay in Core.
func toDestination(address M.Socksaddr, network net.Network) net.Destination {
	if address.IsFqdn() {
		return net.Destination{
			Network: network,
			Address: net.DomainAddress(address.Fqdn),
			Port:    net.Port(address.Port),
		}
	}
	if address.IsIP() {
		return net.Destination{
			Network: network,
			Address: net.IPAddress(address.Addr.AsSlice()),
			Port:    net.Port(address.Port),
		}
	}
	return net.Destination{}
}

func toSocksaddr(destination net.Destination) M.Socksaddr {
	var address M.Socksaddr
	switch destination.Address.Family() {
	case net.AddressFamilyDomain:
		address.Fqdn = destination.Address.Domain()
	default:
		address.Addr = M.AddrFromIP(destination.Address.IP())
	}
	address.Port = uint16(destination.Port)
	return address
}
