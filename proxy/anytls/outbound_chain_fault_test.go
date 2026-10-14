package anytls_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/anytls"
)

// The gate cuts actual transport sockets, not a mocked dispatcher error. TCP
// listener removal alone does not interrupt accepted SOCKS/VLESS connections.
func chainFaultGate(t *testing.T, address, target string, udp bool) func() {
	t.Helper()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var closed bool
	connections := map[net.Conn]bool{}
	if udp {
		front, err := net.ListenPacket("udp4", address)
		if err != nil {
			t.Fatal(err)
		}
		peers := map[string]net.Conn{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			buffer := make([]byte, 65535)
			for {
				n, p, err := front.ReadFrom(buffer)
				if err != nil {
					return
				}
				mu.Lock()
				if closed {
					mu.Unlock()
					return
				}
				back := peers[p.String()]
				if back == nil {
					back, err = net.Dial("udp4", target)
					if err != nil {
						mu.Unlock()
						return
					}
					peers[p.String()] = back
					wg.Add(1)
					go func(back net.Conn, peer net.Addr) {
						defer wg.Done()
						buffer := make([]byte, 65535)
						for {
							n, err := back.Read(buffer)
							if err != nil {
								return
							}
							if _, err := front.WriteTo(buffer[:n], peer); err != nil {
								return
							}
						}
					}(back, p)
				}
				mu.Unlock()
				if _, err = back.Write(buffer[:n]); err != nil {
					return
				}
			}
		}()
		return func() {
			mu.Lock()
			closed = true
			front.Close()
			for _, c := range peers {
				c.Close()
			}
			mu.Unlock()
			wg.Wait()
		}
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if closed {
				mu.Unlock()
				client.Close()
				return
			}
			connections[client] = true
			wg.Add(1)
			mu.Unlock()
			go func() {
				defer wg.Done()
				defer client.Close()
				remote, err := net.DialTimeout("tcp4", target, time.Second)
				if err != nil {
					return
				}
				defer remote.Close()
				mu.Lock()
				if closed {
					mu.Unlock()
					return
				}
				connections[remote] = true
				mu.Unlock()
				done := make(chan struct{})
				go func() { io.Copy(remote, client); remote.Close(); close(done) }()
				io.Copy(client, remote)
				client.Close()
				remote.Close()
				<-done
			}()
		}
	}()
	return func() {
		mu.Lock()
		closed = true
		listener.Close()
		for c := range connections {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	}
}

func TestAnyTLSOutboundChainFaultReceipts(t *testing.T) {
	runChainFaultCases(t, []string{"socks", "vless", "anytls", "hysteria-close-barrier"}, false)
}

// This preserves the early-recovery investigation without adding minutes of
// sleeps to the default suite. Each attempt has a distinct business ID.
func TestAnyTLSOutboundChainFaultDiagnostics(t *testing.T) {
	if os.Getenv("ANYTLS_CHAIN_FAULT_DIAGNOSTICS") != "1" {
		t.Skip("set ANYTLS_CHAIN_FAULT_DIAGNOSTICS=1 for bounded Hy2 diagnostics")
	}
	runChainFaultCases(t, []string{"hysteria", "hysteria-native", "hysteria-close-first", "hysteria-no-gate", "hysteria-native-no-gate"}, true)
}

