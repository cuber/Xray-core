package inbound_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	_ "github.com/xtls/xray-core/main/distro/all"
)

func listenInstance(t *testing.T, port, target int) *core.Instance {
	t.Helper()
	var config conf.Config
	data := fmt.Sprintf(`{"inbounds":[{"tag":"multi","listen":["127.0.0.1","::1"],"port":%d,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1","port":%d,"network":"tcp,udp"}}],"outbounds":[{"protocol":"freedom"}]}`, port, target)
	if err := json.Unmarshal([]byte(data), &config); err != nil {
		t.Fatal(err)
	}
	built, err := config.Build()
	if err != nil {
		t.Fatal(err)
	}
	instance, err := core.New(built)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { instance.Close() })
	return instance
}

func TestMultiListenTCPUDP(t *testing.T) {
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	target := echo.Addr().(*net.TCPAddr).Port
	udp, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", target))
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	go func() {
		b := make([]byte, 2048)
		for {
			n, a, e := udp.ReadFrom(b)
			if e != nil {
				return
			}
			udp.WriteTo(b[:n], a)
		}
	}()
	spare, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := spare.Addr().(*net.TCPAddr).Port
	spare.Close()
	instance := listenInstance(t, port, target)
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1", "::1"} {
		for _, network := range []string{"tcp", "udp"} {
			t.Run(host+"/"+network, func(t *testing.T) {
				c, err := net.DialTimeout(network, net.JoinHostPort(host, fmt.Sprint(port)), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				if _, err = c.Write([]byte("hello")); err != nil {
					t.Fatal(err)
				}
				b := make([]byte, 5)
				if _, err = io.ReadFull(c, b); err != nil {
					t.Fatal(err)
				}
				if string(b) != "hello" {
					t.Fatalf("unexpected reply %q", b)
				}
			})
		}
	}
}

func TestMultiListenBindFailureReleasesEarlierAddress(t *testing.T) {
	occupied, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	instance := listenInstance(t, port, 80)
	if err := instance.Start(); err == nil {
		t.Fatal("expected second address bind failure")
	}
	tcp, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("TCP listener leaked: %v", err)
	}
	tcp.Close()
	udp, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("UDP listener leaked: %v", err)
	}
	udp.Close()
}
