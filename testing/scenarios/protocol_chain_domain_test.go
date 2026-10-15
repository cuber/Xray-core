package scenarios

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
)

var protocolChainDomains = [...]string{"first.payload.protocol-chain.test", "second.payload.protocol-chain.test"}

func protocolChainDomainFixtureStart(t *testing.T, protocols []string) *protocolChainFixture {
	t.Helper()
	f := &protocolChainFixture{}
	pki := protocolChainCertificate(t)
	for range protocolChainDomains {
		f.back = append(f.back, protocolChainBackendStart(t))
	}
	for hop, protocol := range protocols {
		port := protocolChainPort(t)
		in, _ := protocolChainPair(t, pki, protocol, hop, port, 0, false)
		config := protocolChainBase([]any{in}, []any{protocolChainFreedom()})
		if hop == len(protocols)-1 {
			outs := []any{map[string]any{"tag": "deny", "protocol": "blackhole"}}
			var rules []any
			for i, domain := range protocolChainDomains {
				for _, network := range []string{"tcp", "udp"} {
					out := protocolChainFreedom()
					tag := fmt.Sprintf("domain%d-%s", i, network)
					out["tag"] = tag
					out["settings"].(map[string]any)["redirect"] = f.back[i].destination(network).NetAddr()
					outs = append(outs, out)
					rules = append(rules, map[string]any{
						"type": "field", "domain": []string{"full:" + domain}, "network": network, "outboundTag": tag,
					})
				}
			}
			// Redirect replaces the domain with an owned literal endpoint before
			// freedom's domain precheck. This tests routing, not DNS resolution.
			config["outbounds"] = outs
			config["routing"] = map[string]any{"domainStrategy": "AsIs", "rules": rules}
		}
		f.ports = append(f.ports, port)
		f.configs = append(f.configs, config)
		f.servers = append(f.servers, protocolChainStart(t, config))
	}
	for user := range 2 {
		outs := make([]any, len(protocols))
		for hop, protocol := range protocols {
			_, out := protocolChainPair(t, pki, protocol, hop, f.ports[hop], user, false)
			outs[len(protocols)-1-hop] = out
		}
		config := protocolChainBase(nil, outs)
		f.clientConfigs = append(f.clientConfigs, config)
		f.clients = append(f.clients, protocolChainStart(t, config))
	}
	return f
}

func protocolChainDomainRequest(b *protocolChainBackend, backend int, network string, size int) *protocolChainReceipt {
	r := b.request(network, size, false)
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.receipts, network+string(r.wire))
	// Distinct destination markers reject swapped domain-to-backend routes even
	// when both echo servers have otherwise identical request sequences.
	r.wire[0] ^= byte(0x40 * (backend + 1))
	b.receipts[network+string(r.wire)] = r
	return r
}

func protocolChainDomainTraffic(t *testing.T, f *protocolChainFixture, targets, sizes []int) {
	t.Helper()
	for user := range 2 {
		before := make([][2]int64, len(f.servers))
		otherBefore := make([][2]int64, len(f.servers))
		for hop, server := range f.servers {
			before[hop] = protocolChainUserBytes(server, hop, user)
			otherBefore[hop] = protocolChainUserBytes(server, hop, 1-user)
		}
		var expected int64
		for _, network := range []string{"tcp", "udp"} {
			for _, target := range targets {
				backend := f.back[target]
				destination := backend.destination(network)
				if destination.Port == 443 {
					t.Fatal("domain redirect must change the business target port")
				}
				destination.Address = xnet.DomainAddress(protocolChainDomains[target])
				destination.Port = 443
				for _, size := range sizes {
					r := protocolChainDomainRequest(backend, target, network, size)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					c, err := core.Dial(ctx, f.clients[user].x, destination)
					if err == nil {
						err = protocolChainExchangeConn(ctx, c, network, r)
					}
					cancel()
					if err != nil {
						t.Fatalf("user%d %s domain=%s size=%d: %v", user, network, protocolChainDomains[target], size, err)
					}
					expected += int64(len(r.wire))
					if network == "tcp" {
						expected += 4
					}
				}
			}
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			valid := true
			for hop, server := range f.servers {
				got := protocolChainUserBytes(server, hop, user)
				other := protocolChainUserBytes(server, hop, 1-user)
				if (user == 0 || hop == len(f.servers)-1) && other != otherBefore[hop] {
					t.Fatalf("hop%d user%d domain traffic charged to other user: %v -> %v", hop, user, otherBefore[hop], other)
				}
				if hop == len(f.servers)-1 {
					valid = valid && got == [2]int64{before[hop][0] + expected, before[hop][1] + expected}
				} else {
					valid = valid && got[0] > before[hop][0] && got[1] > before[hop][1]
				}
			}
			if valid {
				break
			}
			if time.Now().After(deadline) {
				for hop, server := range f.servers {
					t.Logf("hop%d user%d counters=%v before=%v expected exit delta=%d", hop, user, protocolChainUserBytes(server, hop, user), before[hop], expected)
				}
				t.Fatal("authenticated domain per-hop/exact exit counters did not converge")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func protocolChainDomainReceipts(t *testing.T, f *protocolChainFixture, want [2]int) {
	t.Helper()
	for i, backend := range f.back {
		backend.mu.Lock()
		for _, r := range backend.receipts {
			if r.count != want[i] {
				t.Errorf("domain destination%d receipts=%d want=%d", i, r.count, want[i])
			}
		}
		backend.mu.Unlock()
	}
}

func TestProtocolChainDomainPreservation(t *testing.T) {
	protocolChainNetworkWindow(t)
	for _, depth := range []int{2, 3} {
		for _, protocols := range protocolChainMatrix(depth) {
			t.Run(strings.Join(protocols, "_"), func(t *testing.T) {
				f := protocolChainDomainFixtureStart(t, protocols)
				protocolChainDomainTraffic(t, f, []int{0, 1}, []int{31, 1200})
				f.close(t)
				protocolChainDomainReceipts(t, f, [2]int{1, 1})
			})
		}
	}
}

func TestProtocolChainDomainRouteControls(t *testing.T) {
	protocolChainNetworkWindow(t)
	for _, final := range protocolChainProtocols {
		protocols := []string{"ss2", "vless", final}
		t.Run(strings.Join(protocols, "_"), func(t *testing.T) {
			f := protocolChainDomainFixtureStart(t, protocols)
			protocolChainDomainTraffic(t, f, []int{1}, []int{31})
			for user := range 2 {
				for _, network := range []string{"tcp", "udp"} {
					for _, kind := range []string{"wrong-domain", "literal-ip"} {
						r := protocolChainDomainRequest(f.back[0], 0, network, 31)
						destination := f.back[0].destination(network)
						if kind == "wrong-domain" {
							destination.Address = xnet.DomainAddress("unmatched.payload.protocol-chain.test")
						}
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						c, err := core.Dial(ctx, f.clients[user].x, destination)
						if err == nil {
							err = protocolChainExchangeConn(ctx, c, network, r)
						}
						cancel()
						if err == nil {
							t.Fatalf("user%d %s %s bypassed domain-only exit route", user, network, kind)
						}
					}
				}
			}
			f.close(t)
			protocolChainDomainReceipts(t, f, [2]int{0, 1})
		})
	}
}
