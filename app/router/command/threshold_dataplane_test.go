package command_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	. "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/core"
)

// Core spec027 T007 (N01-N08, R16): real connections through a real inbound and routing rule reach
// the outbound the published decision names. Every outbound is a freedom redirected to its own local
// server, which answers the burst probe (204 after the delay the test sets, or 503) and the data
// requests (its marker), over TCP and UDP on the same port.

type markerServer struct {
	name     string
	port     int
	delay    atomic.Int64
	fail     atomic.Bool
	requests atomic.Int64 // data requests, probes excluded
	tcp      net.Listener
	udp      net.PacketConn
}

func newMarkerServer(t *testing.T, name string, delay time.Duration) *markerServer {
	t.Helper()
	m := &markerServer{name: name}
	m.delay.Store(int64(delay))
	var err error
	for range 20 { // a TCP and a UDP socket on one port number
		if m.tcp, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		m.port = m.tcp.Addr().(*net.TCPAddr).Port
		if m.udp, err = net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", m.port)); err == nil {
			break
		}
		m.tcp.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/generate_204" {
			if m.fail.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			time.Sleep(time.Duration(m.delay.Load()))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		m.requests.Add(1)
		io.WriteString(w, m.name)
	})}
	go server.Serve(m.tcp)
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := m.udp.ReadFrom(buf)
			if err != nil {
				return
			}
			m.requests.Add(1)
			m.udp.WriteTo([]byte(m.name+":"+string(buf[:n])), addr)
		}
	}()
	t.Cleanup(func() { server.Close(); m.udp.Close() })
	return m
}

func (m *markerServer) set(delay time.Duration) { m.fail.Store(false); m.delay.Store(int64(delay)) }

type dataFixture struct {
	rpc                     *rpcFixture
	a, b, f, sentinel       *markerServer
	inPort, blackholeInPort int
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func newDataFixture(t *testing.T) *dataFixture {
	d := &dataFixture{
		sentinel: newMarkerServer(t, "default", 0),
		a:        newMarkerServer(t, "A", 20*time.Millisecond), b: newMarkerServer(t, "B", 30*time.Millisecond),
		f:      newMarkerServer(t, "F", 10*time.Millisecond),
		inPort: freePort(t), blackholeInPort: freePort(t),
	}
	apiPort := freePort(t)
	redirect := func(tag string, m *markerServer) map[string]any {
		return map[string]any{"tag": tag, "protocol": "freedom", "settings": map[string]any{"redirect": fmt.Sprintf("127.0.0.1:%d", m.port)}}
	}
	inbound := func(tag string, port int) map[string]any {
		return map[string]any{
			"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "dokodemo-door",
			"settings": map[string]any{"address": "data.test", "port": 80, "network": "tcp,udp"},
		}
	}
	raw, err := json.Marshal(map[string]any{
		"api":      map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort), "services": []string{"RoutingService"}},
		"inbounds": []any{inbound("in", d.inPort), inbound("in-blackhole", d.blackholeInPort)},
		// The first outbound is the default: a sentinel that must never see a request.
		"outbounds": []any{
			redirect("default", d.sentinel), redirect("a-hy2", d.a), redirect("a-vless", d.b), redirect("f-backup", d.f),
			map[string]any{"tag": "sink", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"balancers": []any{
				threshold("t", []string{"a-"}, "f-backup", "50ms", ""),
				threshold("u", []string{"a-"}, "sink", "50ms", ""),
			},
			"rules": []any{
				map[string]any{"type": "field", "inboundTag": []string{"in"}, "balancerTag": "t"},
				map[string]any{"type": "field", "inboundTag": []string{"in-blackhole"}, "balancerTag": "u"},
			},
		},
		"burstObservatory": map[string]any{"pingGroups": []any{map[string]any{
			"subjectSelector": []string{"a-", "f-"},
			"pingConfig":      map[string]any{"destination": "http://probe.test/generate_204", "interval": "1s", "sampling": 1, "timeout": "2s"},
		}}},
	})
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
	d.rpc = &rpcFixture{routing: NewRoutingServiceClient(conn)}
	return d
}

func (d *dataFixture) waitChoice(t *testing.T, mode, choice string) {
	t.Helper()
	d.rpc.waitFor(t, "t", mode+" "+choice, func(m *BalancerMsg) bool {
		return m.ThresholdDecision.Mode == mode && m.ThresholdDecision.ChoiceTag == choice
	})
}

// tcpClient is one TCP connection through an inbound, carrying keep-alive HTTP requests.
type tcpClient struct {
	conn   net.Conn
	reader *bufio.Reader
}

func dialInbound(t *testing.T, port int) *tcpClient {
	t.Helper()
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &tcpClient{conn: conn, reader: bufio.NewReader(conn)}
}

// get sends one request and returns the marker, or "" when the connection ends without a response.
func (c *tcpClient) get(t *testing.T) string {
	t.Helper()
	c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c.conn, "GET /marker HTTP/1.1\r\nHost: data.test\r\n\r\n"); err != nil {
		return ""
	}
	resp, err := http.ReadResponse(c.reader, nil)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(body)
}

func (d *dataFixture) marker(t *testing.T) string {
	t.Helper()
	return dialInbound(t, d.inPort).get(t)
}

type udpClient struct{ conn net.Conn }

