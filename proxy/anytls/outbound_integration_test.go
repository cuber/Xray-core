package anytls_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/anytls"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/tls"
	"google.golang.org/protobuf/proto"
)

func outboundFixture(t *testing.T, chainMode string, customize ...func(map[string]any)) (*integrationFixture, *core.OutboundHandlerConfig) {
	t.Helper()
	port := unusedPort(t)
	f := newFixture(t, true, true, func(config map[string]any) {
		config["outbounds"] = append(config["outbounds"].([]any), map[string]any{"tag": "chain-exit", "protocol": "freedom", "settings": map[string]any{"ipsBlocked": []string{}}})
		config["policy"].(map[string]any)["system"] = map[string]any{
			"statsInboundUplink": true, "statsInboundDownlink": true, "statsOutboundUplink": true, "statsOutboundDownlink": true,
		}
		routing := config["routing"].(map[string]any)
		routing["rules"] = append([]any{
			map[string]any{"type": "field", "inboundTag": []string{"anytls-test"}, "outboundTag": "client"},
			map[string]any{"type": "field", "inboundTag": []string{"hop-in"}, "outboundTag": "chain-exit"},
		}, routing["rules"].([]any)...)
		for _, apply := range customize {
			apply(config)
		}
	})
	remote := map[string]any{}
	for k, v := range f.inbound {
		remote[k] = v
	}
	remote["tag"], remote["port"] = "anytls-remote", port
	remote["settings"] = map[string]any{"clients": []any{map[string]any{"email": "remote", "password": "remote-secret"}}}
	if err := f.addInbound(t, remote); err != nil {
		t.Fatal(err)
	}
	value := map[string]any{
		"tag": "client", "protocol": "anytls", "settings": map[string]any{"address": "127.0.0.1", "port": port, "password": "remote-secret"},
		"streamSettings": map[string]any{"network": "raw", "security": "tls", "tlsSettings": map[string]any{
			"serverName": "anytls.test", "certificates": []any{map[string]any{"usage": "verify", "certificate": strings.Split(string(f.caPEM), "\n")}},
		}},
	}
	if chainMode != "" {
		// This hop has no redirect: reaching the remote AnyTLS listener requires
		// both the handler chain and TLS to preserve the actual server endpoint.
		var hop conf.OutboundDetourConfig
		mode, protocol, _ := strings.Cut(chainMode, "/")
		if protocol == "" {
			protocol = "freedom"
		}
		hopValue := map[string]any{"tag": "hop", "protocol": protocol, "settings": map[string]any{"ipsBlocked": []string{}}}
		if protocol != "freedom" {
			hopPort := unusedTCPUDPPort(t)
			inbound := map[string]any{"tag": "hop-in", "listen": "127.0.0.1", "port": hopPort, "protocol": protocol}
			settings := map[string]any{"address": "127.0.0.1", "port": hopPort}
			hopValue["settings"] = settings
			switch protocol {
			case "socks":
				inbound["settings"] = map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": "hop", "pass": "hop-secret"}}}
				settings["user"], settings["pass"] = "hop", "hop-secret"
			case "vless":
				const id = "db96e722-a410-4e18-8d16-e33f4e5d74db"
				inbound["settings"] = map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": id, "email": "hop"}}}
				settings["id"], settings["encryption"] = id, "none"
			case "anytls", "hysteria":
				network := "raw"
				inbound["settings"] = map[string]any{"clients": []any{map[string]any{"email": "hop", "password": "hop-secret"}}}
				settings["password"] = "hop-secret"
				serverStream := map[string]any{"network": network, "security": "tls", "tlsSettings": f.inbound["streamSettings"].(map[string]any)["tlsSettings"]}
				clientStream := map[string]any{"network": network, "security": "tls", "tlsSettings": value["streamSettings"].(map[string]any)["tlsSettings"]}
				if protocol == "hysteria" {
					settings["version"] = 2
					inbound["settings"] = map[string]any{"version": 2, "clients": []any{map[string]any{"email": "hop", "auth": "hop-secret"}}}
					serverStream["network"], clientStream["network"] = "hysteria", "hysteria"
					serverStream["hysteriaSettings"] = map[string]any{"version": 2}
					clientStream["hysteriaSettings"] = map[string]any{"version": 2, "auth": "hop-secret"}
					serverStream["tlsSettings"] = map[string]any{"alpn": []string{"h3"}, "certificates": f.inbound["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)["certificates"]}
					clientStream["tlsSettings"] = map[string]any{"alpn": []string{"h3"}, "serverName": "anytls.test", "certificates": value["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)["certificates"]}
				}
				inbound["streamSettings"], hopValue["streamSettings"] = serverStream, clientStream
			}
			if err := f.addInbound(t, inbound); err != nil {
				t.Fatal(err)
			}
		}
		hopRaw, _ := json.Marshal(hopValue)
		recordAnyTLSFixture(t, "core-hop", hopRaw)
		if err := json.Unmarshal(hopRaw, &hop); err != nil {
			t.Fatal(err)
		}
		built, err := hop.Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.api.AddOutbound(context.Background(), &handler.AddOutboundRequest{Outbound: built}); err != nil {
			t.Fatal(err)
		}
		if mode == "proxySettings" {
			value["proxySettings"] = map[string]any{"tag": "hop"}
		} else {
			value["streamSettings"].(map[string]any)["sockopt"] = map[string]any{"dialerProxy": "hop"}
		}
	}
	raw, _ := json.Marshal(value)
	recordAnyTLSFixture(t, "core-outbound", raw)
	var c conf.OutboundDetourConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	built, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.AddOutbound(context.Background(), &handler.AddOutboundRequest{Outbound: built}); err != nil {
		t.Fatal(err)
	}
	return f, built
}

func TestAnyTLSOutboundStatisticsSwitches(t *testing.T) {
	for mask := range 16 {
		t.Run(fmt.Sprintf("mask=%d", mask), func(t *testing.T) {
			inbound, outbound, user, domain := mask&1 != 0, mask&2 != 0, mask&4 != 0, mask&8 != 0
			f, _ := outboundFixture(t, "", func(config map[string]any) {
				config["stats"].(map[string]any)["domainTraffic"].(map[string]any)["enabled"] = domain
				policy := config["policy"].(map[string]any)
				level := policy["levels"].(map[string]any)["0"].(map[string]any)
				level["statsUserUplink"], level["statsUserDownlink"] = user, user
				policy["system"] = map[string]any{"statsInboundUplink": inbound, "statsInboundDownlink": inbound, "statsOutboundUplink": outbound, "statsOutboundDownlink": outbound}
			})
			if err := f.alter("alice", "alice-secret", false); err != nil {
				t.Fatal(err)
			}
			if got, err := exchange(f.client(t, "alice-secret"), "alpha.test", "payload"); err != nil || got != "Apayload" {
				t.Fatal(got, err)
			}
			for prefix, enabled := range map[string]bool{"inbound>>>anytls-test": inbound, "outbound>>>client": outbound, "user>>>alice": user} {
				for direction, exact := range map[string]int64{"uplink": 7, "downlink": 8} {
					result, err := f.stats.GetStats(context.Background(), &stats.GetStatsRequest{Name: prefix + ">>>traffic>>>" + direction})
					if !enabled {
						if err == nil && result.Stat.Value != 0 {
							t.Fatal("disabled counter recorded traffic", prefix)
						}
						continue
					}
					if err != nil || result.Stat.Value <= 0 {
						t.Fatal("enabled counter missing", prefix, result, err)
					}
					if prefix == "user>>>alice" && result.Stat.Value != exact {
						t.Fatal("logical counter changed", result)
					}
				}
			}
			time.Sleep(1100 * time.Millisecond)
			buckets, err := f.stats.GetDomainTrafficBuckets(context.Background(), &stats.GetDomainTrafficBucketsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			var up, down uint64
			for _, bucket := range buckets.Buckets {
				for _, entry := range bucket.Entries {
					if entry.User == "alice" && entry.Domain == "alpha.test" {
						up += entry.UplinkBytes
						down += entry.DownlinkBytes
					}
				}
			}
			if domain && (up != 7 || down != 8) || !domain && (up != 0 || down != 0) {
				t.Fatalf("domain enabled=%v bytes=%d/%d", domain, up, down)
			}
		})
	}
}

func TestAnyTLSOutboundFailureRecovery(t *testing.T) {
	for _, failure := range []string{
		"password", "sni", "untrusted", "missing-hop", "self-cycle", "cross-cycle", "dialer-missing-hop", "dialer-self-cycle", "dialer-cross-cycle",
		"mixed-cycle-pd", "mixed-cycle-dp",
		"mixed-cycle-ppd", "mixed-cycle-pdp", "mixed-cycle-pdd", "mixed-cycle-dpp", "mixed-cycle-dpd", "mixed-cycle-ddp",
	} {
		t.Run(failure, func(t *testing.T) {
			f, good := outboundFixture(t, "")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			replace := func(config *core.OutboundHandlerConfig) {
				t.Helper()
				if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
					t.Fatal(err)
				}
				if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
					t.Fatal(err)
				}
			}
			bad := proto.Clone(good).(*core.OutboundHandlerConfig)
			if entrances, mixed := strings.CutPrefix(failure, "mixed-cycle-"); mixed {
				// Each letter selects the next edge, including the final edge back
				// to client. Cover both starts and every mixed three-node ordering.
				tags := []string{"client", "cycle-hop-1", "cycle-hop-2"}[:len(entrances)]
				for i, entrance := range entrances {
					hop := proto.Clone(good).(*core.OutboundHandlerConfig)
					hop.Tag = tags[i]
					value, err := hop.SenderSettings.GetInstance()
					if err != nil {
						t.Fatal(err)
					}
					sender := value.(*proxyman.SenderConfig)
					sender.ProxySettings = nil
					sender.StreamSettings.SocketSettings = nil
					next := tags[(i+1)%len(tags)]
					if entrance == 'p' {
						sender.ProxySettings = &internet.ProxyConfig{Tag: next}
					} else {
						sender.StreamSettings.SocketSettings = &internet.SocketConfig{DialerProxy: next}
					}
					hop.SenderSettings = serial.ToTypedMessage(sender)
					if i == 0 {
						bad = hop
					} else if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: hop}); err != nil {
						t.Fatal(err)
					}
				}
			} else if failure == "password" {
				value, _ := bad.ProxySettings.GetInstance()
				value.(*anytls.ClientConfig).Server.User.Account = serial.ToTypedMessage(&anytls.Account{Password: "incorrect"})
				bad.ProxySettings = serial.ToTypedMessage(value)
			} else {
				value, _ := bad.SenderSettings.GetInstance()
				sender := value.(*proxyman.SenderConfig)
				switch failure {
				case "missing-hop", "self-cycle", "cross-cycle", "dialer-missing-hop", "dialer-self-cycle", "dialer-cross-cycle":
					tag := "nonexistent"
					if strings.HasSuffix(failure, "self-cycle") {
						tag = "client"
					}
					setHop := func(sender *proxyman.SenderConfig, tag string) {
						if strings.HasPrefix(failure, "dialer-") {
							sender.StreamSettings.SocketSettings = &internet.SocketConfig{DialerProxy: tag}
						} else {
							sender.ProxySettings = &internet.ProxyConfig{Tag: tag}
						}
					}
					if strings.HasSuffix(failure, "cross-cycle") {
						tag = "cycle-hop"
						hop := proto.Clone(good).(*core.OutboundHandlerConfig)
						hop.Tag = tag
						hopValue, err := hop.SenderSettings.GetInstance()
						if err != nil {
							t.Fatal(err)
						}
						hopSender := hopValue.(*proxyman.SenderConfig)
						setHop(hopSender, "client")
						hop.SenderSettings = serial.ToTypedMessage(hopSender)
						if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: hop}); err != nil {
							t.Fatal(err)
						}
					}
					setHop(sender, tag)
				default:
					security, _ := sender.StreamSettings.SecuritySettings[0].GetInstance()
					settings := security.(*tls.Config)
					if failure == "sni" {
						settings.ServerName = "wrong.test"
					} else {
						settings.Certificate = nil
					}
					sender.StreamSettings.SecuritySettings[0] = serial.ToTypedMessage(settings)
				}
				bad.SenderSettings = serial.ToTypedMessage(sender)
			}
			replace(bad)
			if err := f.alter("alice", "alice-secret", false); err != nil {
				t.Fatal(err)
			}
			client := f.client(t, "alice-secret")
			started := time.Now()
			if got, err := exchange(client, "alpha.test", "must-fail"); err == nil {
				t.Fatalf("broken path bypassed validation: %q", got)
			}
			if elapsed := time.Since(started); elapsed > 5*time.Second {
				t.Fatalf("broken path exceeded bounded failure: %v", elapsed)
			}
			replace(good)
			if got, err := exchange(client, "alpha.test", "recovered"); err != nil || got != "Arecovered" {
				t.Fatalf("fresh pool did not recover: %q %v", got, err)
			}
		})
	}
}

