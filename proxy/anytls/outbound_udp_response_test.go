package anytls_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/uot"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/singbridge"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/proxy/anytls"
)

func TestAnyTLSOutboundUDPResponseBoundaries(t *testing.T) {
	modes := []string{""}
	for _, protocol := range []string{"socks", "vless", "anytls", "hysteria"} {
		for _, entrance := range []string{"proxySettings", "dialerProxy"} {
			modes = append(modes, entrance+"/"+protocol)
		}
	}
	for _, mode := range modes {
		t.Run("chain="+mode, func(t *testing.T) {
			f, config := outboundFixture(t, mode)
			certificate := f.inbound["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)["certificates"].([]any)[0].(map[string]any)
			pair, err := tls.X509KeyPair([]byte(strings.Join(certificate["certificate"].([]string), "\n")), []byte(strings.Join(certificate["key"].([]string), "\n")))
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			receipts := map[byte]int{}
			handled := make(chan struct{}, 64)
			peer := newChainCountsPeer(t, pair, "remote-secret", func(ctx context.Context, conn net.Conn, target M.Socksaddr) {
				defer func() { conn.Close(); handled <- struct{}{} }()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				if target.Fqdn != uot.MagicAddress {
					t.Error("not a UoT request", target)
					return
				}
				if err := N.ReportHandshakeSuccess(conn); err != nil {
					t.Error(err)
					return
				}
				request, err := uot.ReadRequest(conn)
				if err != nil {
					t.Error(err)
					return
				}
				packets := uot.NewConn(conn, *request)
				input := B.NewSize(65535)
				defer input.Release()
				from, err := packets.ReadPacket(input)
				if err != nil || input.Len() != 3 {
					t.Errorf("request length=%d err=%v", input.Len(), err)
					return
				}
				data := input.Bytes()
				mu.Lock()
				receipts[data[2]]++
				mu.Unlock()
				response := bytes.Repeat([]byte{data[2]}, int(binary.BigEndian.Uint16(data)))
				if err := packets.WritePacket(B.As(response), from); err != nil {
					t.Error(err)
				}
				// Wait for the consumer to close, rather than racing FIN against
				// the very response whose boundary is under test.
				io.Copy(io.Discard, conn)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			value, err := config.ProxySettings.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			value.(*anytls.ClientConfig).Server.Port = uint32(peer.listener.Addr().(*net.TCPAddr).Port)
			config.ProxySettings = serial.ToTypedMessage(value)
			if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
				t.Fatal(err)
			}
			manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
			observed := &chainCountsAlias{Handler: manager.GetHandler("client"), tag: "response-client", done: make(chan struct{}, 32)}
			if err := manager.AddHandler(ctx, observed); err != nil {
				t.Fatal(err)
			}
			dispatcher := f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher)
			id := byte(0)
			for _, address := range []string{"127.0.0.1:53", "[::1]:5353", "response.test:53"} {
				for _, size := range []int{1, 8192, 0, 1, 8193, 8192} {
					id++
					requestCtx, stop := context.WithCancel(ctx)
					feedback := &udpBoundaryErrors{}
					requestCtx = session.TrackedConnectionError(requestCtx, feedback)
					requestCtx = session.SetForcedOutboundTagToContext(requestCtx, observed.Tag())
					dest := singbridge.ToDestination(M.ParseSocksaddr(address), xnet.Network_UDP)
					link, err := dispatcher.Dispatch(requestCtx, dest)
					if err != nil {
						stop()
						t.Fatal(err)
					}
					func() {
						defer func() { stop(); common.Interrupt(link.Reader); common.Interrupt(link.Writer) }()
						packet := buf.New()
						payload := packet.Extend(3)
						binary.BigEndian.PutUint16(payload, uint16(size))
						payload[2] = id
						packet.UDP = &dest
						if err := link.Writer.WriteMultiBuffer(buf.MultiBuffer{packet}); err != nil {
							t.Fatal(err)
						}
						response, err := link.Reader.(buf.TimeoutReader).ReadMultiBufferTimeout(5 * time.Second)
						defer buf.ReleaseMulti(response)
						if size > 0 && size <= buf.Size {
							if err != nil || len(response) != 1 || response[0].UDP == nil || *response[0].UDP != dest || !bytes.Equal(response[0].Bytes(), bytes.Repeat([]byte{id}, size)) {
								t.Fatalf("response %s/%d: %v, buffers=%d", address, size, err, len(response))
							}
							stop()
						} else if err == nil || err == buf.ErrReadTimeout || !response.IsEmpty() {
							t.Fatalf("invalid response %d not explicitly rejected: %v", size, err)
						}
						obCountsWait(t, observed.done, "UDP response native dispatch")
						if (size == 0 || size > buf.Size) && !feedback.contains("invalid UDP response or unsupported payload size") {
							t.Fatalf("wrong rejection: %q", feedback.snapshot())
						}
						obCountsWait(t, handled, "UDP response peer handler")
					}()
				}
			}
			f.instance.Close()
			peer.close(t, false)
			mu.Lock()
			defer mu.Unlock()
			if len(receipts) != int(id) {
				t.Fatalf("receipts=%d want=%d", len(receipts), id)
			}
			for id, count := range receipts {
				if count != 1 {
					t.Errorf("request %d replayed %d times", id, count)
				}
			}
		})
	}
}
