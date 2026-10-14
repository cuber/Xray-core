package anytls_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	xrayTLS "github.com/xtls/xray-core/transport/internet/tls"
	"google.golang.org/protobuf/proto"
)

type udpBoundaryReceipt struct {
	size int
	hash [32]byte
}

func udpBoundaryKey(payload []byte) udpBoundaryReceipt {
	return udpBoundaryReceipt{len(payload), sha256.Sum256(payload)}
}

type udpBoundaryEndpoint struct {
	conn     net.PacketConn
	done     chan struct{}
	mu       sync.Mutex
	receipts map[udpBoundaryReceipt]int
	wire     []string
}

func newUDPBoundaryEndpoint(t *testing.T) *udpBoundaryEndpoint {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e := &udpBoundaryEndpoint{conn: conn, done: make(chan struct{}), receipts: make(map[udpBoundaryReceipt]int)}
	go func() {
		defer close(e.done)
		packet := make([]byte, 65535)
		for {
			n, peer, err := conn.ReadFrom(packet)
			if err != nil {
				return
			}
			if !bytes.Equal(packet[:n], []byte("udp-boundary-drain-marker")) {
				e.mu.Lock()
				e.receipts[udpBoundaryKey(packet[:n])]++
				if len(e.wire) < 64 {
					e.wire = append(e.wire, fmt.Sprintf("source=%s bytes=%d prefix=%x", peer, n, packet[:min(n, 16)]))
				}
				e.mu.Unlock()
			}
			if _, err := conn.WriteTo(packet[:n], peer); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		conn.Close()
		obCountsWait(t, e.done, "UDP endpoint worker")
		if t.Failed() {
			e.mu.Lock()
			defer e.mu.Unlock()
			for _, record := range e.wire {
				t.Logf("business endpoint=%s %s", conn.LocalAddr(), record)
			}
		}
	})
	return e
}

// Call only after Core shutdown has joined the packet producers. A round-trip
// marker drains already queued loopback datagrams before taking the final
// receipt snapshot; absence is not inferred from a short read timeout or sleep.
func (e *udpBoundaryEndpoint) drainedReceipts(t *testing.T) map[udpBoundaryReceipt]int {
	t.Helper()
	conn, err := net.DialTimeout("udp4", e.conn.LocalAddr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	marker := []byte("udp-boundary-drain-marker")
	if _, err := conn.Write(marker); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 65535)
	n, err := conn.Read(got)
	if err != nil || !bytes.Equal(got[:n], marker) {
		t.Fatalf("endpoint drain marker: %q %v", got[:n], err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make(map[udpBoundaryReceipt]int, len(e.receipts))
	for key, n := range e.receipts {
		result[key] = n
	}
	return result
}

type udpBoundaryErrors struct {
	mu     sync.Mutex
	errors []error
}

func (e *udpBoundaryErrors) SubmitError(err error) {
	e.mu.Lock()
	e.errors = append(e.errors, err)
	e.mu.Unlock()
}

func (e *udpBoundaryErrors) contains(text string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, err := range e.errors {
		if strings.Contains(err.Error(), text) {
			return true
		}
	}
	return false
}

func (e *udpBoundaryErrors) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]string, len(e.errors))
	for i, err := range e.errors {
		result[i] = err.Error()
	}
	return result
}

