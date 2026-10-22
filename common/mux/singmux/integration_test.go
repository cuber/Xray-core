package singmux_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sm "github.com/sagernet/sing-mux"
	M "github.com/sagernet/sing/common/metadata"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	statscmd "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"golang.org/x/net/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Independent clients: set SINGMUX_MIHOMO and SINGMUX_SINGBOX to executables
// (several separated by the OS path-list separator). SINGMUX_STRICT=1 fails
// instead of skipping when one is missing.

const (
	ssMethod = "2022-blake3-aes-128-gcm"
	ssMaster = "MDEyMzQ1Njc4OWFiY2RlZg=="
	// sniffPayload is an HTTP request to an IP target whose Host only sniffing reveals.
	sniffPayload = "GET / HTTP/1.1\r\nHost: sniff.test\r\n\r\n"
)

var userKeys = map[string]string{
	"mihomo":  "ZmVkY2JhOTg3NjU0MzIxMA==",
	"singbox": "YWFhYWJiYmJjY2NjZGRkZA==",
	"loop":    "MTExMTIyMjIzMzMzNDQ0NA==",
}

type fixture struct {
	ssPort      int
	loopPort    int
	loopOffPort int
	api         handler.HandlerServiceClient
	stats       statscmd.StatsServiceClient
}

func unusedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func unusedTCPUDPPort(t *testing.T) int {
	t.Helper()
	for attempt := 0; attempt < 100; attempt++ {
		port := unusedPort(t)
		if packet, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			packet.Close()
			return port
		}
	}
	t.Fatal("no port free for both TCP and UDP")
	return 0
}

// echoTCP writes its marker, then echoes.
func echoTCP(t *testing.T, marker string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Minute))
				conn.Write([]byte(marker))
				io.Copy(conn, conn)
			}()
		}
	}()
	return l.Addr().String()
}

func echoUDP(t *testing.T) string {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		data := make([]byte, 65535)
		for {
			n, addr, err := c.ReadFrom(data)
			if err != nil {
				return
			}
			c.WriteTo(data[:n], addr)
		}
	}()
	return c.LocalAddr().String()
}

func ssClients() []any {
	var clients []any
	for email, key := range userKeys {
		clients = append(clients, map[string]any{"email": email, "password": key})
	}
	return clients
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{ssPort: unusedTCPUDPPort(t), loopPort: unusedPort(t), loopOffPort: unusedPort(t)}
	offPort, apiPort := unusedPort(t), unusedPort(t)
	freedom := func(tag, redirect string) any {
		return map[string]any{"tag": tag, "protocol": "freedom", "settings": map[string]any{
			"redirect": redirect, "finalRules": []any{map[string]any{"action": "allow"}},
		}}
	}
	ssOut := func(tag string, port int, mux bool) any {
		out := map[string]any{"tag": tag, "protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{
			"address": "127.0.0.1", "port": port, "method": ssMethod, "password": ssMaster + ":" + userKeys["loop"],
		}}}}
		if mux {
			out["mux"] = map[string]any{"enabled": true, "concurrency": 8}
		}
		return out
	}
	ssIn := func(tag string, port int, singMux bool) map[string]any {
		in := map[string]any{
			"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "shadowsocks",
			"settings": map[string]any{"method": ssMethod, "password": ssMaster, "network": "tcp,udp", "clients": ssClients()},
			"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls"}},
		}
		if singMux {
			in["singMux"] = map[string]any{"enabled": true}
		}
		return in
	}
	logLevel := "error"
	if os.Getenv("SINGMUX_TEST_DEBUG") != "" {
		logLevel = "debug"
	}
	config := map[string]any{
		"log":   map[string]any{"loglevel": logLevel, "access": "none"},
		"api":   map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort), "services": []string{"HandlerService", "StatsService"}},
		"stats": map[string]any{},
		"policy": map[string]any{
			"levels": map[string]any{"0": map[string]any{"handshake": 4, "connIdle": 60, "uplinkOnly": 1, "downlinkOnly": 1, "statsUserUplink": true, "statsUserDownlink": true}},
			"system": map[string]any{"statsInboundUplink": true, "statsInboundDownlink": true},
		},
		"inbounds": []any{
			ssIn("ss-in", f.ssPort, true),
			ssIn("ss-off", offPort, false),
			map[string]any{"tag": "loop-in", "listen": "127.0.0.1", "port": f.loopPort, "protocol": "socks"},
			map[string]any{"tag": "loop-off-in", "listen": "127.0.0.1", "port": f.loopOffPort, "protocol": "socks"},
		},
		"outbounds": []any{
			freedom("a", echoTCP(t, "A")), freedom("b", echoTCP(t, "B")), freedom("udp", echoUDP(t)),
			ssOut("loop-ss", f.ssPort, false), ssOut("loop-ss-mux", f.ssPort, true), ssOut("loop-ss-off", offPort, false),
		},
		"routing": map[string]any{"rules": []any{
			map[string]any{"type": "field", "inboundTag": []string{"loop-in"}, "domain": []string{"full:cool.test"}, "outboundTag": "loop-ss-mux"},
			map[string]any{"type": "field", "inboundTag": []string{"loop-in"}, "outboundTag": "loop-ss"},
			map[string]any{"type": "field", "inboundTag": []string{"loop-off-in"}, "outboundTag": "loop-ss-off"},
			map[string]any{"type": "field", "network": "udp", "outboundTag": "udp"},
			map[string]any{"type": "field", "user": []string{"mihomo", "singbox", "loop"}, "domain": []string{"full:user.test"}, "outboundTag": "b"},
			map[string]any{"type": "field", "domain": []string{"full:beta.test", "full:sniff.test"}, "outboundTag": "b"},
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
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", apiPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	f.api = handler.NewHandlerServiceClient(conn)
	f.stats = statscmd.NewStatsServiceClient(conn)
	return f
}

func (f *fixture) muxStats(t *testing.T, tag string) *handler.InboundMuxStats {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := f.api.GetInboundMuxStats(ctx, &handler.GetInboundMuxStatsRequest{Tag: tag})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Inbounds) != 1 || response.Inbounds[0].Tag != tag {
		t.Fatalf("unexpected mux stats for %s: %v", tag, response)
	}
	return response.Inbounds[0]
}

