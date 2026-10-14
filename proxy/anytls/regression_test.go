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
	"testing"
	"time"

	handler "github.com/xtls/xray-core/app/proxyman/command"
	"golang.org/x/net/proxy"
)

func TestVLESSAfterAnyTLSRemoval(t *testing.T) {
	binary := os.Getenv("ANYTLS_SINGBOX")
	if binary == "" {
		t.Skip("set ANYTLS_SINGBOX for independent VLESS regression")
	}
	f := newFixture(t, true, true)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := f.api.RemoveInbound(ctx, &handler.RemoveInboundRequest{Tag: "anytls-test"}); err != nil {
		t.Fatal(err)
	}
	port, socksPort := unusedPort(t), unusedPort(t)
	const id = "375c9138-c167-44f6-b5e8-15eae4cc4267"
	if err := f.addInbound(t, map[string]any{
		"tag": "vless-only", "listen": "127.0.0.1", "port": port, "protocol": "vless",
		"settings": map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": id, "email": "vless-regression"}}},
	}); err != nil {
		t.Fatal(err)
	}
	list, err := f.api.ListInbounds(ctx, &handler.ListInboundsRequest{})
	if err != nil || len(list.Inbounds) != 1 || list.Inbounds[0].Tag != "vless-only" {
		t.Fatal("AnyTLS handler survived removal", err)
	}
	dir := t.TempDir()
	config := map[string]any{
		"log":       map[string]any{"level": "error"},
		"inbounds":  []any{map[string]any{"type": "socks", "listen": "127.0.0.1", "listen_port": socksPort}},
		"outbounds": []any{map[string]any{"type": "vless", "server": "127.0.0.1", "server_port": port, "uuid": id}},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "client.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, binary, "run", "-c", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		cmd.Wait()
	}()
	address := fmt.Sprintf("127.0.0.1:%d", socksPort)
	for {
		c, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		if ctx.Err() != nil {
			t.Fatal("sing-box did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	dialer, err := proxy.SOCKS5("tcp", address, nil, &net.Dialer{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dialer.Dial("tcp", "alpha.test:80")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("vless")); err != nil {
		t.Fatal(err)
	}
	var reply [6]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil || string(reply[:]) != "Avless" {
		t.Fatalf("VLESS marker: %q %v", reply, err)
	}
}
