package anytls_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"golang.org/x/net/proxy"
)

// Independent executable -> Core authenticated inbound -> native Core AnyTLS
// outbound -> Core remote inbound -> echo. Neither AnyTLS client leg is mocked.
func TestAnyTLSExternalClientsThroughNativeOutbound(t *testing.T) {
	for _, kind := range []string{"SINGBOX", "MIHOMO"} {
		t.Run(kind, func(t *testing.T) {
			binary := externalControlBinary(t, kind)
			binaryBytes, err := os.ReadFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s executable=%s sha256=%x", kind, binary, sha256.Sum256(binaryBytes))
			f, _ := outboundFixture(t, "", func(config map[string]any) {
				for _, raw := range config["outbounds"].([]any) {
					out := raw.(map[string]any)
					if out["tag"] == "chain-exit" {
						// Avoid Darwin wildcard UDP port collisions in the loopback fixture.
						out["sendThrough"] = "127.0.0.1"
					}
				}
				routing := config["routing"].(map[string]any)
				routing["rules"] = append([]any{map[string]any{
					"type": "field", "inboundTag": []string{"anytls-remote"}, "outboundTag": "chain-exit",
				}}, routing["rules"].([]any)...)
			})
			user, password := "external-"+strings.ToLower(kind), "external-chain-"+kind
			if err := f.alter(user, password, false); err != nil {
				t.Fatal(err)
			}
			tcpTarget := externalControlEcho(t, "tcp")
			udpTargets := []string{externalControlEcho(t, "udp"), externalControlEcho(t, "udp")}
			portOf := func(address string) int {
				t.Helper()
				_, portText, err := net.SplitHostPort(address)
				if err != nil {
					t.Fatal(err)
				}
				port, err := strconv.Atoi(portText)
				if err != nil {
					t.Fatal(err)
				}
				return port
			}
			readyTarget := externalControlEcho(t, "tcp")
			for portOf(readyTarget) == portOf(udpTargets[0]) || portOf(readyTarget) == portOf(udpTargets[1]) {
				readyTarget = externalControlEcho(t, "tcp")
			}
			dir := t.TempDir()
			// Same SNI, different trust root: a functioning SOCKS listener must
			// reject this peer instead of silently accepting an untrusted TLS leg.
			_, _, wrongCA, wrongFingerprint := externalControlCertificate(t, dir)
			caPath := filepath.Join(dir, "core-ca.pem")
			if err := os.WriteFile(caPath, f.caPEM, 0600); err != nil {
				t.Fatal(err)
			}
			startClient := func(name, trustPath, fingerprint string) (proxy.Dialer, string, func()) {
				t.Helper()
				socksPort := unusedTCPUDPPort(t)
				var config map[string]any
				if kind == "SINGBOX" {
					config = map[string]any{
						"log":      map[string]any{"level": "error"},
						"inbounds": []any{map[string]any{"type": "socks", "listen": "127.0.0.1", "listen_port": socksPort}},
						"outbounds": []any{
							map[string]any{"type": "anytls", "tag": "proxy", "server": "127.0.0.1", "server_port": portOf(f.address), "password": password,
								"tls": map[string]any{"enabled": true, "server_name": "anytls.test", "certificate_path": trustPath, "insecure": false}},
							map[string]any{"type": "direct", "tag": "ready-only"},
						},
						"route": map[string]any{"final": "proxy", "rules": []any{map[string]any{
							"network": "tcp", "port": portOf(readyTarget), "action": "route", "outbound": "ready-only",
						}}},
					}
				} else {
					config = map[string]any{
						"socks-port": socksPort, "bind-address": "127.0.0.1", "mode": "rule", "log-level": "error",
						"proxies": []any{map[string]any{"name": "proxy", "type": "anytls", "server": "127.0.0.1", "port": portOf(f.address),
							"password": password, "sni": "anytls.test", "fingerprint": fingerprint, "skip-cert-verify": false, "udp": true}},
						"rules": []string{fmt.Sprintf("DST-PORT,%d,DIRECT", portOf(readyTarget)), "MATCH,proxy"},
					}
				}
				stop := startExternalControl(t, binary, kind, filepath.Join(dir, name), config, socksPort)
				socks := fmt.Sprintf("127.0.0.1:%d", socksPort)
				dialer, err := proxy.SOCKS5("tcp", socks, nil, &net.Dialer{Timeout: 3 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				// Mihomo binds before its forwarding state is Running. Retry only
				// this isolated readiness exchange, never the acceptance workload.
				deadline := time.Now().Add(10 * time.Second)
				for {
					if err := externalControlTCP(dialer, readyTarget, []byte("ready")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("external client forwarding readiness timed out")
					}
					time.Sleep(20 * time.Millisecond)
				}
				return dialer, socks, stop
			}
			bad, _, stopBad := startClient("wrong-trust", wrongCA, wrongFingerprint)
			if err := externalControlTCP(bad, tcpTarget, []byte("untrusted-must-fail")); err == nil {
				t.Fatal("external client bypassed TLS trust verification")
			}
			stopBad()
			dialer, socks, stopClient := startClient("verified", caPath, f.caFingerprint)
			var total int64
			for _, size := range []int{1, 8192, 65535, 65536, 1024 * 1024} {
				payload := make([]byte, size)
				if _, err := rand.Read(payload); err != nil {
					t.Fatal(err)
				}
				if err := externalControlTCP(dialer, tcpTarget, payload); err != nil {
					t.Fatalf("external -> Core -> native AnyTLS -> Core TCP size=%d: %v", size, err)
				}
				total += int64(size)
			}
			for _, target := range udpTargets {
				// Validates exact payload and full SOCKS reply endpoint for each
				// 1/512/8192-byte datagram; TCP and UDP share the native outbound.
				externalControlUDP(t, socks, target)
				total += 1 + 512 + 8192
			}
			if err := externalControlTCP(dialer, tcpTarget, []byte("after-udp")); err != nil {
				t.Fatal(err)
			}
			total += int64(len("after-udp"))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			assertUser := func(name string) {
				t.Helper()
				for _, direction := range []string{"uplink", "downlink"} {
					result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>" + name + ">>>traffic>>>" + direction})
					if err != nil || result.GetStat().GetValue() != total {
						t.Fatalf("%s %s logical bytes=%v want=%d: %v", name, direction, result, total, err)
					}
				}
			}
			assertUser(user)
			assertUser("remote")
			// Echo endpoints remain reachable. Removing the actual native handler
			// must break the external path, not permit a DIRECT/freedom fallback.
			if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
				t.Fatal(err)
			}
			if err := externalControlTCP(dialer, tcpTarget, []byte("must-not-bypass-native")); err == nil {
				t.Fatal("external client reached echo without the native AnyTLS outbound")
			}
			assertUser("remote")
			stopClient()
			t.Logf("verified TLS trust rejection; TCP sizes=1/8192/65535/65536/1048576; UDP sizes=1/512/8192 x 2 endpoints; %s and remote up/down=%d/%d; no native bypass", user, total, total)
		})
	}
}