// waitIdle waits until every carrier and stream of earlier clients has ended,
// which also checks that a closed client releases its server state.
func (f *fixture) waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		s := f.muxStats(t, "ss-in").SingMux
		var carriers uint64
		for _, n := range s.ActiveCarriers {
			carriers += n
		}
		if carriers == 0 && s.ActiveTcpStreams == 0 && s.ActiveUdpStreams == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server state of closed clients was not released: %+v", s)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (f *fixture) userTraffic(t *testing.T, email string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var total int64
	for _, direction := range []string{"uplink", "downlink"} {
		result, err := f.stats.GetStats(ctx, &statscmd.GetStatsRequest{Name: "user>>>" + email + ">>>traffic>>>" + direction})
		if err == nil {
			total += result.Stat.Value
		}
	}
	return total
}

// exchange sends payload and returns the marker of the echo server that answered.
func exchange(conn net.Conn, payload string) (string, error) {
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		return "", err
	}
	got := make([]byte, 1+len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		return "", err
	}
	if string(got[1:]) != payload {
		return "", fmt.Errorf("echo mismatch: %q", got[1:])
	}
	return string(got[:1]), nil
}

// echoAgain checks an open stream whose marker was already read.
func echoAgain(conn net.Conn, payload string) error {
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		return err
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		return err
	}
	if string(got) != payload {
		return fmt.Errorf("echo mismatch: %q", got)
	}
	return nil
}

type route struct {
	target  string
	payload string
	marker  string
}

// routes checks domain routing, the per-stream user rule and per-stream sniffing.
var routes = []route{
	{"alpha.test:80", "alpha", "A"},
	{"beta.test:80", "beta", "B"},
	{"user.test:80", "user", "B"},
	{"127.0.0.2:80", sniffPayload, "B"},
}

// checkRoutes opens streams concurrently through dial and checks every route.
func checkRoutes(t *testing.T, dial func(target string) (net.Conn, error), rounds int) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, rounds*len(routes))
	for i := 0; i < rounds; i++ {
		for _, r := range routes {
			wg.Add(1)
			go func(r route) {
				defer wg.Done()
				conn, err := dial(r.target)
				if err != nil {
					errs <- fmt.Errorf("dial %s: %w", r.target, err)
					return
				}
				defer conn.Close()
				marker, err := exchange(conn, r.payload)
				if err != nil {
					errs <- fmt.Errorf("%s: %w", r.target, err)
				} else if marker != r.marker {
					errs <- fmt.Errorf("%s routed to %s, want %s", r.target, marker, r.marker)
				}
			}(r)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}
}