func runChainFaultCases(t *testing.T, variants []string, diagnostic bool) {
	t.Helper()
	// An older idle TLS session can send close_notify during pool cleanup.
	// QUIC measures idle from the first ack-eliciting send after its last receive,
	// so allow one pool timeout + scan interval + QUIC idle timeout, plus margin.
	const closeBarrierBudget = (30 + 30 + 30 + 10) * time.Second
	for _, entrance := range []string{"proxySettings", "dialerProxy"} {
		for _, variant := range variants {
			t.Run(entrance+"/"+variant, func(t *testing.T) {
				protocol, _, _ := strings.Cut(variant, "-")
				f, clientConfig := outboundFixture(t, entrance+"/"+protocol, func(config map[string]any) {
					if variant == "hysteria-close-barrier" {
						config["policy"].(map[string]any)["levels"].(map[string]any)["0"].(map[string]any)["connIdle"] = 180
					}
					r := config["routing"].(map[string]any)
					r["rules"] = append([]any{map[string]any{"type": "field", "inboundTag": []string{"anytls-remote"}, "outboundTag": "chain-exit"}}, r["rules"].([]any)...)
					for _, ob := range config["outbounds"].([]any) {
						v := ob.(map[string]any)
						if v["tag"] == "chain-exit" {
							v["sendThrough"] = "127.0.0.1"
						}
					}
				})
				ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
				defer cancel()
				if raw := os.Getenv("ANYTLS_CHAIN_IDLE_SECONDS"); raw != "" {
					seconds, err := strconv.ParseUint(raw, 10, 32)
					if err != nil || seconds <= 1 || seconds > 30 {
						t.Fatal("ANYTLS_CHAIN_IDLE_SECONDS must be between 2 and 30 seconds")
					}
					settings, err := clientConfig.ProxySettings.GetInstance()
					if err != nil {
						t.Fatal(err)
					}
					client := settings.(*anytls.ClientConfig)
					client.IdleSessionCheckInterval = uint32(seconds)
					client.IdleSessionTimeout = uint32(seconds)
					clientConfig.ProxySettings = serial.ToTypedMessage(client)
					if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
						t.Fatal(err)
					}
					if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: clientConfig}); err != nil {
						t.Fatal(err)
					}
					t.Logf("diagnostic-only AnyTLS idle interval/timeout=%ds", seconds)
				}
				inbounds, err := f.api.ListInbounds(ctx, &handler.ListInboundsRequest{})
				if err != nil {
					t.Fatal(err)
				}
				var hop *core.InboundHandlerConfig
				for _, in := range inbounds.Inbounds {
					if in.Tag == "hop-in" {
						hop = in
					}
				}
				if hop == nil {
					t.Fatal("missing hop")
				}
				value, err := hop.ReceiverSettings.GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				receiver := value.(*proxyman.ReceiverConfig)
				oldPort := receiver.PortList.Range[0].From
				newPort := uint32(unusedTCPUDPPort(t))
				noGate := strings.HasSuffix(variant, "no-gate") || variant == "hysteria-close-barrier"
				if noGate {
					newPort = oldPort
				}
				receiver.PortList = &xnet.PortList{Range: []*xnet.PortRange{{From: newPort, To: newPort}}}
				hop.ReceiverSettings = serial.ToTypedMessage(receiver)
				remove := func() {
					t.Helper()
					if _, err := f.api.RemoveInbound(ctx, &handler.RemoveInboundRequest{Tag: "hop-in"}); err != nil {
						t.Fatal(err)
					}
				}
				add := func() {
					t.Helper()
					if _, err := f.api.AddInbound(ctx, &handler.AddInboundRequest{Inbound: hop}); err != nil {
						t.Fatal(err)
					}
				}
				remove()
				add()
				startGate := func() func() {
					if noGate {
						return func() {}
					}
					return chainFaultGate(t, fmt.Sprintf("127.0.0.1:%d", oldPort), fmt.Sprintf("127.0.0.1:%d", newPort), protocol == "hysteria")
				}
				stopGate := startGate()
				defer func() { stopGate() }()

				backend, err := net.Listen("tcp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				var mu sync.Mutex
				receipts := map[string]int{}
				var workers sync.WaitGroup
				received := make(chan struct{}, 16)
				release := make(chan struct{})
				workers.Add(1)
				go func() {
					defer workers.Done()
					for {
						c, err := backend.Accept()
						if err != nil {
							return
						}
						workers.Add(1)
						go func() {
							defer workers.Done()
							defer c.Close()
							c.SetDeadline(time.Now().Add(10 * time.Second))
							id, err := bufio.NewReader(c).ReadString('\n')
							if err != nil {
								return
							}
							mu.Lock()
							receipts[id]++
							mu.Unlock()
							if id == "held\n" {
								received <- struct{}{}
								<-release
							}
							io.WriteString(c, "receipt:"+id)
						}()
					}
				}()
				var backendClose sync.Once
				stopBackend := func() {
					backendClose.Do(func() { close(release); backend.Close(); workers.Wait() })
				}
				defer stopBackend()
				request := func(id string) error {
					bound := 3 * time.Second
					if variant == "hysteria-close-barrier" && id == "held" {
						bound = closeBarrierBudget
					}
					requestCtx, done := context.WithTimeout(ctx, bound)
					defer done()
					dest, err := xnet.ParseDestination("tcp:" + backend.Addr().String())
					if err != nil {
						return err
					}
					tag := "client"
					if strings.Contains(variant, "native") {
						tag = "hop"
					}
					c, err := core.Dial(session.SetForcedOutboundTagToContext(requestCtx, tag), f.instance, dest)
					if err != nil {
						return err
					}
					defer c.Close()
					stop := context.AfterFunc(requestCtx, func() { c.Close() })
					defer stop()
					if _, err = io.WriteString(c, id+"\n"); err != nil {
						return err
					}
					got, err := bufio.NewReader(c).ReadString('\n')
					if err != nil {
						if variant == "hysteria-close-barrier" && id == "held" {
							t.Logf("close barrier: request context error=%v; read error=%v", requestCtx.Err(), err)
							if requestCtx.Err() != nil {
								if os.Getenv("ANYTLS_TEST_DEBUG") != "" {
									stack := make([]byte, 1<<20)
									t.Logf("close barrier stacks:\n%s", stack[:runtime.Stack(stack, true)])
								}
								t.Error("local deadline is not a protocol-close barrier")
							}
						}
						return err
					}
					if got != "receipt:"+id+"\n" {
						return fmt.Errorf("unexpected receipt %q", got)
					}
					return nil
				}
				if err := request("before"); err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() { result <- request("held") }()
				select {
				case <-received:
				case <-time.After(4 * time.Second):
					t.Fatal("backend receipt barrier not reached")
				}
				cutAt := time.Now()
				if variant == "hysteria-close-first" {
					remove()
				} else {
					stopGate()
					remove()
				}
				if err := <-result; err == nil {
					t.Fatal("interrupted transfer succeeded")
				} else {
					t.Logf("transfer failure after hop cut: %s err=%v (normal request bound=3s; close-barrier=%s)", time.Since(cutAt), err, closeBarrierBudget)
				}
				if variant == "hysteria-close-first" {
					stopGate()
				}
				// Remote listener and backend remain live: bypassing the failed
				// middle hop would deliver this ID, which must never be received.
				if err := request("down"); err == nil {
					t.Fatal("failed hop bypassed")
				}
				add()
				stopGate = startGate()
				firstErr := request("after")
				t.Logf("independent recovery ID=after err=%v", firstErr)
				secondErr := request("after-second")
				t.Logf("independent recovery ID=after-second err=%v", secondErr)
				if !diagnostic {
					// No retry can satisfy acceptance: TCP must recover directly;
					// Hy2 must recover on the first NEW ID after observed close.
					if firstErr != nil || secondErr != nil {
						t.Errorf("post-barrier recovery failed: first=%v second=%v", firstErr, secondErr)
					}
				} else if firstErr != nil && secondErr != nil && protocol == "hysteria" {
					// Diagnostic boundary, not a retry: wait out Hy2's configured
					// default QUIC idle timeout, then send one NEW business ID.
					time.Sleep(31 * time.Second)
					lateErr := request("after-idle")
					t.Logf("independent recovery ID=after-idle (QUIC idle boundary) err=%v", lateErr)
					if lateErr != nil {
						t.Errorf("bounded recovery failed: first=%v second=%v idle=%v", firstErr, secondErr, lateErr)
					}
				} else if secondErr != nil {
					t.Errorf("second independent request failed: %v", secondErr)
				}
				if err := f.instance.Close(); err != nil {
					t.Fatal(err)
				}
				stopBackend()
				mu.Lock()
				defer mu.Unlock()
				t.Logf("all business receipts: %v", receipts)
				if receipts["before\n"] != 1 || receipts["held\n"] != 1 || receipts["down\n"] != 0 {
					t.Fatalf("replay/bypass: %v", receipts)
				}
				for id, count := range receipts {
					if count != 1 {
						t.Errorf("ID %q received %d times", id, count)
					}
				}
				if firstErr == nil && receipts["after\n"] != 1 || secondErr == nil && receipts["after-second\n"] != 1 {
					t.Error("successful response missing backend receipt")
				}
			})
		}
	}
}
