package inbound_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/infra/conf"
	_ "github.com/xtls/xray-core/main/distro/all"
)

func listenInstance(t *testing.T, port, target int) *core.Instance {
	t.Helper()
	var config conf.Config
	data := fmt.Sprintf(`{"stats":{},"policy":{"system":{"statsInboundUplink":true,"statsInboundDownlink":true}},"inbounds":[{"tag":"multi","listen":["127.0.0.1","::1"],"port":%d,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1","port":%d,"network":"tcp,udp"}}],"outbounds":[{"protocol":"freedom"}]}`, port, target)
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
			// Linux splice accounts bytes when ReadFrom returns, so finish this
			// fixed-size exchange instead of leaving the echo stream open.
			go func() { defer c.Close(); io.CopyN(c, c, 5) }()
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
	manager := instance.GetFeature(stats.ManagerType()).(stats.Manager)
	up, down := manager.GetCounter("inbound>>>multi>>>traffic>>>uplink"), manager.GetCounter("inbound>>>multi>>>traffic>>>downlink")
	if up == nil || down == nil {
		t.Fatal("missing shared inbound counters")
	}
	for _, host := range []string{"127.0.0.1", "::1"} {
		for _, network := range []string{"tcp", "udp"} {
			t.Run(host+"/"+network, func(t *testing.T) {
				beforeUp, beforeDown := up.Value(), down.Value()
				c, err := net.DialTimeout(network, net.JoinHostPort(host, fmt.Sprint(port)), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				if _, err = c.Write([]byte("hello")); err != nil {
					t.Fatal(err)
				}
				if tcp, ok := c.(*net.TCPConn); ok {
					if err := tcp.CloseWrite(); err != nil {
						t.Fatal(err)
					}
				}
				b := make([]byte, 5)
				if _, err = io.ReadFull(c, b); err != nil {
					t.Fatal(err)
				}
				if string(b) != "hello" {
					t.Fatalf("unexpected reply %q", b)
				}
				if network == "tcp" {
					if n, err := c.Read(b[:1]); n != 0 || err != io.EOF {
						t.Fatalf("echo did not finish: n=%d err=%v", n, err)
					}
				}
				deadline := time.Now().Add(time.Second)
				for (up.Value()-beforeUp < 5 || down.Value()-beforeDown < 5) && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if du, dd := up.Value()-beforeUp, down.Value()-beforeDown; du != 5 || dd != 5 {
					t.Fatalf("shared tag delta uplink=%d downlink=%d, want 5 each", du, dd)
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
