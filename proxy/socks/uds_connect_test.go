package socks_test

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/proxy/socks"
)

func TestUnixConnectReplyAcceptedByCoreClient(t *testing.T) {
	dir, err := os.MkdirTemp("", "socks-reply-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s.sock")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := listener.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(time.Second))
		io.Copy(c, c)
	}()
	defer func() { listener.Close(); <-done }()
	raw, _ := json.Marshal(map[string]any{
		"inbounds":  []any{map[string]any{"listen": path, "protocol": "socks", "settings": map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": "u", "pass": "p"}}}}},
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow"}}}}},
	})
	instance, err := core.StartInstance("json", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	user, err := (&protocol.User{Account: serial.ToTypedMessage(&socks.Account{Username: "u", Password: "p"})}).ToMemoryUser()
	if err != nil {
		t.Fatal(err)
	}
	_, err = socks.ClientHandshake(&protocol.RequestHeader{Version: 5, Command: protocol.RequestCommandTCP, Address: xnet.LocalHostIP, Port: xnet.Port(listener.Addr().(*net.TCPAddr).Port), User: user}, c, c)
	if err != nil {
		t.Fatalf("Core rejected Unix CONNECT reply: %v", err)
	}
	if _, err := c.Write([]byte("echo")); err != nil {
		t.Fatal(err)
	}
	var reply [4]byte
	if _, err := io.ReadFull(c, reply[:]); err != nil || string(reply[:]) != "echo" {
		t.Fatalf("CONNECT changed target: %q %v", reply, err)
	}
}
