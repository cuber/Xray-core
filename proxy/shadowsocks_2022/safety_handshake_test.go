package shadowsocks_2022

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/antireplay"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/transport"
)

func TestSafetyPartialHandshakeAbsoluteBudget(t *testing.T) {
	for _, variant := range []string{"single", "multi", "relay"} {
		for _, kind := range []string{"salt", "fixed", "wrong-key", "trickle", "zero-policy"} {
			t.Run(variant+"/"+kind, func(t *testing.T) {
				m := safetyMulti(t)
				defer m.Close()
				key := bytes.Repeat([]byte{2}, 16)
				if err := m.AddUser(context.Background(), &protocol.MemoryUser{Email: "a", Account: &MemoryAccount{Key: key}}); err != nil {
					t.Fatal(err)
				}
				if kind == "zero-policy" {
					m.policyManager = safetyPolicy{0}
				}
				s := &Inbound{method: m.method, psk: key, saltFilter: antireplay.NewMapFilter[[32]byte](60), policyManager: m.policyManager}
				r := &RelayInbound{method: m.method, relayPSK: m.masterPSK, policyManager: m.policyManager, destinations: map[[16]byte]*relayDest{}}
				process := s.processTCP
				keys := [][]byte{key}
				fixed := 16 + RequestHeaderFixedChunkLength + AEADTagSize
				if variant == "multi" {
					process = m.processTCP
					keys = [][]byte{m.masterPSK, key}
					fixed += 16
				}
				if variant == "relay" {
					process = r.processTCP
					keys = [][]byte{m.masterPSK, key}
					fixed = 32
				}
				if kind == "wrong-key" {
					keys = [][]byte{bytes.Repeat([]byte{9}, 16)}
				}
				var wire bytes.Buffer
				if _, err := WriteTCPRequest(&wire, m.method, keys, xnet.TCPDestination(xnet.LocalHostIP, 1234), bytes.Repeat([]byte{3}, 16), bytes.Repeat([]byte{4}, 100)); err != nil {
					t.Fatal(err)
				}
				c, raw := net.Pipe()
				defer c.Close()
				server := &safetyConn{Conn: raw}
				defer server.Close()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				d := safetyDispatcher{call: func(context.Context, xnet.Destination) (*transport.Link, error) {
					t.Error("invalid handshake dispatched")
					return nil, io.EOF
				}}
				done := make(chan error, 1)
				start := time.Now()
				go func() { done <- process(context.Background(), server, d) }()
				writes := make(chan struct{})
				go func() {
					defer close(writes)
					switch kind {
					case "salt":
						_, _ = c.Write(wire.Bytes()[:16])
					case "fixed":
						_, _ = c.Write(wire.Bytes()[:fixed-1])
					case "wrong-key":
						_, _ = c.Write(wire.Bytes())
					case "trickle":
						if _, err := c.Write(wire.Bytes()[:fixed]); err != nil {
							return
						}
						for _, b := range wire.Bytes()[fixed:] {
							time.Sleep(100 * time.Millisecond)
							if _, err := c.Write([]byte{b}); err != nil {
								return
							}
						}
					}
				}()
				select {
				case err := <-done:
					if err == nil {
						t.Error("invalid handshake succeeded")
					}
				case <-time.After(2 * time.Second):
					server.Close()
					<-done
					t.Error("absolute deadline exceeded")
				}
				c.Close()
				<-writes
				if time.Since(start) > 2*time.Second {
					t.Error("handshake budget renewed")
				}
				if server.closed.Load() != 1 {
					t.Fatalf("server close count=%d", server.closed.Load())
				}
			})
		}
	}
}