func dialUDP(t *testing.T, port int) *udpClient {
	t.Helper()
	conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &udpClient{conn: conn}
}

func (c *udpClient) echo(t *testing.T, payload string) string {
	t.Helper()
	c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1500)
	n, err := c.conn.Read(buf)
	if err != nil {
		return ""
	}
	return string(buf[:n])
}

// dropRecorder captures what the dispatcher logs about dropped connections (D14).
type dropRecorder struct {
	mu       sync.Mutex
	info     int
	warnings []string
}

func (r *dropRecorder) Severity() log.Severity { return log.Severity_Debug }

func (r *dropRecorder) Handle(msg log.Message) {
	m, ok := msg.(*log.GeneralMessage)
	if !ok || !strings.Contains(fmt.Sprint(m.Content), "route dropped for") {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if m.Severity == log.Severity_Info {
		r.info++
	} else {
		r.warnings = append(r.warnings, fmt.Sprint(m.Content))
	}
}

func TestThresholdLeastPingDataPlane(t *testing.T) {
	d := newDataFixture(t)
	// N01: A 20, B 30, F 10.
	d.waitChoice(t, "primary", "a-hy2")
	if got := d.marker(t); got != "A" {
		t.Fatalf("N01: %q", got)
	}
	// N08: a TCP connection and a UDP association that started on A.
	flow := dialInbound(t, d.inPort)
	if got := flow.get(t); got != "A" {
		t.Fatalf("N08 TCP start: %q", got)
	}
	association := dialUDP(t, d.inPort)
	if got := association.echo(t, "1"); got != "A:1" {
		t.Fatalf("N08 UDP start: %q", got)
	}
	// N02: A over the limit, B under it.
	d.a.set(80 * time.Millisecond)
	d.waitChoice(t, "primary", "a-vless")
	if got := d.marker(t); got != "B" {
		t.Fatalf("N02: %q", got)
	}
	// N08: both existing flows still reach A on their original connection; new ones go to B.
	if got := flow.get(t); got != "A" {
		t.Fatalf("N08 TCP after the change: %q", got)
	}
	if got := association.echo(t, "2"); got != "A:2" {
		t.Fatalf("N08 UDP after the change: %q", got)
	}
	if got := dialUDP(t, d.inPort).echo(t, "3"); got != "B:3" {
		t.Fatalf("N08 new UDP association: %q", got)
	}
	// N03: both over, the fallback under.
	d.b.set(90 * time.Millisecond)
	d.f.set(10 * time.Millisecond)
	d.waitChoice(t, "fallback", "f-backup")
	if got := d.marker(t); got != "F" {
		t.Fatalf("N03: %q", got)
	}
	// N04: everything over the limit: the fastest usable, B.
	d.b.set(70 * time.Millisecond)
	d.f.set(120 * time.Millisecond)
	d.waitChoice(t, "degraded", "a-vless")
	if got := d.marker(t); got != "B" {
		t.Fatalf("N04: %q", got)
	}
	// N05, R16: nothing usable and no blackhole: connections drop; neither the default outbound nor the
	// failed fallback sees them; each is counted and logged at info, never as a warning.
	recorder := &dropRecorder{}
	log.RegisterHandler(recorder)
	for _, m := range []*markerServer{d.a, d.b, d.f} {
		m.fail.Store(true)
	}
	d.rpc.waitFor(t, "t", "unavailable", func(m *BalancerMsg) bool { return m.ThresholdDecision.Terminal == "drop" })
	before := d.rpc.info(t, "t").ThresholdDecision.Dropped
	sentinel, fallback := d.sentinel.requests.Load(), d.f.requests.Load()
	for range 3 {
		if got := d.marker(t); got != "" {
			t.Fatalf("N05: a dropped connection answered %q", got)
		}
	}
	if d.sentinel.requests.Load() != sentinel || d.f.requests.Load() != fallback {
		t.Fatal("N05: the default outbound or the failed fallback received a request")
	}
	msg := d.rpc.info(t, "t")
	if got := msg.ThresholdDecision.Dropped - before; got != 3 || msg.ThresholdDecision.LastDropReason == "" {
		t.Fatalf("R16: dropped %d (last %q)", got, msg.ThresholdDecision.LastDropReason)
	}
	recorder.mu.Lock()
	info, warnings := recorder.info, recorder.warnings
	recorder.mu.Unlock()
	if info < 3 || len(warnings) != 0 {
		t.Fatalf("R16 logs: %d info, warnings %v", info, warnings)
	}
	// N06: with an explicit blackhole the connection ends there.
	d.rpc.waitFor(t, "u", "blackhole", func(m *BalancerMsg) bool { return m.ThresholdDecision.Terminal == "blackhole" })
	if got := dialInbound(t, d.blackholeInPort).get(t); got != "" || d.sentinel.requests.Load() != sentinel {
		t.Fatalf("N06: %q, sentinel %d", got, d.sentinel.requests.Load()-sentinel)
	}
	// N07: A recovers; the next request returns to it.
	d.a.set(20 * time.Millisecond)
	d.waitChoice(t, "primary", "a-hy2")
	if got := d.marker(t); got != "A" {
		t.Fatalf("N07: %q", got)
	}
	if d.sentinel.requests.Load() != 0 {
		t.Fatalf("the default outbound received %d requests", d.sentinel.requests.Load())
	}
}
