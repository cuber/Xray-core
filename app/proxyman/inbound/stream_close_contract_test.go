package inbound_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	xproxy "golang.org/x/net/proxy"
)

type unixSOCKSForward struct{ path string }

func (d unixSOCKSForward) Dial(_, _ string) (net.Conn, error) { return net.Dial("unix", d.path) }

func TestStreamInstanceCloseStopsForwarding(t *testing.T) {
	for _, network := range []string{"tcp", "unix"} {
		t.Run(network, func(t *testing.T) {
			backend, _ := contractEcho(t, "tcp", "127.0.0.1:0")
			port := contractPort(t)
			inbound := map[string]any{"tag": "socks", "listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"auth": "noauth"}}
			var forward xproxy.Dialer = xproxy.Direct
			address := fmt.Sprintf("127.0.0.1:%d", port)
			if network == "unix" {
				dir, err := os.MkdirTemp("", "stream-close-")
				if err != nil {
					t.Fatal(err)
				}
				defer os.RemoveAll(dir)
				address = filepath.Join(dir, "s.sock")
				inbound["listen"] = address
				delete(inbound, "port")
				forward = unixSOCKSForward{path: address}
			}
			raw, err := json.Marshal(map[string]any{
				"log":       map[string]any{"loglevel": "none", "access": "none"},
				"inbounds":  []any{inbound},
				"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow"}}}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			x := contractInstance(t, string(raw))
			dialer, err := xproxy.SOCKS5("tcp", address, nil, forward)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := dialer.Dial("tcp", backend.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			exchange := func(payload string) error {
				conn.SetDeadline(time.Now().Add(2 * time.Second))
				if _, err := io.WriteString(conn, payload); err != nil {
					return err
				}
				got := make([]byte, len(payload))
				if _, err := io.ReadFull(conn, got); err != nil {
					return err
				}
				if string(got) != payload {
					return fmt.Errorf("echo mismatch")
				}
				return nil
			}
			if err := exchange("before-close"); err != nil {
				t.Fatal(err)
			}
			if err := x.Close(); err != nil {
				t.Fatal(err)
			}
			if err := exchange("after-close"); err == nil {
				t.Fatal("closed Core still forwards accepted traffic")
			}
		})
	}
}
