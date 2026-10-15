//go:build darwin || linux

package scenarios

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestProtocolChainOutage(t *testing.T) {
	protocolChainNetworkWindow(t)
	for _, protocols := range protocolChainMatrix(2) {
		for failedHop := range 2 {
			t.Run(fmt.Sprintf("%s/hop%d", strings.Join(protocols, "_"), failedHop), func(t *testing.T) {
				f := protocolChainFixtureStart(t, protocols, false)
				b := f.back[0]
				want := make(map[*protocolChainReceipt]int)
				request := func(network string, succeeds bool) {
					t.Helper()
					r := b.request(network, 257, false)
					budget := 2 * time.Second
					if succeeds {
						budget = 5 * time.Second
						want[r] = 1
					} else {
						want[r] = 0
					}
					ctx, cancel := context.WithTimeout(context.Background(), budget)
					err := protocolChainExchange(ctx, f.clients[0], b, network, r)
					cancel()
					if succeeds && err != nil {
						t.Fatalf("first new %s request after restore: %v", network, err)
					}
					if !succeeds && err == nil {
						t.Fatalf("%s escaped stopped hop%d", network, failedHop)
					}
				}
				both := func(succeeds bool) {
					t.Helper()
					for _, network := range []string{"tcp", "udp"} {
						request(network, succeeds)
					}
				}
				// Cold: no client has dialed/authenticated before this hop stops.
				f.servers[failedHop].close(t)
				both(false)
				f.servers[failedHop] = protocolChainStart(t, f.configs[failedHop])
				both(true)

				// Established: the complete non-idempotent request was received by
				// the destination, but its response has intentionally been withheld.
				r := b.request("tcp", 513, true)
				want[r] = 1
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
				defer cancel()
				result := make(chan error, 1)
				joined := make(chan struct{})
				go func() {
					defer close(joined)
					result <- protocolChainExchange(ctx, f.clients[0], b, "tcp", r)
				}()
				t.Cleanup(func() { cancel(); protocolChainWait(t, joined, 5*time.Second, "outage request cleanup") })
				protocolChainWait(t, r.seen, 5*time.Second, "established destination receipt")
				f.servers[failedHop].close(t)
				// Native AnyTLS defaults may require pool timeout + scan + QUIC
				// idle (30+30+30s). This bounds a protocol-close barrier, not retry.
				protocolChainWait(t, joined, 100*time.Second, "established request protocol close")
				if err := <-result; err == nil || ctx.Err() != nil {
					t.Fatalf("expected transport failure before local cancellation: err=%v ctx=%v", err, ctx.Err())
				}
				cancel()
				both(false)
				f.servers[failedHop] = protocolChainStart(t, f.configs[failedHop])
				both(true)
				f.close(t)
				b.mu.Lock()
				defer b.mu.Unlock()
				for r, count := range want {
					if r.count != count {
						t.Errorf("final once-only/no-DIRECT receipt count=%d want=%d hold=%t", r.count, count, r.hold)
					}
				}
			})
		}
	}
}

func TestProtocolChainCancellation(t *testing.T) {
	protocolChainNetworkWindow(t)
	for _, protocols := range protocolChainMatrix(2) {
		t.Run(strings.Join(protocols, "_"), func(t *testing.T) {
			f := protocolChainFixtureStart(t, protocols, false)
			b := f.back[0]
			for _, network := range []string{"tcp", "udp"} {
				r := b.request(network, 513, true)
				ctx, cancel := context.WithCancel(context.Background())
				result := make(chan error, 1)
				joined := make(chan struct{})
				go func() {
					defer close(joined)
					result <- protocolChainExchange(ctx, f.clients[0], b, network, r)
				}()
				t.Cleanup(func() { cancel(); protocolChainWait(t, joined, 5*time.Second, "cancellation cleanup") })
				protocolChainWait(t, r.seen, 5*time.Second, "request delivery before cancellation")
				cancel()
				protocolChainWait(t, joined, 5*time.Second, "canceled request worker")
				if err := <-result; err == nil {
					t.Fatal("withheld response succeeded after cancellation")
				}
				// Both another identity and a fresh request from the canceled
				// identity must work without replacing native outbound handlers.
				for _, user := range []int{1, 0} {
					next := b.request(network, 1200, false)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					err := protocolChainExchange(ctx, f.clients[user], b, network, next)
					cancel()
					if err != nil {
						t.Fatalf("user%d %s after cancellation: %v", user, network, err)
					}
				}
			}
			f.close(t)
			b.mu.Lock()
			defer b.mu.Unlock()
			for _, r := range b.receipts {
				if r.count != 1 {
					t.Errorf("cancellation/reuse receipt count=%d want=1", r.count)
				}
			}
		})
	}
}

