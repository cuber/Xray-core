package anytls_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	stats "github.com/xtls/xray-core/app/stats/command"
	"golang.org/x/net/proxy"
)

// Real independent implementations, not the vendored engine's own client.
// Set ANYTLS_SINGBOX and ANYTLS_MIHOMO to the pinned executables for acceptance.
func TestExternalClients(t *testing.T) {
	if os.Getenv("ANYTLS_SINGBOX") == "" && os.Getenv("ANYTLS_MIHOMO") == "" {
		t.Skip("set ANYTLS_SINGBOX and ANYTLS_MIHOMO for independent client acceptance")
	}
	// Core owns process-global dialer state. Both clients must exercise separate
	// users of one instance, not concurrently initialize and close two instances.
	f := newFixture(t, true, true, func(config map[string]any) {
		rules := config["routing"].(map[string]any)
		rules["rules"] = append([]any{map[string]any{"type": "field", "network": "tcp", "user": []string{"mihomo"}, "outboundTag": "b"}}, rules["rules"].([]any)...)
		config["policy"].(map[string]any)["levels"].(map[string]any)["0"].(map[string]any)["bufferSize"] = 512
		if os.Getenv("ANYTLS_TEST_DEBUG") != "" {
			config["log"] = map[string]any{"loglevel": "debug", "access": "none"}
		}
	})
	hy2TLS := f.inbound["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)
	if err := f.addInbound(t, map[string]any{
		"tag": "hy2-test", "listen": "127.0.0.1", "port": f.inbound["port"], "protocol": "hysteria",
		"settings": map[string]any{"version": 2, "clients": []any{map[string]any{"email": "hy2-user", "auth": "hy2-test-secret"}}},
		"streamSettings": map[string]any{"network": "hysteria", "security": "tls",
			"hysteriaSettings": map[string]any{"version": 2},
			"tlsSettings":      map[string]any{"alpn": []string{"h3"}, "certificates": hy2TLS["certificates"]}},
	}); err != nil {
		t.Fatal("Hy2 UDP and AnyTLS TCP cannot coexist", err)
	}
	ssPort := unusedPort(t)
	const ssMaster = "MDEyMzQ1Njc4OWFiY2RlZg=="
	const ssUser = "ZmVkY2JhOTg3NjU0MzIxMA=="
	if err := f.addInbound(t, map[string]any{
		"tag": "ss2-test", "listen": "127.0.0.1", "port": ssPort, "protocol": "shadowsocks",
		"settings": map[string]any{"method": "2022-blake3-aes-128-gcm", "password": ssMaster,
			"clients": []any{map[string]any{"email": "singbox", "password": ssUser}}},
	}); err != nil {
		t.Fatal("SS-2022 regression listener", err)
	}
	for _, kind := range []string{"SINGBOX", "MIHOMO"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			binary := os.Getenv("ANYTLS_" + kind)
			if binary == "" {
				t.Skip("set ANYTLS_" + kind + " to run independent client acceptance")
			}
			password := "external-test-" + kind
			marker := "A"
			if kind == "MIHOMO" {
				marker = "B"
			}
			if err := f.alter(strings.ToLower(kind), password, false); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			caPath := filepath.Join(dir, "ca.pem")
			if err := os.WriteFile(caPath, f.caPEM, 0600); err != nil {
				t.Fatal(err)
			}
			host, portText, _ := net.SplitHostPort(f.address)
			port, _ := strconv.Atoi(portText)
			socksPort := unusedPort(t)
			hy2SocksPort := unusedPort(t)
			ssSocksPort := unusedPort(t)
			readyAddress := ""
			var config map[string]any
			configPath := filepath.Join(dir, "client.json")
			var args []string
			if kind == "SINGBOX" {
				config = map[string]any{
					"log":       map[string]any{"level": "error"},
					"inbounds":  []any{map[string]any{"type": "socks", "listen": "127.0.0.1", "listen_port": socksPort}},
					"outbounds": []any{map[string]any{"type": "anytls", "tag": "proxy", "server": host, "server_port": port, "password": password, "tls": map[string]any{"enabled": true, "server_name": "anytls.test", "certificate_path": caPath}}},
					"route":     map[string]any{"final": "proxy"},
				}
				config["inbounds"] = append(config["inbounds"].([]any), map[string]any{
					"type": "socks", "tag": "hy2-client", "listen": "127.0.0.1", "listen_port": hy2SocksPort,
				})
				config["outbounds"] = append(config["outbounds"].([]any), map[string]any{
					"type": "hysteria2", "tag": "hy2", "server": host, "server_port": port, "password": "hy2-test-secret",
					"tls": map[string]any{"enabled": true, "server_name": "anytls.test", "certificate_path": caPath},
				})
				config["route"].(map[string]any)["rules"] = []any{map[string]any{"inbound": []string{"hy2-client"}, "action": "route", "outbound": "hy2"}}
				config["inbounds"] = append(config["inbounds"].([]any), map[string]any{
					"type": "socks", "tag": "ss-client", "listen": "127.0.0.1", "listen_port": ssSocksPort,
				})
				config["outbounds"] = append(config["outbounds"].([]any), map[string]any{
					"type": "shadowsocks", "tag": "ss2", "server": host, "server_port": ssPort,
					"method": "2022-blake3-aes-128-gcm", "password": ssMaster + ":" + ssUser,
				})
				config["route"].(map[string]any)["rules"] = append(config["route"].(map[string]any)["rules"].([]any),
					map[string]any{"inbound": []string{"ss-client"}, "action": "route", "outbound": "ss2"})
				args = []string{"run", "-c", configPath}
			} else {
				readyAddress = echoTCP(t, "")
				config = map[string]any{
					"socks-port": socksPort, "bind-address": "127.0.0.1", "mode": "rule", "log-level": "debug",
					"proxies": []any{map[string]any{"name": "proxy", "type": "anytls", "server": host, "port": port, "password": password, "sni": "anytls.test", "fingerprint": f.caFingerprint, "skip-cert-verify": false, "udp": true}},
					"rules":   []string{"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve", "MATCH,proxy"},
				}
				args = []string{"-d", dir, "-f", configPath}
			}
			raw, _ := json.Marshal(config)
			recordAnyTLSFixture(t, kind, raw)
			if err := os.WriteFile(configPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cmd := exec.CommandContext(ctx, binary, args...)
			logFile, err := os.OpenFile(filepath.Join(dir, "client.log"), os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stdout, cmd.Stderr = logFile, logFile
			if err := cmd.Start(); err != nil {
				cancel()
				logFile.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { cancel(); cmd.Wait(); logFile.Close() })
			socksAddr := fmt.Sprintf("127.0.0.1:%d", socksPort)
			deadline := time.Now().Add(10 * time.Second)
			for {
				c, err := net.DialTimeout("tcp", socksAddr, 100*time.Millisecond)
				if err == nil {
					c.Close()
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("external client failed to listen")
				}
				time.Sleep(50 * time.Millisecond)
			}
			dialer, err := proxy.SOCKS5("tcp", socksAddr, nil, &net.Dialer{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			if readyAddress != "" {
				// Mihomo binds SOCKS before its tunnel enters Running. A direct
				// loopback exchange waits for that state without retrying AnyTLS.
				deadline := time.Now().Add(10 * time.Second)
				for {
					conn, err := dialer.Dial("tcp", readyAddress)
					if err == nil {
						conn.SetDeadline(time.Now().Add(time.Second))
						_, err = conn.Write([]byte("ready"))
						var reply [5]byte
						if err == nil {
							_, err = io.ReadFull(conn, reply[:])
						}
						conn.Close()
						if err == nil && string(reply[:]) == "ready" {
							break
						}
					}
					if time.Now().After(deadline) {
						t.Fatalf("external client forwarding not ready: %v", err)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			for i := 0; i < 10; i++ {
				conn, err := dialer.Dial("tcp", "alpha.test:80")
				if err != nil {
					t.Fatal(err)
				}
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, err = conn.Write([]byte("independent"))
				result := make([]byte, 12)
				if err == nil {
					_, err = io.ReadFull(conn, result)
				}
				conn.Close()
				if err != nil || string(result) != marker+"independent" {
					data, _ := os.ReadFile(filepath.Join(dir, "client.log"))
					t.Log(string(data))
					t.Fatalf("external client exchange: %q %v", result, err)
				}
			}
			testSOCKSUDP(t, socksAddr)
			if kind == "SINGBOX" {
				hy2, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", hy2SocksPort), nil, &net.Dialer{Timeout: 5 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				conn, err := hy2.Dial("tcp", "alpha.test:80")
				if err != nil {
					t.Fatal("Hy2 connection", err)
				}
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				conn.Write([]byte("hy2"))
				var response [4]byte
				_, err = io.ReadFull(conn, response[:])
				conn.Close()
				if err != nil || string(response[:]) != "Ahy2" {
					data, _ := os.ReadFile(filepath.Join(dir, "client.log"))
					t.Log(string(data))
					t.Fatal("Hy2 same-port marker", err)
				}
				before := make(map[string]int64)
				for _, direction := range []string{"uplink", "downlink"} {
					result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>singbox>>>traffic>>>" + direction})
					if err != nil {
						t.Fatal(err)
					}
					before[direction] = result.Stat.Value
				}
				ss, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", ssSocksPort), nil, &net.Dialer{Timeout: 5 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				conn, err = ss.Dial("tcp", "alpha.test:80")
				if err != nil {
					t.Fatal(err)
				}
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				conn.Write([]byte("ss2"))
				_, err = io.ReadFull(conn, response[:])
				conn.Close()
				if err != nil || string(response[:]) != "Ass2" {
					t.Fatal("SS-2022 marker", err)
				}
				for direction, delta := range map[string]int64{"uplink": 3, "downlink": 4} {
					result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>singbox>>>traffic>>>" + direction})
					if err != nil || result.Stat.Value != before[direction]+delta {
						t.Fatal("same-user cross-protocol counter did not aggregate", direction, err)
					}
				}
			}
			if durationText := os.Getenv("ANYTLS_STRESS_DURATION"); durationText != "" {
				duration, err := time.ParseDuration(durationText)
				if err != nil {
					t.Fatal(err)
				}
				before := make(map[string]int64)
				for _, direction := range []string{"uplink", "downlink"} {
					result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>" + strings.ToLower(kind) + ">>>traffic>>>" + direction})
					if err != nil {
						t.Fatal(err)
					}
					before[direction] = result.Stat.Value
				}
				up, down := testClientStress(t, dialer, duration, marker)
				for direction, delta := range map[string]int64{"uplink": up, "downlink": down} {
					result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>" + strings.ToLower(kind) + ">>>traffic>>>" + direction})
					if err != nil || result.Stat.Value != before[direction]+delta {
						t.Fatalf("stress %s accounting mismatch: result=%v baseline=%d delta=%d error=%v", direction, result, before[direction], delta, err)
					}
				}
			}
		})
	}
}

func testSOCKSUDP(t *testing.T, address string) {
	t.Helper()
	control, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	control.SetDeadline(time.Now().Add(5 * time.Second))
	control.Write([]byte{5, 1, 0})
	var reply [2]byte
	if _, err := io.ReadFull(control, reply[:]); err != nil || reply != [2]byte{5, 0} {
		t.Fatal("SOCKS greeting", err)
	}
	control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	var header [3]byte
	if _, err := io.ReadFull(control, header[:]); err != nil || header[1] != 0 {
		t.Fatal("SOCKS UDP associate", err)
	}
	relay, err := M.SocksaddrSerializer.ReadAddrPort(control)
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.DialUDP("udp", nil, relay.UDPAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	udp.SetDeadline(time.Now().Add(5 * time.Second))
	// Mihomo resolves UDP domains client-side; use a reserved test IP here.
	// Domain-preserving UoT is covered separately by TestAnyTLSUoT.
	request := []byte{0, 0, 0, 1, 192, 0, 2, 1, 0, 53}
	request = append(request, []byte("udp-independent")...)
	if _, err := udp.Write(request); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4096)
	n, err := udp.Read(buffer)
	if err != nil || !strings.HasSuffix(string(buffer[:n]), "udp-independent") {
		t.Fatal("independent client UoT failed", err)
	}
}

func testClientStress(t *testing.T, dialer proxy.Dialer, duration time.Duration, marker string) (int64, int64) {
	t.Helper()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	startGoroutines := runtime.NumGoroutine()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	started := time.Now()
	var workers sync.WaitGroup
	var up, down atomic.Int64
	errors := make(chan error, 50)
	for worker := 0; worker < 50; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			conn, err := dialer.Dial("tcp", "alpha.test:80")
			if err != nil {
				errors <- err
				return
			}
			defer conn.Close()
			payload := fmt.Sprintf("%04d%s", worker, strings.Repeat("x", 252))
			first := true
			for ctx.Err() == nil {
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				if _, err := conn.Write([]byte(payload)); err != nil {
					errors <- fmt.Errorf("worker %d write at %s (context %v): %w", worker, time.Since(started), ctx.Err(), err)
					return
				}
				up.Add(int64(len(payload)))
				want := payload
				if first {
					want = marker + want
					first = false
				}
				response := make([]byte, len(want))
				if _, err := io.ReadFull(conn, response); err != nil {
					errors <- fmt.Errorf("worker %d read at %s (context %v): %w", worker, time.Since(started), ctx.Err(), err)
					return
				}
				if string(response) != want {
					errors <- fmt.Errorf("worker %d mixed payload", worker)
					return
				}
				down.Add(int64(len(response)))
				select {
				case <-ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
				}
			}
		}(worker)
	}
	finished := make(chan struct{})
	go func() { workers.Wait(); close(finished) }()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			var usage runtime.MemStats
			runtime.ReadMemStats(&usage)
			t.Logf("stress heap=%d goroutines=%d", usage.HeapAlloc, runtime.NumGoroutine())
		case <-finished:
			close(errors)
			for err := range errors {
				t.Error(err)
			}
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			t.Logf("stress completed: 50 workers duration=%s heap=%d->%d goroutines=%d->%d", duration, before.HeapAlloc, after.HeapAlloc, startGoroutines, runtime.NumGoroutine())
			return up.Load(), down.Load()
		}
	}
}