func checkUDP(t *testing.T, conn net.Conn) {
	t.Helper()
	for i := 0; i < 3; i++ {
		payload := fmt.Sprintf("datagram-%d", i)
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := conn.Write([]byte(payload)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, 128)
		n, err := conn.Read(got)
		if err != nil {
			t.Fatal("UDP echo:", err)
		}
		if string(got[:n]) != payload {
			t.Fatalf("UDP echo %q, want %q", got[:n], payload)
		}
	}
}

// socksDialer opens carriers through the instance's own SOCKS inbound and SS-2022 outbound.
type socksDialer struct {
	port int
}

func (d socksDialer) DialContext(_ context.Context, _ string, dest M.Socksaddr) (net.Conn, error) {
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", d.port), nil, proxy.Direct)
	if err != nil {
		return nil, err
	}
	return dialer.Dial("tcp", dest.String())
}

func (socksDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

func newLoopClient(t *testing.T, port int, protocol string, padding bool, maxConnections int) *sm.Client {
	t.Helper()
	client, err := sm.NewClient(sm.Options{Dialer: socksDialer{port}, Protocol: protocol, MaxConnections: maxConnections, MinStreams: 4, Padding: padding})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func streamDialer(client *sm.Client) func(string) (net.Conn, error) {
	return func(target string) (net.Conn, error) {
		return client.DialContext(context.Background(), "tcp", M.ParseSocksaddr(target))
	}
}

func TestInProcessClientThroughSS2022(t *testing.T) {
	f := newFixture(t)
	for _, protocol := range []string{"h2mux", "smux", "yamux"} {
		for _, padding := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/padding=%v", protocol, padding), func(t *testing.T) {
				f.waitIdle(t)
				before := f.muxStats(t, "ss-in").SingMux
				traffic := f.userTraffic(t, "loop")
				client := newLoopClient(t, f.loopPort, protocol, padding, 4)
				checkRoutes(t, streamDialer(client), 5)
				packets, err := client.DialContext(context.Background(), "udp", M.ParseSocksaddrHostPort("127.0.0.1", 9))
				if err != nil {
					t.Fatal(err)
				}
				defer packets.Close()
				checkUDP(t, packets)
				after := f.muxStats(t, "ss-in").SingMux
				carriers := after.TotalCarriers - before.TotalCarriers
				if carriers < 1 || carriers > 4 {
					t.Fatalf("%d carriers for %d streams, want 1-4", carriers, 5*len(routes)+1)
				}
				if streams := after.TotalStreams - before.TotalStreams; streams < uint64(5*len(routes)+1) {
					t.Fatalf("server saw %d streams", streams)
				}
				if after.ActiveCarriers[protocol] == 0 || after.ActiveUdpStreams != before.ActiveUdpStreams+1 {
					t.Fatalf("active state missing: %+v", after)
				}
				if padding && after.ActivePaddedCarriers == 0 {
					t.Fatalf("padded carrier not reported: %+v", after)
				}
				if f.userTraffic(t, "loop") <= traffic {
					t.Fatal("per-stream user traffic was not counted")
				}
			})
		}
	}
}

func TestUserRemovalRefusesNewStreams(t *testing.T) {
	f := newFixture(t)
	client := newLoopClient(t, f.loopPort, "h2mux", false, 1)
	dial := streamDialer(client)
	open, err := dial("alpha.test:80")
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	if _, err := exchange(open, "before"); err != nil {
		t.Fatal(err)
	}
	alter := func(op *serial.TypedMessage) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := f.api.AlterInbound(ctx, &handler.AlterInboundRequest{Tag: "ss-in", Operation: op}); err != nil {
			t.Fatal(err)
		}
	}
	alter(serial.ToTypedMessage(&handler.RemoveUserOperation{Email: "loop"}))
	refused, err := dial("beta.test:80")
	if err != nil {
		t.Fatal(err)
	}
	defer refused.Close()
	if _, err := exchange(refused, "after"); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("new stream after removal: %v, want a revoked refusal", err)
	}
	if err := echoAgain(open, "still-open"); err != nil {
		t.Fatal("open stream ended after removal:", err)
	}
	if got := f.muxStats(t, "ss-in").SingMux.Rejected["revoked"]; got != 1 {
		t.Fatalf("revoked refusals %d, want 1", got)
	}
	alter(serial.ToTypedMessage(&handler.AddUserOperation{User: &protocol.User{
		Email:   "loop",
		Account: serial.ToTypedMessage(&shadowsocks_2022.Account{Key: userKeys["loop"]}),
	}}))
	again, err := dial("alpha.test:80")
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if _, err := exchange(again, "restored"); err != nil {
		t.Fatal("stream after re-adding the user:", err)
	}
}

