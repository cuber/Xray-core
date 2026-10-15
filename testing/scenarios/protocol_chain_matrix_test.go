package scenarios

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/xudp"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/hysteria"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/vless/encoding"
	"github.com/xtls/xray-core/transport/internet"
)

func protocolChainNetworkWindow(t *testing.T) {
	t.Helper()
	if os.Getenv("XRAY_PROTOCOL_CHAIN_NETWORK") != "1" {
		if os.Getenv("XRAY_PROTOCOL_CHAIN_STRICT") == "1" {
			t.Fatal("strict chain acceptance requires XRAY_PROTOCOL_CHAIN_NETWORK=1; skipping is forbidden")
		}
		t.Skip("network matrix not executed: reserve a serial socket-heavy window and set XRAY_PROTOCOL_CHAIN_NETWORK=1")
	}
}

func protocolChainMatrix(depth int) [][]string {
	if depth == 0 {
		return [][]string{{}}
	}
	var result [][]string
	for _, prefix := range protocolChainMatrix(depth - 1) {
		for _, protocol := range protocolChainProtocols {
			result = append(result, append(append([]string(nil), prefix...), protocol))
		}
	}
	return result
}

func TestProtocolChainConfigMatrix(t *testing.T) {
	pki := protocolChainCertificate(t)
	for _, depth := range []int{2, 3} {
		matrix := protocolChainMatrix(depth)
		want := 16
		if depth == 3 {
			want = 64
		}
		if len(matrix) != want {
			t.Fatalf("depth %d combinations=%d want=%d", depth, len(matrix), want)
		}
		for _, protocols := range matrix {
			t.Run(strings.Join(protocols, "_"), func(t *testing.T) {
				for user := range 2 {
					outs := make([]any, depth)
					for hop, protocol := range protocols {
						in, out := protocolChainPair(t, pki, protocol, hop, 40000+hop, user, false)
						for _, config := range []map[string]any{protocolChainBase([]any{in}, nil), protocolChainBase(nil, []any{out})} {
							if _, err := core.LoadConfig("json", bytes.NewReader(protocolChainJSON(t, config))); err != nil {
								t.Fatalf("%s hop%d user%d config: %v", protocol, hop, user, err)
							}
						}
						outs[depth-1-hop] = out
						if _, exists := out["proxySettings"]; exists {
							t.Fatal("removed legacy chain entrance")
						}
						if hop > 0 {
							got := out["streamSettings"].(map[string]any)["sockopt"].(map[string]any)["dialerProxy"]
							if got != fmt.Sprintf("hop%d", hop-1) {
								t.Fatalf("wrong predecessor: %v", got)
							}
						}
						if protocol == "ss2" {
							if protocolChainSecret(hop, 0) == protocolChainSecret(hop, 1) || protocolChainSecret(hop, 9) == protocolChainSecret(hop, user) {
								t.Fatal("SS2 identity/server keys are not distinct")
							}
						} else {
							tls := out["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)
							if tls["allowInsecure"] == true || tls["serverName"] != "protocol-chain.test" || tls["disableSystemRoot"] != true {
								t.Fatal("fixture must use explicit CA and verified SNI")
							}
						}
						if protocol == "anytls" {
							if _, exists := out["mux"]; exists || out["settings"].(map[string]any)["idleSessionTimeout"] != nil {
								t.Fatal("AnyTLS must retain the native pool and its normal idle defaults")
							}
						}
					}
					if _, err := core.LoadConfig("json", bytes.NewReader(protocolChainJSON(t, protocolChainBase(nil, outs)))); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func protocolChainHealthy(t *testing.T, protocols []string, mux, full bool) {
	t.Helper()
	f := protocolChainFixtureStart(t, protocols, mux)
	diagnostic := os.Getenv("XRAY_PROTOCOL_CHAIN_DIAGNOSTIC") == "1"
	if diagnostic {
		for hop, port := range f.ports {
			t.Logf("topology hop%d %s listen=127.0.0.1:%d", hop, protocols[hop], port)
		}
		for i, b := range f.back {
			t.Logf("topology destination%d TCP=%s UDP=%s", i, b.tcp.Addr(), b.udp.LocalAddr())
		}
	}
	for user := range 2 {
		before := make([][2]int64, len(protocols))
		otherBefore := make([][2]int64, len(protocols))
		for hop, server := range f.servers {
			before[hop] = protocolChainUserBytes(server, hop, user)
			otherBefore[hop] = protocolChainUserBytes(server, hop, 1-user)
		}
		var expected int64
		for _, network := range []string{"tcp", "udp"} {
			sizes := []int{31, 1200}
			if full {
				sizes = []int{1, 31, 512, 1200}
				if network == "tcp" {
					sizes = append(sizes, 8192, 65536)
				}
			}
			for _, backend := range f.back {
				for _, size := range sizes {
					r := backend.request(network, size, false)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					err := protocolChainExchange(ctx, f.clients[user], backend, network, r)
					cancel()
					if diagnostic {
						backend.mu.Lock()
						t.Logf("business user%d network=%s dest=%s size=%d sha=%x receipts=%d err=%v", user, network, backend.destination(network), size, sha256.Sum256(r.wire), r.count, err)
						backend.mu.Unlock()
						for hop, server := range f.servers {
							t.Logf("business hop%d user%d counters=%v", hop, user, protocolChainUserBytes(server, hop, user))
						}
					}
					if err != nil {
						backend.mu.Lock()
						t.Logf("failed request backend receipts=%d invalid=%v", r.count, backend.invalid)
						backend.mu.Unlock()
						for hop, server := range f.servers {
							t.Logf("failed request hop%d user%d counters=%v", hop, user, protocolChainUserBytes(server, hop, user))
						}
						t.Fatalf("user%d %s size=%d: %v", user, network, size, err)
					}
					expected += int64(size)
					if network == "tcp" {
						expected += 4
					}
				}
			}
		}
		// Receipt delivery precedes the dispatcher's counter publication. Wait
		// for exact values, not a sleep or a lower-bound assertion at the exit.
		deadline := time.Now().Add(3 * time.Second)
		for {
			valid := true
			for hop, server := range f.servers {
				got := protocolChainUserBytes(server, hop, user)
				other := protocolChainUserBytes(server, hop, 1-user)
				// Once user0 has run, its outer pools can still finish control
				// writes. Those wire bytes are not user1 attribution leakage.
				if (user == 0 || hop == len(protocols)-1) && other != otherBefore[hop] {
					t.Fatalf("hop%d user%d traffic charged to other user: %v -> %v", hop, user, otherBefore[hop], other)
				}
				if hop == len(protocols)-1 {
					valid = valid && got == [2]int64{before[hop][0] + expected, before[hop][1] + expected}
				} else {
					// Outer layers carry encrypted/framed inner protocol traffic.
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
				t.Fatal("authenticated per-hop/exact exit counters did not converge")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	f.close(t)
	for _, b := range f.back {
		b.mu.Lock()
		for _, r := range b.receipts {
			if r.count != 1 {
				t.Errorf("business receipt count=%d want=1", r.count)
			}
		}
		b.mu.Unlock()
	}
}

func TestProtocolChainTwoHop(t *testing.T) {
	protocolChainNetworkWindow(t)
	for _, protocols := range protocolChainMatrix(2) {
		t.Run(strings.Join(protocols, "_"), func(t *testing.T) { protocolChainHealthy(t, protocols, false, true) })
	}
}

func TestProtocolChainSS2UDPDiagnostic(t *testing.T) {
	protocolChainNetworkWindow(t)
	if os.Getenv("XRAY_PROTOCOL_CHAIN_DIAGNOSTIC") != "1" {
		t.Fatal("diagnostic gate requires XRAY_PROTOCOL_CHAIN_DIAGNOSTIC=1")
	}
	d := &protocolChainDiagnosticDialer{}
	internet.UseAlternativeSystemDialer(d)
	defer internet.UseAlternativeSystemDialer(nil)
	for round := range 50 {
		passed := t.Run(fmt.Sprintf("round%02d", round), func(t *testing.T) {
			defer func() {
				d.mu.Lock()
				events := d.events
				d.events = nil
				d.mu.Unlock()
				for _, event := range events {
					t.Log(event)
				}
			}()
			protocolChainHealthy(t, []string{"ss2", "ss2"}, false, true)
		})
		if !passed {
			return
		}
	}
}

func TestProtocolChainThreeHop(t *testing.T) {
	protocolChainNetworkWindow(t)
	for _, protocols := range protocolChainMatrix(3) {
		t.Run(strings.Join(protocols, "_"), func(t *testing.T) { protocolChainHealthy(t, protocols, false, false) })
	}
}

func TestProtocolChainVLESSMux(t *testing.T) {
	protocolChainNetworkWindow(t)
	for _, final := range protocolChainProtocols {
		t.Run("vless_"+final, func(t *testing.T) { protocolChainHealthy(t, []string{"vless", final}, true, true) })
	}
}

func TestProtocolChainTLSVerification(t *testing.T) {
	protocolChainNetworkWindow(t)
	for _, protocol := range []string{"vless", "hy2", "anytls"} {
		for _, trust := range []string{"trusted", "wrong_ca", "wrong_sni"} {
			t.Run(protocol+"/"+trust, func(t *testing.T) {
				pki := protocolChainCertificate(t)
				b := protocolChainBackendStart(t)
				in, out := protocolChainPair(t, pki, protocol, 0, protocolChainPort(t), 0, false)
				server := protocolChainStart(t, protocolChainBase([]any{in}, []any{protocolChainFreedom()}))
				tls := out["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)
				if trust == "wrong_ca" {
					tls["certificates"] = []any{map[string]any{"usage": "verify", "certificate": protocolChainCertificate(t).ca}}
				}
				if trust == "wrong_sni" {
					tls["serverName"] = "not-protocol-chain.test"
				}
				client := protocolChainStart(t, protocolChainBase(nil, []any{out}))
				r := b.request("tcp", 257, false)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := protocolChainExchange(ctx, client, b, "tcp", r)
				contextErr := ctx.Err()
				cancel()
				want := 0
				if trust == "trusted" {
					want = 1
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || contextErr != nil {
					t.Fatalf("%s must reject %s before local timeout: err=%v ctx=%v", protocol, trust, err, contextErr)
				}
				client.close(t)
				server.close(t)
				b.stop(t, false)
				b.mu.Lock()
				defer b.mu.Unlock()
				if r.count != want {
					t.Fatalf("%s backend receipts=%d want=%d", trust, r.count, want)
				}
			})
		}
	}
}

func protocolChainUDPLimit(protocol string, dest xnet.Destination) int {
	switch protocol {
	case "ss2":
		// AES client header + one EIH + fixed body + address + AEAD tag.
		return buf.Size - 16 - 16 - 11 - shadowsocks_2022.AddrPortLength(dest) - 16
	case "vless":
		return buf.Size - 2
	case "hy2":
		message := hysteria.UDPMessage{Addr: dest.NetAddr()}
		return buf.Size - message.HeaderSize()
	default:
		return buf.Size
	}
}

func TestProtocolChainUDPEncodingBoundary(t *testing.T) {
	dest := xnet.UDPDestination(xnet.LocalHostIP, 54321)
	for _, protocol := range []string{"ss2", "vless", "vless_xudp", "hy2"} {
		t.Run(protocol, func(t *testing.T) {
			limit := protocolChainUDPLimit(protocol, dest)
			if protocol == "vless_xudp" {
				limit = buf.Size - 666
			}
			for _, size := range []int{limit, limit + 1} {
				payload := make([]byte, size)
				switch protocol {
				case "ss2":
					method, err := shadowsocks_2022.GetCipherMethod("2022-blake3-aes-128-gcm")
					if err != nil {
						t.Fatal(err)
					}
					serverKey, _ := base64.StdEncoding.DecodeString(protocolChainSecret(0, 9))
					userKey, _ := base64.StdEncoding.DecodeString(protocolChainSecret(0, 0))
					codec, err := shadowsocks_2022.NewUDPPacketCodec(method, [][]byte{serverKey, userKey})
					if err != nil {
						t.Fatal(err)
					}
					session, err := codec.NewClientSession()
					if err != nil {
						t.Fatal(err)
					}
					packet, err := session.EncodePacket(dest, payload)
					if size == limit {
						if err != nil || packet == nil || packet.Len() != buf.Size {
							t.Fatalf("SS2 exact maximum encoding: %v", err)
						}
						packet.Release()
					} else if !errors.Is(err, shadowsocks_2022.ErrPacketTooLarge) || packet != nil {
						t.Fatalf("SS2 oversized packet was not rejected: %v", err)
					}
				case "vless":
					var wire bytes.Buffer
					writer := encoding.NewMultiLengthPacketWriter(buf.NewWriter(&wire))
					if err := writer.WriteMultiBuffer(buf.MultiBuffer{buf.FromBytes(payload)}); err != nil {
						t.Fatal(err)
					}
					want := 0
					if size == limit {
						want = buf.Size
					}
					if wire.Len() != want {
						t.Fatalf("VLESS encoded bytes=%d want=%d", wire.Len(), want)
					}
				case "vless_xudp":
					var wire bytes.Buffer
					writer := xudp.NewPacketWriter(buf.NewWriter(&wire), dest, [8]byte{})
					if err := writer.WriteMultiBuffer(buf.MultiBuffer{buf.FromBytes(payload)}); err != nil {
						t.Fatal(err)
					}
					want := 0
					if size == limit {
						want = limit + 16 // First IPv4 frame without a global ID.
					}
					if wire.Len() != want || size == limit && !bytes.HasSuffix(wire.Bytes(), payload) {
						t.Fatalf("VLESS XUDP encoded bytes=%d want=%d", wire.Len(), want)
					}
				case "hy2":
					message := hysteria.UDPMessage{Addr: dest.NetAddr(), FragCount: 1, Data: payload}
					n := message.Serialize(make([]byte, buf.Size))
					want := -1
					if size == limit {
						want = buf.Size
					}
					if n != want {
						t.Fatalf("Hy2 serialization=%d want=%d", n, want)
					}
				}
			}
		})
	}
}

func TestProtocolChainUDPBoundary(t *testing.T) {
	protocolChainNetworkWindow(t)
	for _, protocol := range protocolChainProtocols {
		t.Run(protocol, func(t *testing.T) {
			f := protocolChainFixtureStart(t, []string{protocol}, false)
			b := f.back[0]
			limit := protocolChainUDPLimit(protocol, b.destination("udp"))
			if protocol == "vless" {
				// Default cone mode selects XUDP for this non-53/non-443 target.
				// Its explicit writer reserve is 666, not bare VLESS's 2 bytes.
				limit = buf.Size - 666
			}
			for _, size := range []int{limit, limit + 1} {
				r := b.request("udp", size, false)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := protocolChainExchange(ctx, f.clients[0], b, "udp", r)
				cancel()
				if size == limit && err != nil {
					t.Fatalf("supported %s UDP maximum %d: %v", protocol, limit, err)
				}
				if size > limit && err == nil {
					t.Fatalf("oversized %s UDP %d unexpectedly succeeded", protocol, size)
				}
			}
			f.close(t)
			b.mu.Lock()
			defer b.mu.Unlock()
			for _, r := range b.receipts {
				want := 0
				if len(r.wire) == limit {
					want = 1
				}
				if r.count != want {
					t.Errorf("%s size%d receipts=%d want=%d", protocol, len(r.wire), r.count, want)
				}
			}
			t.Logf("%s exact UDP boundary: max=%d accepted once, max+1 delivered zero times", protocol, limit)
		})
	}
}