// This complements the clientPackets unit test and legal-packet user accounting:
// it sends an intact illegal datagram through real dispatch/native chain paths.
// core.Dial(...).Write(8193 bytes) is NOT this input: cnc.Connection.Write splits
// it into Core buffers, potentially producing two legal UDP packets (8192+1).
// Zero-length MultiBuffers are dropped by pipe/Copy before clientPackets; zero
// rejection is therefore not claimed by this dispatcher-level acceptance test.
func TestAnyTLSOutboundUDPInvalidSizeAndNoDirectBypass(t *testing.T) {
	modes := []string{""}
	for _, protocol := range []string{"socks", "vless", "anytls", "hysteria"} {
		for _, entrance := range []string{"proxySettings", "dialerProxy"} {
			modes = append(modes, entrance+"/"+protocol)
		}
	}
	for _, mode := range modes {
		t.Run("chain="+mode, func(t *testing.T) {
			endpoint := newUDPBoundaryEndpoint(t)
			f, good := outboundFixture(t, mode, func(config map[string]any) {
				routing := config["routing"].(map[string]any)
				routing["rules"] = append([]any{map[string]any{
					"type": "field", "inboundTag": []string{"anytls-remote"}, "outboundTag": "chain-exit",
				}}, routing["rules"].([]any)...)
				for _, raw := range config["outbounds"].([]any) {
					out := raw.(map[string]any)
					if out["tag"] == "chain-exit" {
						out["sendThrough"] = "127.0.0.1"
					}
				}
			})
			closeHopSockets := func() {}
			if strings.HasSuffix(mode, "/hysteria") {
				closeHopSockets = installHy2Diagnostic(t, f, endpoint.conn.LocalAddr())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
			dispatcher := f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher)
			dest, err := xnet.ParseDestination("udp:" + endpoint.conn.LocalAddr().String())
			if err != nil {
				t.Fatal(err)
			}
			observe := func() *chainCountsAlias {
				t.Helper()
				alias := &chainCountsAlias{Handler: manager.GetHandler("client"), tag: "udp-boundary-client", done: make(chan struct{}, 16)}
				if err := manager.AddHandler(ctx, alias); err != nil {
					t.Fatal(err)
				}
				return alias
			}
			observed := observe()
			replace := func(config *core.OutboundHandlerConfig) {
				t.Helper()
				old := manager.GetHandler("client").(outbound.RetiringHandler).Retirement()
				if err := manager.RemoveHandler(ctx, observed.Tag()); err != nil {
					t.Fatal(err)
				}
				if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
					t.Fatal(err)
				}
				obCountsWait(t, old.Retire(), "old native UDP pool retirement")
				if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
					t.Fatal(err)
				}
				observed = observe()
			}
			want := make(map[udpBoundaryReceipt]int)
			request := func(size int, id byte, failure string) {
				t.Helper()
				started := time.Now()
				requestCtx, stop := context.WithCancel(ctx)
				feedback := &udpBoundaryErrors{}
				requestCtx = session.TrackedConnectionError(requestCtx, feedback)
				requestCtx = session.SetForcedOutboundTagToContext(requestCtx, observed.Tag())
				link, err := dispatcher.Dispatch(requestCtx, dest)
				if err != nil {
					stop()
					t.Fatal(err)
				}
				defer func() { stop(); common.Interrupt(link.Reader); common.Interrupt(link.Writer) }()
				payload := bytes.Repeat([]byte{id}, size)
				packet := buf.NewWithSize(int32(size))
				n, err := packet.Write(payload)
				if err != nil || n != size || packet.Len() != int32(size) {
					packet.Release()
					t.Fatalf("fixture failed to construct one %d-byte packet: n=%d err=%v", size, n, err)
				}
				packet.UDP = &dest
				// A successful pipe write is only enqueueing, not protocol acceptance.
				if err := link.Writer.WriteMultiBuffer(buf.MultiBuffer{packet}); err != nil && failure == "" {
					t.Fatal(err)
				}
				reader, ok := link.Reader.(buf.TimeoutReader)
				if !ok {
					t.Fatal("dispatcher response reader has no bounded-read API")
				}
				response, readErr := reader.ReadMultiBufferTimeout(5 * time.Second)
				defer buf.ReleaseMulti(response)
				if failure == "" {
					if readErr != nil || len(response) != 1 || response[0].UDP == nil || *response[0].UDP != dest || !bytes.Equal(response[0].Bytes(), payload) {
						// EOF can arrive just before Handler.Dispatch publishes its
						// error. Join that completion before collecting diagnostics;
						// this never retries the packet or converts failure to success.
						if readErr != nil && !errors.Is(readErr, buf.ErrReadTimeout) {
							select {
							case <-observed.done:
							case <-time.After(5 * time.Second):
								t.Log("native dispatch did not finish after failed UDP read")
							}
						}
						t.Fatalf("UDP size=%d id=%x lost payload/endpoint: buffers=%d err=%v elapsed=%s ctx=%v feedback=%q", size, id, len(response), readErr, time.Since(started), requestCtx.Err(), feedback.snapshot())
					}
					want[udpBoundaryKey(payload)]++
				} else if readErr == nil || errors.Is(readErr, buf.ErrReadTimeout) || !response.IsEmpty() {
					t.Fatalf("%s must explicitly fail, not reply or merely time out: buffers=%d err=%v elapsed=%s ctx=%v feedback=%q", failure, len(response), readErr, time.Since(started), requestCtx.Err(), feedback.snapshot())
				}
				if failure == "" {
					stop()
				}
				// In negative cases wait for native completion BEFORE canceling the
				// caller, so a local deadline cannot manufacture the rejection.
				obCountsWait(t, observed.done, "native UDP dispatch completion")
				if failure == "oversize" && !feedback.contains("unsupported payload size") {
					t.Fatalf("8193-byte packet did not reach the native UDP size validator: feedback=%q", feedback.snapshot())
				}
				if failure == "broken-path" && !feedback.contains("failed to process outbound traffic") {
					t.Fatalf("broken chain did not report a native outbound failure: feedback=%q", feedback.snapshot())
				}
			}
			request(1, 0x11, "")
			request(8192, 0x12, "")
			request(8193, 0xe1, "oversize")
			request(1, 0x21, "")
			request(8192, 0x22, "")

			var restoreHop *core.OutboundHandlerConfig
			broken := proto.Clone(good).(*core.OutboundHandlerConfig)
			if mode == "" {
				value, err := broken.SenderSettings.GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				sender := value.(*proxyman.SenderConfig)
				security, err := sender.StreamSettings.SecuritySettings[0].GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				security.(*xrayTLS.Config).ServerName = "wrong-certificate.test"
				sender.StreamSettings.SecuritySettings[0] = serial.ToTypedMessage(security)
				broken.SenderSettings = serial.ToTypedMessage(sender)
			} else {
				hop := manager.GetHandler("hop")
				restoreHop = &core.OutboundHandlerConfig{Tag: "hop", SenderSettings: hop.SenderSettings(), ProxySettings: hop.ProxySettings()}
				if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "hop"}); err != nil {
					t.Fatal(err)
				}
			}
			// Discard the healthy cached inner tunnel. The remote AnyTLS listener
			// and actual UDP destination stay live: a direct bypass would be echoed
			// and recorded, including for AnyTLS-over-Hy2.
			replace(broken)
			request(1, 0xe2, "broken-path")
			request(8192, 0xe3, "broken-path")
			if restoreHop != nil {
				if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: restoreHop}); err != nil {
					t.Fatal(err)
				}
			}
			replace(good)
			request(1, 0x31, "")
			request(8192, 0x32, "")
			closed := make(chan struct{})
			go func() {
				defer close(closed)
				if err := f.instance.Close(); err != nil {
					t.Error(err)
				}
			}()
			obCountsWait(t, closed, "Core UDP producer shutdown")
			closeHopSockets()
			if got := endpoint.drainedReceipts(t); !reflect.DeepEqual(got, want) {
				t.Fatalf("UDP endpoint receipts include truncation, replay or bypass: got=%v want=%v", got, want)
			}
			t.Log("six exact legal UDP receipts; zero oversize/broken-path receipts; native completion and endpoint drain barriers joined")
		})
	}
}