func TestProtocolChainActiveClose(t *testing.T) {
	protocolChainNetworkWindow(t)
	for _, protocols := range protocolChainMatrix(2) {
		t.Run(strings.Join(protocols, "_"), func(t *testing.T) {
			f := protocolChainFixtureStart(t, protocols, false)
			type request struct {
				ctx     context.Context
				cancel  context.CancelFunc
				result  chan error
				joined  chan struct{}
				network string
			}
			var requests []request
			var entryPorts []int
			for user := range 2 {
				f.clients[user].close(t)
				ports := map[string]int{"tcp": protocolChainPort(t), "udp": protocolChainPort(t)}
				var ins []any
				for _, network := range []string{"tcp", "udp"} {
					dest := f.back[user].destination(network)
					ins = append(ins, map[string]any{
						"tag": network, "listen": "127.0.0.1", "port": ports[network], "protocol": "dokodemo-door",
						"settings": map[string]any{"address": "127.0.0.1", "port": int(dest.Port), "network": network},
					})
					entryPorts = append(entryPorts, ports[network])
				}
				config := f.clientConfigs[user]
				config["inbounds"] = ins
				f.clients[user] = protocolChainStart(t, config)
				for _, network := range []string{"tcp", "udp"} {
					b := f.back[user]
					r := b.request(network, 513, true)
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					c, err := net.DialTimeout(network+"4", fmt.Sprintf("127.0.0.1:%d", ports[network]), time.Second)
					if err != nil {
						cancel()
						t.Fatal(err)
					}
					result := make(chan error, 1)
					joined := make(chan struct{})
					go func() {
						defer close(joined)
						result <- protocolChainExchangeConn(ctx, c, network, r)
					}()
					t.Cleanup(func() { cancel(); protocolChainWait(t, joined, 5*time.Second, "active-close cleanup") })
					protocolChainWait(t, r.seen, 5*time.Second, "active request before Core.Close")
					requests = append(requests, request{ctx: ctx, cancel: cancel, result: result, joined: joined, network: network})
				}
			}
			// Real accepted TCP connections belong to the client Core's workers.
			// The parent UDP socket is caller-owned and has no remote EOF.
			for _, client := range f.clients {
				client.close(t)
			}
			for _, r := range requests {
				if r.network == "udp" {
					continue
				}
				protocolChainWait(t, r.joined, 5*time.Second, "active request after Core.Close")
				if err := <-r.result; !errors.Is(err, io.EOF) || r.ctx.Err() != nil {
					t.Fatalf("Core.Close did not cause accepted TCP EOF: err=%v ctx=%v", err, r.ctx.Err())
				}
				r.cancel()
			}
			for _, port := range entryPorts {
				address := fmt.Sprintf("127.0.0.1:%d", port)
				ln, err := net.Listen("tcp4", address)
				if err != nil {
					t.Fatal("client TCP listener survived Core.Close", err)
				}
				ln.Close()
				pc, err := net.ListenPacket("udp4", address)
				if err != nil {
					t.Fatal("client UDP listener survived Core.Close", err)
				}
				pc.Close()
			}
			for _, r := range requests {
				if r.network == "udp" {
					if err := r.ctx.Err(); err != nil {
						t.Fatal("UDP caller safety timeout fired before explicit cleanup", err)
					}
					r.cancel()
					protocolChainWait(t, r.joined, 5*time.Second, "caller-owned UDP socket cleanup")
					if err := <-r.result; err == nil {
						t.Fatal("withheld UDP response unexpectedly succeeded")
					}
				}
			}
			// Remote owners can legitimately retain QUIC/UDP state after a
			// client-only shutdown. Close each real service owner before judging
			// backend EOF; never manufacture it by force-closing backend sockets.
			for _, server := range f.servers {
				server.close(t)
			}
			f.close(t)
			t.Log("client Core.Close caused accepted TCP EOF and local listener retirement; UDP read workers required caller cancellation/Close; all service owners closed before natural backend EOF/worker join")
			for _, b := range f.back {
				b.mu.Lock()
				for _, r := range b.receipts {
					if r.count != 1 {
						t.Errorf("active-close receipt count=%d want=1", r.count)
					}
				}
				b.mu.Unlock()
			}
		})
	}
}

