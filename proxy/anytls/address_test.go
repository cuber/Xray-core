package anytls

import (
	"go/parser"
	"go/token"
	"io/fs"
	"net/netip"
	"path/filepath"
	"strconv"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/common/net"
)

func TestAnyTLSAddressRoundTrip(t *testing.T) {
	for _, host := range []string{"192.0.2.1", "2001:db8::1", "Mixed.example", "sp.v2.udp-over-tcp.arpa"} {
		for _, network := range []net.Network{net.Network_TCP, net.Network_UDP} {
			for _, port := range []uint16{0, 1, 65535} {
				t.Run(host+"/"+network.String()+"/"+strconv.Itoa(int(port)), func(t *testing.T) {
					want := net.Destination{Address: net.ParseAddress(host), Network: network, Port: net.Port(port)}
					address := toSocksaddr(want)
					if address.Port != port {
						t.Fatalf("port=%d, want %d", address.Port, port)
					}
					if want.Address.Family().IsDomain() {
						if address.Fqdn != host || address.Addr.IsValid() {
							t.Fatalf("domain changed: %+v", address)
						}
					} else if address.Addr != netip.MustParseAddr(host) || address.Fqdn != "" {
						t.Fatalf("IP changed: %+v", address)
					}
					if got := toDestination(address, network); got != want {
						t.Fatalf("round trip: %+v, want %+v", got, want)
					}
				})
			}
		}
	}
}

func TestAnyTLSAddressInvalid(t *testing.T) {
	for _, address := range []M.Socksaddr{{}, {Port: 53}, {Fqdn: "not a host", Port: 53}} {
		if got := toDestination(address, net.Network_UDP); got != (net.Destination{}) {
			t.Fatalf("invalid address %+v converted to %+v", address, got)
		}
	}
}

func TestAnyTLSAddressNormalization(t *testing.T) {
	for _, address := range []M.Socksaddr{
		{Addr: netip.MustParseAddr("::ffff:192.0.2.1"), Port: 53},
		{Addr: netip.MustParseAddr("192.0.2.1"), Port: 53},
	} {
		got := toDestination(address, net.Network_UDP)
		want := net.UDPDestination(net.ParseAddress("192.0.2.1"), 53)
		if got != want {
			t.Fatalf("IP normalization: %+v, want %+v", got, want)
		}
		if back := toSocksaddr(got); back.Addr != netip.MustParseAddr("192.0.2.1") || back.Port != 53 {
			t.Fatalf("normalized round trip: %+v", back)
		}
	}
	address := M.Socksaddr{Fqdn: "destination.example", Addr: netip.MustParseAddr("192.0.2.1"), Port: 443}
	if got := toDestination(address, net.Network_TCP); got != net.TCPDestination(net.DomainAddress(address.Fqdn), 443) {
		t.Fatalf("domain precedence changed: %+v", got)
	}
}

func TestAnyTLSDoesNotImportSingbridge(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			if imported.Path.Value == `"github.com/xtls/xray-core/common/singbridge"` {
				t.Errorf("%s imports the legacy SS-2022 bridge", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