func TestDisabledInboundAndMuxCoolCoexist(t *testing.T) {
	f := newFixture(t)
	off := newLoopClient(t, f.loopOffPort, "h2mux", false, 1)
	conn, err := off.DialContext(context.Background(), "tcp", M.ParseSocksaddr("alpha.test:80"))
	if err == nil {
		_, err = exchange(conn, "refused")
		conn.Close()
	}
	// Without singMux the carrier destination is routed like any other (here to
	// echo A), so the client cannot open a session.
	if err == nil {
		t.Fatal("an inbound without singMux served a carrier")
	}
	stats := f.muxStats(t, "ss-off").SingMux
	if stats.Enabled || stats.TotalCarriers != 0 {
		t.Fatalf("unexpected ss-off state: %+v", stats)
	}
	// Xray's own Mux.Cool client reaches the same port as sing-mux clients.
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", f.loopPort), nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	cool, err := dialer.Dial("tcp", "cool.test:80")
	if err != nil {
		t.Fatal(err)
	}
	defer cool.Close()
	if marker, err := exchange(cool, "mux-cool"); err != nil || marker != "A" {
		t.Fatalf("Mux.Cool stream: marker %q, %v", marker, err)
	}
	if got := f.muxStats(t, "ss-in").MuxCool; got.TotalCarriers == 0 {
		t.Fatalf("Mux.Cool carrier not reported: %+v", got)
	}
}

func TestGetInboundMuxStatsContract(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	all, err := f.api.GetInboundMuxStats(ctx, &handler.GetInboundMuxStatsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, inbound := range all.Inbounds {
		tags = append(tags, inbound.Tag)
		if inbound.SingMux == nil || inbound.MuxCool == nil {
			t.Fatalf("incomplete stats for %s", inbound.Tag)
		}
		if enabled := inbound.Tag == "ss-in"; inbound.SingMux.Enabled != enabled {
			t.Fatalf("%s enabled=%v", inbound.Tag, inbound.SingMux.Enabled)
		}
		for _, name := range []string{"smux", "yamux", "h2mux"} {
			if _, ok := inbound.SingMux.ActiveCarriers[name]; !ok {
				t.Fatalf("%s lacks the %s gauge", inbound.Tag, name)
			}
		}
	}
	if strings.Join(tags, ",") != "loop-in,loop-off-in,ss-in,ss-off" {
		t.Fatalf("inbounds %v, want every inbound sorted by tag", tags)
	}
	if got := f.muxStats(t, "ss-in").SingMux.MaxStreams; got != 256 {
		t.Fatalf("default maxStreams %d, want 256", got)
	}
	if _, err := f.api.GetInboundMuxStats(ctx, &handler.GetInboundMuxStatsRequest{Tag: "missing"}); err == nil {
		t.Fatal("unknown tag answered")
	}
}

// countingRelay forwards TCP and UDP to the SS-2022 port and counts TCP connections.
type countingRelay struct {
	port     int
	accepted atomic.Int32
	mu       sync.Mutex
	conns    []net.Conn
}

// dropAll cuts every relayed TCP connection, as a network failure would.
func (r *countingRelay) dropAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, conn := range r.conns {
		conn.Close()
	}
	r.conns = nil
}