func TestAnyTLSOutboundRoutingStatsAndChains(t *testing.T) {
	for _, mode := range []string{"", "proxySettings", "dialerProxy", "proxySettings/socks", "dialerProxy/socks", "proxySettings/vless", "dialerProxy/vless", "proxySettings/anytls", "dialerProxy/anytls", "proxySettings/hysteria", "dialerProxy/hysteria"} {
		t.Run("chain="+mode, func(t *testing.T) {
			f, _ := outboundFixture(t, mode)
			for _, user := range []string{"alice", "bob"} {
				if err := f.alter(user, user+"-secret", false); err != nil {
					t.Fatal(err)
				}
				client := f.client(t, user+"-secret")
				for i, domain := range []string{"alpha.test", "beta.test"} {
					payload := user + ":" + domain
					got, err := exchange(client, domain, payload)
					want := []string{"A", "B"}[i] + payload
					if err != nil || got != want {
						t.Fatalf("%s/%s: %q %v", user, domain, got, err)
					}
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for user, expected := range map[string][2]int64{"alice": {31, 33}, "bob": {27, 29}} {
				for direction, want := range map[string]int64{"uplink": expected[0], "downlink": expected[1]} {
					r, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>" + user + ">>>traffic>>>" + direction})
					if err != nil || r.Stat.Value != want {
						t.Fatalf("logical %s/%s: %v %v", user, direction, r, err)
					}
				}
			}
			for _, tag := range []string{"client", "hop"} {
				if tag == "hop" && mode == "" {
					continue
				}
				for _, dir := range []string{"uplink", "downlink"} {
					r, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "outbound>>>" + tag + ">>>traffic>>>" + dir})
					if err != nil || r.Stat.Value <= 32 {
						t.Fatalf("wire %s/%s: %v %v", tag, dir, r, err)
					}
				}
			}
			time.Sleep(1100 * time.Millisecond)
			buckets, err := f.stats.GetDomainTrafficBuckets(ctx, &stats.GetDomainTrafficBucketsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			totals := map[string][2]uint64{}
			for _, bucket := range buckets.Buckets {
				for _, entry := range bucket.Entries {
					key := entry.User + "/" + entry.Domain
					v := totals[key]
					totals[key] = [2]uint64{v[0] + entry.UplinkBytes, v[1] + entry.DownlinkBytes}
				}
			}
			for key, expected := range map[string][2]uint64{
				"alice/alpha.test": {16, 17}, "alice/beta.test": {15, 16},
				"bob/alpha.test": {14, 15}, "bob/beta.test": {13, 14},
			} {
				if got := totals[key]; got != expected {
					t.Fatalf("domain %s: %v want %v", key, got, expected)
				}
			}
			packets := openUoT(t, f.client(t, "alice-secret"))
			defer packets.Close()
			for index, size := range []int{1, 512, 8192} {
				payload := bytes.Repeat([]byte{byte(index + 1)}, size)
				target := M.Socksaddr{Fqdn: fmt.Sprintf("udp-%d.test", index), Port: 53}
				if err := packets.WritePacket(B.As(payload), target); err != nil {
					t.Fatal(err)
				}
				response := B.NewSize(65535)
				_, err := packets.ReadPacket(response)
				matches := bytes.Equal(payload, response.Bytes())
				response.Release()
				if err != nil || !matches {
					t.Fatalf("chained UDP %d: %v", size, err)
				}
			}
		})
	}
}

func TestAnyTLSOutboundControlPlaneValidation(t *testing.T) {
	f, good := outboundFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i, change := range []func(*core.OutboundHandlerConfig){
		func(c *core.OutboundHandlerConfig) { c.SenderSettings = nil },
		func(c *core.OutboundHandlerConfig) {
			value, _ := c.SenderSettings.GetInstance()
			sender := value.(*proxyman.SenderConfig)
			sender.MultiplexSettings = &proxyman.MultiplexingConfig{Enabled: true}
			c.SenderSettings = serial.ToTypedMessage(sender)
		},
		func(c *core.OutboundHandlerConfig) { c.ProxySettings = serial.ToTypedMessage(&anytls.ClientConfig{}) },
	} {
		bad := proto.Clone(good).(*core.OutboundHandlerConfig)
		bad.Tag = fmt.Sprintf("bad-%d", i)
		change(bad)
		if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: bad}); err == nil {
			t.Fatalf("invalid outbound %s published", bad.Tag)
		}
	}
	for _, source := range []string{"origin", "srcip"} {
		bad := proto.Clone(good).(*core.OutboundHandlerConfig)
		bad.Tag = "bad-" + source
		value, err := bad.SenderSettings.GetInstance()
		if err != nil {
			t.Fatal(err)
		}
		sender := value.(*proxyman.SenderConfig)
		sender.Via = xnet.NewIPOrDomain(xnet.DomainAddress(source))
		bad.SenderSettings = serial.ToTypedMessage(sender)
		if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: bad}); err == nil || !strings.Contains(err.Error(), "fixed sendThrough") {
			t.Fatalf("native dynamic source %s rejection: %v", source, err)
		}
	}
	list, err := f.api.ListOutbounds(ctx, &handler.ListOutboundsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range list.Outbounds {
		if strings.HasPrefix(out.Tag, "bad-") {
			t.Fatal("failed add left a registry entry")
		}
		if out.Tag == "client" {
			decoded, err := out.ProxySettings.GetInstance()
			if _, ok := decoded.(*anytls.ClientConfig); !ok || err != nil {
				t.Fatal("runtime config cannot decode AnyTLS", err)
			}
		}
	}
	for range 5 {
		if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: good}); err != nil {
			t.Fatal("same-tag recreation failed", err)
		}
	}
}
