package inbound_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	handler "github.com/xtls/xray-core/app/proxyman/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/transport/internet"
	xproxy "golang.org/x/net/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func contractPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func contractEcho(t *testing.T, network, address string) (net.Listener, *atomic.Int64) {
	t.Helper()
	l, err := net.Listen(network, address)
	if err != nil {
		t.Fatal(err)
	}
	var accepts atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				io.Copy(c, c)
			}()
		}
	}()
	t.Cleanup(func() { l.Close(); wg.Wait() })
	return l, &accepts
}

func contractInstance(t *testing.T, config string) *core.Instance {
	t.Helper()
	x, err := core.StartInstance("json", []byte(config))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	return x
}

func contractExchange(c net.Conn) error {
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	payload := bytes.Repeat([]byte("0123456789abcdef"), 4096)
	written := make(chan error, 1)
	go func() { _, err := c.Write(payload); written <- err }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(c, got); err != nil {
		c.Close()
		<-written
		return err
	}
	if err := <-written; err != nil {
		return err
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("64KiB payload mismatch")
	}
	return nil
}

func contractUnixDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("UDS fixture requires POSIX socket paths; Windows TCP coverage remains enabled")
	}
	// Keep sockaddr_un paths short even when TMPDIR contains a long CI workspace.
	dir, err := os.MkdirTemp("/tmp", "xray-uds-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestListenerContractSocksAuthentication(t *testing.T) {
	echo, accepts := contractEcho(t, "tcp4", "127.0.0.1:0")
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			listen, address, port := `"127.0.0.1"`, "", contractPort(t)
			if network == "unix" {
				address = contractUnixDir(t) + "/s.sock"
				raw, _ := json.Marshal(address)
				listen = string(raw)
			} else {
				address = fmt.Sprintf("127.0.0.1:%d", port)
			}
			contractInstance(t, fmt.Sprintf(`{"inbounds":[{"listen":%s,"port":%d,"protocol":"socks","settings":{"auth":"password","accounts":[{"user":"u1","pass":"p1"}]}}],"outbounds":[{"protocol":"freedom","settings":{"ipsBlocked":[]}}]}`, listen, port))
			for _, auth := range []xproxy.Auth{{User: "u1", Password: "wrong"}, {User: "unknown", Password: "p1"}, {User: "u1", Password: "p1"}} {
				before := accepts.Load()
				d, err := xproxy.SOCKS5(network, address, &auth, &net.Dialer{Timeout: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				c, err := d.(xproxy.ContextDialer).DialContext(ctx, "tcp", echo.Addr().String())
				cancel()
				valid := auth.User == "u1" && auth.Password == "p1"
				if !valid {
					if c != nil {
						c.Close()
					}
					if err == nil {
						t.Fatal("invalid credentials accepted")
					}
					if got := accepts.Load(); got != before {
						t.Fatalf("rejected auth reached business listener: %d -> %d", before, got)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := contractExchange(c); err != nil {
					t.Fatal(err)
				}
				if accepts.Load() != before+1 {
					t.Fatal("valid auth did not reach exactly one business connection")
				}
			}
		})
	}
}

func TestListenerContractUnixDialAndFreedom(t *testing.T) {
	dir := contractUnixDir(t)
	path := dir + "/echo.sock"
	echo, _ := contractEcho(t, "unix", path)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := internet.Dial(ctx, xnet.UnixDestination(xnet.DomainAddress(path)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractExchange(c); err != nil {
		t.Fatal(err)
	}
	port := contractPort(t)
	contractInstance(t, fmt.Sprintf(`{"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1","port":80}}],"outbounds":[{"protocol":"freedom","settings":{"redirect":%q,"ipsBlocked":[]}}]}`, port, "unix:"+path))
	c, err = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractExchange(c); err != nil {
		t.Fatal(err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	for _, test := range []struct {
		name, path string
		ctx        context.Context
	}{{"missing", dir + "/missing", ctx}, {"canceled", path, canceled}} {
		t.Run(test.name, func(t *testing.T) {
			c, err := internet.Dial(test.ctx, xnet.UnixDestination(xnet.DomainAddress(test.path)), nil)
			if c != nil {
				c.Close()
			}
			if err == nil {
				t.Fatal("expected Unix dial error")
			}
		})
	}
	echo.Close()
	c, err = internet.Dial(ctx, xnet.UnixDestination(xnet.DomainAddress(path)), nil)
	if c != nil {
		c.Close()
	}
	if err == nil {
		t.Fatal("closed Unix listener accepted dial")
	}
	c, err = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte("fail"))
	var b [1]byte
	if _, err = c.Read(b[:]); err == nil {
		t.Fatal("Freedom returned data for closed UDS")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("Freedom did not close failed redirect")
	}
}

func TestListenerContractRPCFailureRetry(t *testing.T) {
	echo, _ := contractEcho(t, "tcp4", "127.0.0.1:0")
	apiPort := contractPort(t)
	contractInstance(t, fmt.Sprintf(`{"api":{"tag":"api","listen":"127.0.0.1:%d","services":["HandlerService"]},"outbounds":[{"protocol":"freedom","settings":{"ipsBlocked":[]}}]}`, apiPort))
	channel, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", apiPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	api := handler.NewHandlerServiceClient(channel)
	for _, multi := range []bool{false, true} {
		t.Run(fmt.Sprint(multi), func(t *testing.T) {
			network, address, listen := "tcp4", "127.0.0.1:0", `"127.0.0.1"`
			if multi {
				network, address, listen = "tcp6", "[::1]:0", `["127.0.0.1","::1"]`
			}
			occupied, err := net.Listen(network, address)
			if err != nil {
				t.Fatal(err)
			}
			defer occupied.Close()
			port := occupied.Addr().(*net.TCPAddr).Port
			var inbound conf.InboundDetourConfig
			if err := json.Unmarshal([]byte(fmt.Sprintf(`{"tag":"retry","listen":%s,"port":%d,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1","port":%d,"network":"tcp,udp"}}`, listen, port, echo.Addr().(*net.TCPAddr).Port)), &inbound); err != nil {
				t.Fatal(err)
			}
			built, err := inbound.Build()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err = api.AddInbound(ctx, &handler.AddInboundRequest{Inbound: built}); err == nil {
				t.Fatal("occupied port accepted")
			} else {
				t.Logf("expected AddInbound failure: %v", err)
			}
			list, err := api.ListInbounds(ctx, &handler.ListInboundsRequest{})
			if err != nil || len(list.GetInbounds()) != 0 {
				t.Fatalf("ghost handler: %v %v", list, err)
			}
			if multi {
				l, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
				if err != nil {
					t.Fatal("partial TCP listener leaked", err)
				}
				l.Close()
				u, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
				if err != nil {
					t.Fatal("partial UDP listener leaked", err)
				}
				u.Close()
			}
			occupied.Close()
			if _, err = api.AddInbound(ctx, &handler.AddInboundRequest{Inbound: built}); err != nil {
				t.Fatal("same-tag retry failed", err)
			}
			hosts := []string{"127.0.0.1"}
			if multi {
				hosts = append(hosts, "::1")
			}
			for _, host := range hosts {
				c, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if err := contractExchange(c); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = api.RemoveInbound(ctx, &handler.RemoveInboundRequest{Tag: "retry"}); err != nil {
				t.Fatal(err)
			}
			list, err = api.ListInbounds(ctx, &handler.ListInboundsRequest{})
			if err != nil || len(list.GetInbounds()) != 0 {
				t.Fatalf("removed handler still listed: %v %v", list, err)
			}
			for _, host := range hosts {
				address := net.JoinHostPort(host, fmt.Sprint(port))
				l, err := net.Listen("tcp", address)
				if err != nil {
					t.Fatal(err)
				}
				l.Close()
				u, err := net.ListenPacket("udp", address)
				if err != nil {
					t.Fatal(err)
				}
				u.Close()
			}
		})
	}
}

func TestListenerContractMultiUserAndCounters(t *testing.T) {
	echo, accepts := contractEcho(t, "tcp4", "127.0.0.1:0")
	port, apiPort := contractPort(t), contractPort(t)
	x := contractInstance(t, fmt.Sprintf(`{"api":{"tag":"api","listen":"127.0.0.1:%d","services":["HandlerService"]},"stats":{},"policy":{"system":{"statsInboundUplink":true,"statsInboundDownlink":true}},"inbounds":[{"tag":"shared","listen":["127.0.0.1","::1"],"port":%d,"protocol":"trojan","settings":{"clients":[]}}],"outbounds":[{"protocol":"freedom","settings":{"ipsBlocked":[]}}]}`, apiPort, port))
	channel, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", apiPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	api := handler.NewHandlerServiceClient(channel)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	user := &protocol.User{Email: "shared@test", Account: serial.ToTypedMessage(&trojan.Account{Password: "contract-secret"})}
	if _, err = api.AlterInbound(ctx, &handler.AlterInboundRequest{Tag: "shared", Operation: serial.ToTypedMessage(&handler.AddUserOperation{User: user})}); err != nil {
		t.Fatal(err)
	}
	account, err := (&trojan.Account{Password: "contract-secret"}).AsAccount()
	if err != nil {
		t.Fatal(err)
	}
	target := xnet.TCPDestination(xnet.LocalHostIP, xnet.Port(echo.Addr().(*net.TCPAddr).Port))
	manager := x.GetFeature(stats.ManagerType()).(stats.Manager)
	up, down := manager.GetCounter("inbound>>>shared>>>traffic>>>uplink"), manager.GetCounter("inbound>>>shared>>>traffic>>>downlink")
	if up == nil || down == nil {
		t.Fatal("missing shared counters")
	}
	var firstUp, firstDown int64
	for i, host := range []string{"127.0.0.1", "::1"} {
		beforeUp, beforeDown := up.Value(), down.Value()
		c, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(3 * time.Second))
		writer := &trojan.ConnWriter{Writer: c, Target: target, Account: account.(*trojan.MemoryAccount)}
		payload := bytes.Repeat([]byte("s"), 65536)
		if _, err := writer.Write(payload); err != nil {
			c.Close()
			t.Fatal(err)
		}
		got := make([]byte, len(payload))
		_, err = io.ReadFull(c, got)
		c.Close()
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatal("shared user exchange", err)
		}
		du, dd := up.Value()-beforeUp, down.Value()-beforeDown
		if dd != 65536 || du != 65536+68 {
			t.Fatalf("unexpected shared tag delta up=%d down=%d", du, dd)
		}
		if i == 0 {
			firstUp, firstDown = du, dd
		} else if du != firstUp || dd != firstDown {
			t.Fatal("different accounting across addresses")
		}
	}
	if accepts.Load() != 2 {
		t.Fatal("expected two authenticated business connections")
	}
	if _, err = api.AlterInbound(ctx, &handler.AlterInboundRequest{Tag: "shared", Operation: serial.ToTypedMessage(&handler.RemoveUserOperation{Email: user.Email})}); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1", "::1"} {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(time.Second))
		w := &trojan.ConnWriter{Writer: c, Target: target, Account: account.(*trojan.MemoryAccount)}
		w.Write([]byte("rejected"))
		var b [1]byte
		_, err = c.Read(b[:])
		c.Close()
		if err == nil {
			t.Fatal("removed user accepted")
		}
	}
	if accepts.Load() != 2 {
		t.Fatal("removed user reached business listener")
	}
}
