package loopback_test

import (
	"encoding/json"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	_ "github.com/xtls/xray-core/main/distro/all"
)

func listenEcho(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// echo writes through the inbound on port and returns what comes back before the connection ends.
func echo(t *testing.T, port int) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		return ""
	}
	buf := make([]byte, 4)
	n, _ := io.ReadFull(conn, buf)
	return string(buf[:n])
}

// Core spec017 L7 (FR-007): a rule -> loopback -> the same loopback loop ends after one pass and is
// counted; a path through two different loopbacks still works.
func TestLoopbackReentry(t *testing.T) {
	target := listenEcho(t)
	loopPort, twoPort := freePort(t), freePort(t)
	dokodemo := func(tag string, port int) map[string]any {
		return map[string]any{
			"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "dokodemo-door",
			"settings": map[string]any{"address": "127.0.0.1", "port": target, "network": "tcp"},
		}
	}
	loopback := func(tag, inbound string) map[string]any {
		return map[string]any{"tag": tag, "protocol": "loopback", "settings": map[string]any{"inboundTag": inbound}}
	}
	rule := func(inbound, outbound string) map[string]any {
		return map[string]any{"type": "field", "inboundTag": []string{inbound}, "outboundTag": outbound}
	}
	config := map[string]any{
		"inbounds": []any{dokodemo("in-loop", loopPort), dokodemo("in-two", twoPort)},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom"},
			loopback("lo-a", "via-a"), loopback("lo-b", "via-b"), loopback("lo-c", "via-c"),
		},
		"routing": map[string]any{"rules": []any{
			rule("in-loop", "lo-a"), rule("via-a", "lo-a"),
			rule("in-two", "lo-b"), rule("via-b", "lo-c"), rule("via-c", "direct"),
		}},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	x, err := core.StartInstance("json", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	guard := x.GetFeature(routing.RouterType()).(routing.LoopGuard)

	if got := echo(t, twoPort); got != "ping" {
		t.Fatalf("two loopbacks echo %q, want ping", got)
	}
	if _, reentry := guard.LoopGuards(); reentry != 0 {
		t.Fatalf("loopback_reentry = %d after two different loopbacks", reentry)
	}
	if got := echo(t, loopPort); got != "" {
		t.Fatalf("a loop through lo-a echoed %q", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		chain, reentry := guard.LoopGuards()
		if reentry == 1 && chain == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("guards = %d, %d; want 0, 1", chain, reentry)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