func newCountingRelay(t *testing.T, target int) *countingRelay {
	t.Helper()
	port := unusedTCPUDPPort(t)
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	packets, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close(); packets.Close() })
	r := &countingRelay{port: port}
	upstream := fmt.Sprintf("127.0.0.1:%d", target)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			r.accepted.Add(1)
			r.mu.Lock()
			r.conns = append(r.conns, conn)
			r.mu.Unlock()
			go func() {
				defer conn.Close()
				remote, err := net.Dial("tcp", upstream)
				if err != nil {
					return
				}
				defer remote.Close()
				go func() { io.Copy(remote, conn); remote.(*net.TCPConn).CloseWrite() }()
				io.Copy(conn, remote)
			}()
		}
	}()
	go func() {
		var mu sync.Mutex
		peers := map[string]net.Conn{}
		data := make([]byte, 65535)
		for {
			n, from, err := packets.ReadFrom(data)
			if err != nil {
				return
			}
			mu.Lock()
			remote := peers[from.String()]
			if remote == nil {
				if remote, err = net.Dial("udp", upstream); err != nil {
					mu.Unlock()
					continue
				}
				peers[from.String()] = remote
				t.Cleanup(func() { remote.Close() })
				go func(remote net.Conn, from net.Addr) {
					reply := make([]byte, 65535)
					for {
						n, err := remote.Read(reply)
						if err != nil {
							return
						}
						packets.WriteTo(reply[:n], from)
					}
				}(remote, from)
			}
			mu.Unlock()
			remote.Write(data[:n])
		}
	}()
	return r
}

// clientVersion runs only the version flag: mihomo without a config directory
// would create one under the user's home.
func clientVersion(binary, flag string) string {
	out, err := exec.Command(binary, flag).CombinedOutput()
	if err != nil {
		return "unknown: " + err.Error()
	}
	return strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
}

func externalBinaries(t *testing.T, env string) []string {
	t.Helper()
	value := os.Getenv(env)
	if value == "" {
		if os.Getenv("SINGMUX_STRICT") == "1" {
			t.Fatalf("SINGMUX_STRICT=1 requires %s", env)
		}
		t.Skipf("set %s to run independent client acceptance", env)
	}
	return filepath.SplitList(value)
}