func TestProtocolChainInvalidIdentity(t *testing.T) {
	protocolChainNetworkWindow(t)
	for _, protocol := range protocolChainProtocols {
		for position := range 3 {
			t.Run(fmt.Sprintf("%s/position%d", protocol, position), func(t *testing.T) {
				protocols := []string{"ss2", "vless", "hy2"}
				protocols[position] = protocol
				f := protocolChainFixtureStart(t, protocols, false)
				// Control traffic uses the other destination. The denied target
				// must see zero requests in total, not merely zero replies.
				for _, network := range []string{"tcp", "udp"} {
					r := f.back[1].request(network, 513, false)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					err := protocolChainExchange(ctx, f.clients[0], f.back[1], network, r)
					cancel()
					if err != nil {
						t.Fatalf("valid identity control: %v", err)
					}
				}
				f.clients[0].close(t)
				config := f.clientConfigs[0]
				out := config["outbounds"].([]any)[2-position].(map[string]any)
				settings := out["settings"].(map[string]any)
				switch protocol {
				case "ss2":
					settings["password"] = protocolChainSecret(position, 9) + ":" + protocolChainSecret(position, 3)
				case "vless":
					settings["id"] = protocolChainID(position, 3)
				case "hy2":
					out["streamSettings"].(map[string]any)["hysteriaSettings"].(map[string]any)["auth"] = protocolChainSecret(position, 3)
				case "anytls":
					settings["password"] = protocolChainSecret(position, 3)
				}
				f.clients[0] = protocolChainStart(t, config)
				for _, network := range []string{"tcp", "udp"} {
					r := f.back[0].request(network, 513, false)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					err := protocolChainExchange(ctx, f.clients[0], f.back[0], network, r)
					cancel()
					if err == nil {
						t.Fatalf("wrong %s identity at position%d bypassed via %s", protocol, position, network)
					}
				}
				f.close(t)
				for i, b := range f.back {
					b.mu.Lock()
					for _, r := range b.receipts {
						if r.count != i {
							t.Errorf("destination%d receipts=%d want=%d", i, r.count, i)
						}
					}
					b.mu.Unlock()
				}
			})
		}
	}
}

func protocolChainFDs(t *testing.T) int {
	t.Helper()
	if runtime.GOOS == "linux" {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries) - 1
	}
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	count := 0
	for fd := uint64(0); fd < limit.Cur; fd++ {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			count++
		}
	}
	return count
}

func TestProtocolChainResourceRounds(t *testing.T) {
	protocolChainNetworkWindow(t)
	// Warm lazy runtime/certificate/QUIC initialization before taking a process
	// baseline. This is supportive process accounting, not an exact pool gauge.
	protocolChainHealthy(t, []string{"anytls", "hy2", "vless"}, false, false)
	runtime.GC()
	fds, goroutines := protocolChainFDs(t), runtime.NumGoroutine()
	for round := range 3 {
		for _, protocols := range [][]string{
			{"ss2", "vless", "anytls"},
			{"vless", "hy2", "ss2"},
			{"hy2", "anytls", "vless"},
			{"anytls", "ss2", "hy2"},
		} {
			t.Run(fmt.Sprintf("round%d/%s", round, strings.Join(protocols, "_")), func(t *testing.T) {
				protocolChainHealthy(t, protocols, false, false)
			})
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		runtime.GC()
		gotFDs, gotGoroutines := protocolChainFDs(t), runtime.NumGoroutine()
		if gotFDs <= fds+4 && gotGoroutines <= goroutines+8 {
			t.Logf("warmed process FD/goroutine baseline=%d/%d final=%d/%d; bounded Core.Close, joined owned backend workers and retired listeners (not an all-Core-worker gauge)", fds, goroutines, gotFDs, gotGoroutines)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("resource growth: FD %d -> %d (allow 4), goroutines %d -> %d (allow 8)", fds, gotFDs, goroutines, gotGoroutines)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
