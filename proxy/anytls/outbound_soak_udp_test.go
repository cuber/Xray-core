package anytls_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
)

type soakUDPRecord struct {
	ID        uint64
	Length    int
	Hash      [32]byte
	Source    string
	Sent      int
	SendError string
}

// Opt-in differential: each API uses the same real AnyTLS outbound and echo.
// A failure logs wire-side evidence instead of retrying the datagram or raising
// its deadline. The original ten-minute soak failure remains a separate result.
func TestAnyTLSSoakUDPDifferential(t *testing.T) {
	text := os.Getenv("ANYTLS_UDP_ITERATIONS")
	if text == "" {
		t.Skip("set ANYTLS_UDP_ITERATIONS for bounded packet differential")
	}
	rounds, err := strconv.Atoi(text)
	if err != nil || rounds < 1 || rounds > 10000 {
		t.Fatal("ANYTLS_UDP_ITERATIONS must be 1..10000")
	}
	for _, mode := range []string{"cnc-direct", "packetconn-direct", "cnc", "packetconn", "uot"} {
		for _, churn := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/churn=%v", mode, churn), func(t *testing.T) {
				api := strings.TrimSuffix(mode, "-direct")
				tag := "client"
				if api != mode {
					tag = "chain-exit"
				}
				f, _ := outboundFixture(t, "", func(config map[string]any) {
					if os.Getenv("ANYTLS_UDP_BIND_IPV4") == "1" {
						for _, raw := range config["outbounds"].([]any) {
							out := raw.(map[string]any)
							if out["tag"] == "chain-exit" {
								out["sendThrough"] = "127.0.0.1"
							}
						}
					}
					routes := config["routing"].(map[string]any)
					routes["rules"] = append([]any{map[string]any{"type": "field", "inboundTag": []string{"anytls-remote"}, "outboundTag": "chain-exit"}}, routes["rules"].([]any)...)
				})
				echo, err := net.ListenPacket("udp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				var mu sync.Mutex
				records := make(map[uint64]soakUDPRecord)
				wire := &soakUDPWireDialer{SystemDialer: &internet.DefaultSystemDialer{}, reads: make(map[string][]soakUDPRecord)}
				internet.UseAlternativeSystemDialer(wire)
				t.Cleanup(func() { f.instance.Close(); internet.UseAlternativeSystemDialer(nil) })
				done := make(chan struct{})
				go func() {
					defer close(done)
					packet := make([]byte, 65535)
					for {
						n, peer, err := echo.ReadFrom(packet)
						if err != nil {
							return
						}
						var id uint64
						if n >= 8 {
							id = binary.BigEndian.Uint64(packet[:8])
						}
						// Publish receive/send evidence together so a fast response
						// cannot race the fixture record's completion.
						mu.Lock()
						sent, sendErr := echo.WriteTo(packet[:n], peer)
						records[id] = soakUDPRecord{ID: id, Length: n, Hash: sha256.Sum256(packet[:n]), Source: peer.String(), Sent: sent, SendError: fmt.Sprint(sendErr)}
						mu.Unlock()
						if sendErr != nil {
							return
						}
					}
				}()
				t.Cleanup(func() { echo.Close(); soakWait(t, done, "differential echo") })
				target := echo.LocalAddr()
				dest, err := xnet.ParseDestination("udp:" + target.String())
				if err != nil {
					t.Fatal(err)
				}
				if mode == "uot" {
					if err := f.alter("udp-differential", "udp-secret", false); err != nil {
						t.Fatal(err)
					}
				}
				engineClient := f.client(t, "udp-secret")
				var workers sync.WaitGroup
				for worker := 0; worker < 4; worker++ {
					workers.Add(1)
					go func() {
						defer workers.Done()
						var closeConn func()
						defer func() {
							if closeConn != nil {
								closeConn()
							}
						}()
						var transfer func([]byte) ([]byte, string, error)
						open := func() error {
							ctx, cancel := context.WithCancel(session.SetForcedOutboundTagToContext(context.Background(), tag))
							switch api {
							case "cnc":
								conn, err := core.Dial(ctx, f.instance, dest)
								if err != nil {
									cancel()
									return err
								}
								closeConn = func() { cancel(); conn.Close() }
								transfer = func(payload []byte) ([]byte, string, error) {
									timer := time.AfterFunc(3*time.Second, closeConn)
									defer timer.Stop()
									if _, err := conn.Write(payload); err != nil {
										return nil, "", err
									}
									response := make([]byte, 65535)
									// UDP Read is one packet, never io.ReadFull across packets.
									n, err := conn.Read(response)
									return response[:n], "CNC does not expose reply endpoint", err
								}
							case "packetconn":
								conn, err := core.DialUDP(ctx, f.instance)
								if err != nil {
									cancel()
									return err
								}
								closeConn = func() { cancel(); conn.Close() }
								transfer = func(payload []byte) ([]byte, string, error) {
									timer := time.AfterFunc(3*time.Second, closeConn)
									defer timer.Stop()
									if _, err := conn.WriteTo(payload, target); err != nil {
										return nil, "", err
									}
									response := make([]byte, 65535)
									n, source, err := conn.ReadFrom(response)
									if err != nil {
										return response[:n], "", err
									}
									return response[:n], source.String(), nil
								}
							case "uot":
								opening, stop := context.WithTimeout(ctx, 3*time.Second)
								conn, err := engineClient.DialContext(opening, M.Socksaddr{Fqdn: uot.MagicAddress, Port: 443})
								stop()
								if err != nil {
									cancel()
									return err
								}
								closeConn = func() { cancel(); conn.Close() }
								conn.SetDeadline(time.Now().Add(3 * time.Second))
								request := uot.Request{Destination: M.ParseSocksaddr(target.String())}
								if err := uot.WriteRequest(conn, request); err != nil {
									closeConn()
									return err
								}
								packets := uot.NewConn(conn, request)
								transfer = func(payload []byte) ([]byte, string, error) {
									conn.SetDeadline(time.Now().Add(3 * time.Second))
									if err := packets.WritePacket(B.As(payload), M.ParseSocksaddr(target.String())); err != nil {
										return nil, "", err
									}
									response := B.NewSize(65535)
									defer response.Release()
									from, err := packets.ReadPacket(response)
									return append([]byte(nil), response.Bytes()...), from.String(), err
								}
							default:
								cancel()
								return fmt.Errorf("unsupported UDP differential API %q", api)
							}
							return nil
						}
						for round := 0; round < rounds; round++ {
							if closeConn == nil || churn {
								if closeConn != nil {
									closeConn()
								}
								if err := open(); err != nil {
									t.Error(err)
									return
								}
							}
							id := uint64(worker+1)<<32 | uint64(round+1)
							payload := make([]byte, 8192)
							binary.BigEndian.PutUint64(payload, id)
							for i := 8; i < len(payload); i++ {
								payload[i] = byte(i*31 + round + worker)
							}
							got, source, err := transfer(payload)
							mu.Lock()
							record, seen := records[id]
							mu.Unlock()
							if err != nil || !bytes.Equal(got, payload) || !seen || record.Length != len(payload) || record.Sent != len(payload) || record.Hash != sha256.Sum256(payload) || (api != "cnc" && source != target.String()) {
								t.Errorf("id=%x mode=%s churn=%v wantLen=%d gotLen=%d wantHash=%x gotHash=%x replySource=%s echoSeen=%v echo=%+v err=%v", id, mode, churn, len(payload), len(got), sha256.Sum256(payload), sha256.Sum256(got), source, seen, record, err)
								_, port, _ := net.SplitHostPort(record.Source)
								wire.mu.Lock()
								trace := append([]soakUDPRecord(nil), wire.reads[port]...)
								wire.mu.Unlock()
								t.Logf("Core system UDP ReadFrom source-port=%s trace=%+v", port, trace)
								return
							}
						}
					}()
				}
				joined := make(chan struct{})
				go func() { workers.Wait(); close(joined) }()
				select {
				case <-joined:
				case <-time.After(90 * time.Second):
					t.Fatal("differential exceeded 90s bound")
				}
				wire.mu.Lock()
				observedPorts := len(wire.reads)
				wire.mu.Unlock()
				t.Logf("mode=%s churn=%v workers=4 iterations=%d ipv4bind=%s observedCoreReadPorts=%d owned workers joined", mode, churn, rounds, os.Getenv("ANYTLS_UDP_BIND_IPV4"), observedPorts)
				if err := f.instance.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

// Diagnose the host socket allocator separately from all Core/AnyTLS code.
// Only our own sockets receive the probe: no production processes are stopped.
func TestAnyTLSSoakUDPWildcardCollision(t *testing.T) {
	if os.Getenv("ANYTLS_UDP_ITERATIONS") == "" || runtime.GOOS != "darwin" {
		t.Skip("opt-in Darwin socket allocator diagnostic")
	}
	occupied := make(map[int]*net.UDPConn)
	for range 64 {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		occupied[conn.LocalAddr().(*net.UDPAddr).Port] = conn
	}
	echo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	for attempt := 0; attempt < 20000; attempt++ {
		conn, err := net.ListenPacket("udp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		port := conn.LocalAddr().(*net.UDPAddr).Port
		blocker := occupied[port]
		if blocker == nil {
			conn.Close()
			continue
		}
		defer conn.Close()
		payload := []byte("socket-allocator-no-core-no-anytls")
		conn.SetDeadline(time.Now().Add(time.Second))
		echo.SetDeadline(time.Now().Add(time.Second))
		blocker.SetDeadline(time.Now().Add(time.Second))
		if _, err := conn.WriteTo(payload, echo.LocalAddr()); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 8192)
		n, source, err := echo.ReadFrom(buffer)
		if err != nil || !bytes.Equal(buffer[:n], payload) {
			t.Fatal("echo receive", err)
		}
		if _, err := echo.WriteTo(buffer[:n], source); err != nil {
			t.Fatal(err)
		}
		n, from, err := blocker.ReadFrom(buffer)
		if err != nil || !bytes.Equal(buffer[:n], payload) {
			t.Fatalf("allocated overlap but wrong socket not selected: %v", err)
		}
		t.Logf("PROVEN attempt=%d wildcard=%s existingIPv4=%s echo=%s source=%s replyReceivedByExistingIPv4From=%s bytes=%d", attempt, conn.LocalAddr(), blocker.LocalAddr(), echo.LocalAddr(), source, from, n)
		return
	}
	t.Skip("no wildcard/IPv4 allocator collision observed within 20000 allocations")
}

// Passive instrumentation at the actual UDP socket, before freedom/UoT framing.
// Preserve PacketConnWrapper so freedom retains its packet-oriented reader.
type soakUDPWireDialer struct {
	internet.SystemDialer
	mu    sync.Mutex
	reads map[string][]soakUDPRecord
}

func (d *soakUDPWireDialer) Dial(ctx context.Context, src xnet.Address, dest xnet.Destination, opts *internet.SocketConfig) (net.Conn, error) {
	conn, err := d.SystemDialer.Dial(ctx, src, dest, opts)
	if err == nil {
		if packet, ok := conn.(*internet.PacketConnWrapper); ok {
			packet.PacketConn = &soakUDPWireConn{PacketConn: packet.PacketConn, owner: d}
		}
	}
	return conn, err
}

type soakUDPWireConn struct {
	net.PacketConn
	owner *soakUDPWireDialer
}

func (c *soakUDPWireConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, source, err := c.PacketConn.ReadFrom(p)
	if n > 0 {
		var id uint64
		if n >= 8 {
			id = binary.BigEndian.Uint64(p[:8])
		}
		_, port, _ := net.SplitHostPort(c.LocalAddr().String())
		entry := soakUDPRecord{ID: id, Length: n, Hash: sha256.Sum256(p[:n]), Source: fmt.Sprint(source)}
		c.owner.mu.Lock()
		records := append(c.owner.reads[port], entry)
		if len(records) > 4 {
			records = records[len(records)-4:]
		}
		c.owner.reads[port] = records
		c.owner.mu.Unlock()
	}
	return n, source, err
}