func runClient(t *testing.T, binary string, args []string, dir string, readyPort int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, args...)
	logPath := filepath.Join(dir, "client.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		cmd.Wait()
		logFile.Close()
		if t.Failed() {
			if data, err := os.ReadFile(logPath); err == nil {
				t.Logf("%s log:\n%s", filepath.Base(binary), data)
			}
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", readyPort), time.Second)
		if err == nil {
			conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not open its SOCKS port", binary)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func socksDial(port int) func(string) (net.Conn, error) {
	return func(target string) (net.Conn, error) {
		dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", port), nil, proxy.Direct)
		if err != nil {
			return nil, err
		}
		return dialer.Dial("tcp", target)
	}
}

type clientCase struct {
	protocol string
	padding  bool
	onlyTCP  bool
}

func clientCases(withOnlyTCP bool) []clientCase {
	var cases []clientCase
	for _, protocol := range []string{"h2mux", "smux", "yamux"} {
		for _, padding := range []bool{false, true} {
			cases = append(cases, clientCase{protocol: protocol, padding: padding})
		}
	}
	if withOnlyTCP {
		// The subscription's proposed setting: TCP multiplexed, UDP on plain SS-2022.
		cases = append(cases, clientCase{protocol: "h2mux", padding: true, onlyTCP: true})
	}
	return cases
}

// checkExternal runs the route and UDP checks through a client process and the relay.
func checkExternal(t *testing.T, f *fixture, relay *countingRelay, email string, socksPort, udpPort int, c clientCase) {
	t.Helper()
	f.waitIdle(t)
	before := f.muxStats(t, "ss-in").SingMux
	traffic := f.userTraffic(t, email)
	checkRoutes(t, socksDial(socksPort), 5)
	udp, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", udpPort))
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	checkUDP(t, udp)
	after := f.muxStats(t, "ss-in").SingMux
	t.Logf("%d requests + UDP: %d TCP connections, %d carriers, %d streams", 5*len(routes), relay.accepted.Load(),
		after.TotalCarriers-before.TotalCarriers, after.TotalStreams-before.TotalStreams)
	if n := relay.accepted.Load(); n < 1 || n > 4 {
		t.Fatalf("%d TCP connections for %d requests, want 1-4", n, 5*len(routes))
	}
	if carriers := after.TotalCarriers - before.TotalCarriers; carriers < 1 || carriers > 4 {
		t.Fatalf("server counted %d carriers", carriers)
	}
	wantStreams := uint64(5 * len(routes))
	if !c.onlyTCP {
		wantStreams++
	}
	if streams := after.TotalStreams - before.TotalStreams; streams < wantStreams {
		t.Fatalf("server counted %d streams, want at least %d", streams, wantStreams)
	}
	if after.ActiveCarriers[c.protocol] == 0 {
		t.Fatalf("no active %s carrier: %+v", c.protocol, after)
	}
	if c.padding && after.ActivePaddedCarriers == 0 {
		t.Fatalf("padded carrier not reported: %+v", after)
	}
	if f.userTraffic(t, email) <= traffic {
		t.Fatalf("no user traffic counted for %s", email)
	}
}

func TestMihomoClients(t *testing.T) {
	binaries := externalBinaries(t, "SINGMUX_MIHOMO")
	f := newFixture(t)
	for i, binary := range binaries {
		t.Logf("mihomo %d: %s", i, clientVersion(binary, "-v"))
		for _, c := range clientCases(true) {
			t.Run(fmt.Sprintf("%d/%s/padding=%v/only-tcp=%v", i, c.protocol, c.padding, c.onlyTCP), func(t *testing.T) {
				relay := newCountingRelay(t, f.ssPort)
				socksPort, udpPort := unusedPort(t), unusedTCPUDPPort(t)
				dir := t.TempDir()
				config := map[string]any{
					"socks-port": socksPort, "bind-address": "127.0.0.1", "allow-lan": false, "mode": "rule", "log-level": "warning", "ipv6": false,
					"proxies": []any{map[string]any{
						"name": "proxy", "type": "ss", "server": "127.0.0.1", "port": relay.port, "cipher": ssMethod,
						"password": ssMaster + ":" + userKeys["mihomo"], "udp": true,
						"smux": map[string]any{"enabled": true, "protocol": c.protocol, "max-connections": 4, "min-streams": 4, "padding": c.padding, "only-tcp": c.onlyTCP},
					}},
					"tunnels": []any{map[string]any{"network": []string{"udp"}, "address": fmt.Sprintf("127.0.0.1:%d", udpPort), "target": "127.0.0.1:9", "proxy": "proxy"}},
					"rules":   []string{"MATCH,proxy"},
				}
				raw, _ := json.Marshal(config)
				path := filepath.Join(dir, "config.yaml")
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				runClient(t, binary, []string{"-d", dir, "-f", path}, dir, socksPort)
				checkExternal(t, f, relay, "mihomo", socksPort, udpPort, c)
			})
		}
	}
}

func TestSingBoxClients(t *testing.T) {
	binaries := externalBinaries(t, "SINGMUX_SINGBOX")
	f := newFixture(t)
	for i, binary := range binaries {
		t.Logf("sing-box %d: %s", i, clientVersion(binary, "version"))
		for _, c := range clientCases(false) {
			t.Run(fmt.Sprintf("%d/%s/padding=%v", i, c.protocol, c.padding), func(t *testing.T) {
				relay := newCountingRelay(t, f.ssPort)
				socksPort, udpPort := unusedPort(t), unusedTCPUDPPort(t)
				dir := t.TempDir()
				config := map[string]any{
					"log": map[string]any{"level": "warn"},
					"inbounds": []any{
						map[string]any{"type": "socks", "tag": "socks", "listen": "127.0.0.1", "listen_port": socksPort},
						map[string]any{"type": "direct", "tag": "udp", "listen": "127.0.0.1", "listen_port": udpPort, "network": "udp", "override_address": "127.0.0.1", "override_port": 9},
					},
					"outbounds": []any{map[string]any{
						"type": "shadowsocks", "tag": "ss", "server": "127.0.0.1", "server_port": relay.port, "method": ssMethod,
						"password":  ssMaster + ":" + userKeys["singbox"],
						"multiplex": map[string]any{"enabled": true, "protocol": c.protocol, "max_connections": 4, "min_streams": 4, "padding": c.padding},
					}},
					"route": map[string]any{"final": "ss"},
				}
				raw, _ := json.Marshal(config)
				path := filepath.Join(dir, "config.json")
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				runClient(t, binary, []string{"run", "-c", path}, dir, socksPort)
				checkExternal(t, f, relay, "singbox", socksPort, udpPort, c)
			})
		}
	}
}

func (f *fixture) waitStreams(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s := f.muxStats(t, "ss-in").SingMux
		if s.ActiveTcpStreams == 0 && s.ActiveUdpStreams == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("streams were not released: %+v", s)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func bulkExchange(dial func(string) (net.Conn, error), size int, seed byte) error {
	conn, err := dial("alpha.test:80")
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(60 * time.Second))
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = seed + byte(i*7)
	}
	go conn.Write(payload)
	got := make([]byte, 1+size)
	if _, err := io.ReadFull(conn, got); err != nil {
		return err
	}
	if got[0] != 'A' || string(got[1:]) != string(payload) {
		return fmt.Errorf("bulk echo corrupted")
	}
	return nil
}

// TestMihomoH2MuxStress runs the proposed subscription setting (h2mux, padding,
// only-tcp) against Core's Go 1.27 HTTP/2 server: many large concurrent streams,
// cancellation, carrier loss and the 30 s idle GOAWAY.
func TestMihomoH2MuxStress(t *testing.T) {
	binary := externalBinaries(t, "SINGMUX_MIHOMO")[0]
	t.Logf("mihomo: %s", clientVersion(binary, "-v"))
	f := newFixture(t)
	relay := newCountingRelay(t, f.ssPort)
	socksPort := unusedPort(t)
	dir := t.TempDir()
	config := map[string]any{
		"socks-port": socksPort, "bind-address": "127.0.0.1", "allow-lan": false, "mode": "rule", "log-level": "warning", "ipv6": false,
		"proxies": []any{map[string]any{
			"name": "proxy", "type": "ss", "server": "127.0.0.1", "port": relay.port, "cipher": ssMethod,
			"password": ssMaster + ":" + userKeys["mihomo"], "udp": true,
			"smux": map[string]any{"enabled": true, "protocol": "h2mux", "max-connections": 4, "min-streams": 4, "padding": true, "only-tcp": true},
		}},
		"rules": []string{"MATCH,proxy"},
	}
	raw, _ := json.Marshal(config)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	runClient(t, binary, []string{"-d", dir, "-f", path}, dir, socksPort)
	dial := socksDial(socksPort)

	const streams, size = 300, 256 << 10
	start := time.Now()
	var wg sync.WaitGroup
	var failed atomic.Int32
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := bulkExchange(dial, size, byte(i)); err != nil {
				if failed.Add(1) <= 3 {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()
	stats := f.muxStats(t, "ss-in").SingMux
	t.Logf("%d streams x %d KiB in %s over %d TCP connections (%d carriers)", streams, size>>10,
		time.Since(start).Round(time.Millisecond), relay.accepted.Load(), stats.TotalCarriers)
	if n := failed.Load(); n > 0 {
		t.Fatalf("%d of %d bulk streams failed", n, streams)
	}
	if n := relay.accepted.Load(); n > 4 {
		t.Fatalf("%d TCP connections, want at most 4", n)
	}

	// Cancellation: abandon streams mid-transfer; the server must release them.
	for i := 0; i < 50; i++ {
		conn, err := dial("beta.test:80")
		if err != nil {
			t.Fatal(err)
		}
		conn.Write(make([]byte, 64<<10))
		conn.Close()
	}
	f.waitStreams(t, 15*time.Second)

	// Carrier loss: every carrier is cut; later requests must open new ones.
	relay.dropAll()
	f.waitIdle(t)
	before := relay.accepted.Load()
	checkRoutes(t, dial, 5)
	if relay.accepted.Load() == before {
		t.Fatal("no new carrier after the old ones were cut")
	}

	// Idle: the h2mux server sends GOAWAY after 30 s without streams.
	idleStart := time.Now()
	f.waitStreams(t, 15*time.Second)
	deadline := time.Now().Add(60 * time.Second)
	for {
		var carriers uint64
		for _, n := range f.muxStats(t, "ss-in").SingMux.ActiveCarriers {
			carriers += n
		}
		if carriers == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle carriers still open after %s", time.Since(idleStart))
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("idle carriers closed after %s", time.Since(idleStart).Round(time.Second))
	checkRoutes(t, dial, 2)
}
