package command_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	handlercmd "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/core"
)

// Core spec026 FR-004–FR-008 (CP-7): GetConnections and CloseConnections on a started instance
// through a real gRPC server. Doors reach an echo server through client outbounds that dial this
// instance's own inbounds: SS-2022 (alice, bob, alice's UDP), VLESS with Mux.Cool (carol), Trojan
// (dave), Hy2 (erin) and a Hy2 relay (relayed rides owner's connection).

type connFixture struct {
	api   handlercmd.HandlerServiceClient
	doors map[string]int
	udp   int // the UDP door
}

func startConnFixture(t *testing.T) *connFixture {
	t.Helper()
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { echo.Close() })
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn)
			}()
		}
	}()
	udpEcho, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { udpEcho.Close() })
	go func() {
		b := make([]byte, 2048)
		for {
			n, addr, err := udpEcho.ReadFrom(b)
			if err != nil {
				return
			}
			udpEcho.WriteTo(b[:n], addr)
		}
	}()

	ca, cert, key := quicStatsPKI(t)
	serverTLS := func(alpn string) map[string]any {
		return map[string]any{"alpn": []string{alpn}, "certificates": []any{map[string]any{"certificate": cert, "key": key}}}
	}
	clientTLS := func(alpn string) map[string]any {
		return map[string]any{
			"alpn": []string{alpn}, "serverName": "quic-stats.test", "disableSystemRoot": true,
			"certificates": []any{map[string]any{"usage": "verify", "certificate": ca}},
		}
	}
	f := &connFixture{doors: map[string]int{}, udp: quicStatsUDPPort(t)}
	for _, d := range []string{"alice", "bob", "carol", "dave", "erin", "relayed"} {
		f.doors[d] = quicStatsPort(t)
	}
	apiPort, ssPort, vlessPort, trojanPort, hy2Port := quicStatsPort(t), quicStatsPort(t), quicStatsPort(t), quicStatsPort(t), quicStatsUDPPort(t)
	echoPort := echo.Addr().(*net.TCPAddr).Port
	const ssServer, ssAlice, ssBob = "MDEyMzQ1Njc4OWFiY2RlZg==", "ZmVkY2JhOTg3NjU0MzIxMA==", "YWJjZGVmZ2hpamtsbW5vcA=="
	const carolID = "6f0b8a2e-5d5b-4b39-9a4d-7f3c2a1e0b11"

	var inbounds, outbounds, rules []any
	door := func(name, out string) {
		inbounds = append(inbounds, map[string]any{
			"tag": "door-" + name, "protocol": "dokodemo-door", "listen": "127.0.0.1", "port": f.doors[name],
			"settings": map[string]any{"address": "127.0.0.1", "port": echoPort, "network": "tcp"},
		})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"door-" + name}, "outboundTag": out})
	}
	outbounds = append(outbounds, map[string]any{
		"tag": "direct", "protocol": "freedom", "sendThrough": "127.0.0.1",
		"settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}},
	})
	ssOut := func(tag, user string) map[string]any {
		return map[string]any{"tag": tag, "protocol": "shadowsocks", "settings": map[string]any{
			"address": "127.0.0.1", "port": ssPort, "method": "2022-blake3-aes-128-gcm", "password": ssServer + ":" + user,
		}}
	}
	hy2Out := func(tag string, settings map[string]any) map[string]any {
		return map[string]any{
			"tag": tag, "protocol": "hysteria", "sendThrough": "127.0.0.1",
			"settings": map[string]any{"version": 2, "address": "127.0.0.1", "port": hy2Port},
			"streamSettings": map[string]any{
				"network": "hysteria", "security": "tls", "tlsSettings": clientTLS("h3"), "hysteriaSettings": settings,
				"finalmask": map[string]any{"quicParams": map[string]any{"keepAlivePeriod": 10}},
			},
		}
	}
	inbounds = append(inbounds,
		map[string]any{
			"tag": "ss-in", "protocol": "shadowsocks", "listen": "127.0.0.1", "port": ssPort,
			"settings": map[string]any{"method": "2022-blake3-aes-128-gcm", "password": ssServer, "network": "tcp,udp", "clients": []any{
				map[string]any{"email": "alice@conns.test", "password": ssAlice},
				map[string]any{"email": "bob@conns.test", "password": ssBob},
			}},
		},
		map[string]any{
			"tag": "vless-in", "protocol": "vless", "listen": "127.0.0.1", "port": vlessPort,
			"settings": map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": carolID, "email": "carol@conns.test"}}},
		},
		map[string]any{
			"tag": "trojan-in", "protocol": "trojan", "listen": "127.0.0.1", "port": trojanPort,
			"settings":       map[string]any{"clients": []any{map[string]any{"password": "dave-secret", "email": "dave@conns.test"}}},
			"streamSettings": map[string]any{"security": "tls", "tlsSettings": serverTLS("http/1.1")},
		},
		map[string]any{
			"tag": "hy2-in", "protocol": "hysteria", "listen": "127.0.0.1", "port": hy2Port,
			"settings": map[string]any{"version": 2, "clients": []any{
				map[string]any{"auth": "erin-secret", "email": "erin@conns.test"},
				map[string]any{"auth": "owner-secret", "email": "owner@conns.test", "relay": []string{"relayed@conns.test"}},
				map[string]any{"auth": "relayed-secret", "email": "relayed@conns.test"},
			}},
			"streamSettings": map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": serverTLS("h3"), "hysteriaSettings": map[string]any{"version": 2}},
		},
		map[string]any{
			"tag": "door-udp", "protocol": "dokodemo-door", "listen": "127.0.0.1", "port": f.udp,
			"settings": map[string]any{"address": "127.0.0.1", "port": udpEcho.LocalAddr().(*net.UDPAddr).Port, "network": "udp"},
		},
	)
	rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"door-udp"}, "outboundTag": "ss-alice"})
	outbounds = append(outbounds,
		ssOut("ss-alice", ssAlice), ssOut("ss-bob", ssBob),
		map[string]any{
			"tag": "vless-carol", "protocol": "vless",
			"settings": map[string]any{"address": "127.0.0.1", "port": vlessPort, "id": carolID, "encryption": "none"},
			"mux":      map[string]any{"enabled": true, "concurrency": 8},
		},
		map[string]any{
			"tag": "trojan-dave", "protocol": "trojan",
			"settings":       map[string]any{"address": "127.0.0.1", "port": trojanPort, "password": "dave-secret"},
			"streamSettings": map[string]any{"security": "tls", "tlsSettings": clientTLS("http/1.1")},
		},
		hy2Out("hy2-erin", map[string]any{"version": 2, "auth": "erin-secret"}),
		hy2Out("owner", map[string]any{"version": 2, "auth": "owner-secret", "session": map[string]any{
			"minConnections": 1, "maxConnections": 1, "streamsPerConnection": 64, "scaleUpStreams": 32,
		}}),
		map[string]any{"tag": "relay", "protocol": "hysteria", "settings": map[string]any{
			"version": 2, "relay": map[string]any{"session": "owner", "auth": "relayed-secret"},
		}},
	)
	door("alice", "ss-alice")
	door("bob", "ss-bob")
	door("carol", "vless-carol")
	door("dave", "trojan-dave")
	door("erin", "hy2-erin")
	door("relayed", "relay")

	raw, err := json.Marshal(map[string]any{
		"log":       map[string]any{"loglevel": "error", "access": "none"},
		"api":       map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort), "services": []string{"HandlerService"}},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   map[string]any{"rules": rules},
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
	f.api = handlercmd.NewHandlerServiceClient(conn)
	return f
}

// hold opens a connection through a door and echoes once, so the request has dispatched end to end.
func (f *connFixture) hold(t *testing.T, door string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", f.doors[door]), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := echoOnce(conn); err != nil {
		t.Fatalf("echo through %s: %v", door, err)
	}
	return conn
}

func echoOnce(conn net.Conn) error {
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetDeadline(time.Time{})
	if _, err := conn.Write([]byte("ping")); err != nil {
		return err
	}
	got := make([]byte, 4)
	_, err := io.ReadFull(conn, got)
	return err
}

// ended says whether the far end closed conn within d.
func ended(conn net.Conn, d time.Duration) bool {
	conn.SetReadDeadline(time.Now().Add(d))
	defer conn.SetReadDeadline(time.Time{})
	_, err := conn.Read(make([]byte, 1))
	return err != nil && !strings.Contains(err.Error(), "timeout")
}

func (f *connFixture) report(t *testing.T) map[string]*handlercmd.InboundConnections {
	t.Helper()
	resp, err := f.api.GetConnections(context.Background(), &handlercmd.GetConnectionsRequest{UserRows: true})
	if err != nil {
		t.Fatal(err)
	}
	byTag := make(map[string]*handlercmd.InboundConnections)
	for _, in := range resp.Inbounds {
		byTag[in.Tag] = in
	}
	return byTag
}

func userCounts(in *handlercmd.InboundConnections, user string) *handlercmd.ConnectionCounts {
	for _, u := range in.GetUsers() {
		if u.User == user {
			return u.Counts
		}
	}
	return &handlercmd.ConnectionCounts{}
}

func (f *connFixture) close(t *testing.T, request *handlercmd.CloseConnectionsRequest) map[string]*handlercmd.InboundClosed {
	t.Helper()
	start := time.Now()
	resp, err := f.api.CloseConnections(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("CloseConnections took %s", d)
	}
	byTag := make(map[string]*handlercmd.InboundClosed)
	for _, in := range resp.Inbounds {
		byTag[in.Tag] = in
	}
	return byTag
}

// eventually polls the report until check holds.
func (f *connFixture) eventually(t *testing.T, what string, check func(map[string]*handlercmd.InboundConnections) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := f.report(t)
		if check(r) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %v", what, r)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestConnectionsContract(t *testing.T) {
	f := startConnFixture(t)
	alice := []net.Conn{f.hold(t, "alice"), f.hold(t, "alice")}
	bob := f.hold(t, "bob")
	carol := []net.Conn{f.hold(t, "carol"), f.hold(t, "carol")}
	dave := f.hold(t, "dave")
	erin := f.hold(t, "erin")
	relayed := f.hold(t, "relayed")
	udp, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", f.udp))
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	if err := echoOnce(udp); err != nil {
		t.Fatalf("UDP echo: %v", err)
	}

	// FR-004: every protocol's objects under their users, the doors unbound
	r := f.report(t)
	ss, vl, tr, hy := r["ss-in"], r["vless-in"], r["trojan-in"], r["hy2-in"]
	if ss == nil || vl == nil || tr == nil || hy == nil || r["door-alice"] == nil {
		t.Fatalf("inbounds %v", r)
	}
	if c := userCounts(ss, "alice@conns.test"); c.Tcp != 2 || c.UdpSessions != 1 {
		t.Errorf("alice %v", c)
	}
	if c := userCounts(ss, "bob@conns.test"); c.Tcp != 1 || ss.Counts.Tcp != 3 || ss.Counts.UdpSessions != 1 {
		t.Errorf("bob %v, ss-in %v", c, ss.Counts)
	}
	if c := userCounts(vl, "carol@conns.test"); c.Tcp != 1 || c.MuxCoolCarriers != 1 || vl.MuxCoolStreams != 2 {
		t.Errorf("carol's two streams on one Mux.Cool carrier: %v, streams %d", c, vl.MuxCoolStreams)
	}
	if c := userCounts(tr, "dave@conns.test"); c.Tcp != 1 {
		t.Errorf("dave %v", c)
	}
	if c := userCounts(hy, "erin@conns.test"); c.Hy2Connections != 1 || c.Hy2Streams != 1 {
		t.Errorf("erin %v", c)
	}
	if c := userCounts(hy, "owner@conns.test"); c.Hy2Connections != 1 || c.Hy2Streams != 0 {
		t.Errorf("owner %v", c)
	}
	if c := userCounts(hy, "relayed@conns.test"); c.Hy2Connections != 0 || c.Hy2Streams != 1 {
		t.Errorf("relayed %v", c)
	}
	if d := r["door-alice"]; d.Counts.Tcp != 2 || d.Unbound != 2 || len(d.Users) != 0 {
		t.Errorf("a door's connections are unbound: %v", d)
	}
	if strings.Contains(fmt.Sprint(r), "secret") || strings.Contains(fmt.Sprint(r), "MDEy") {
		t.Error("no secret in the report")
	}

	// FR-008: arguments and tags before anything closes
	for _, bad := range []*handlercmd.CloseConnectionsRequest{
		{},
		{Users: []string{"alice@conns.test"}, AllUsers: true, Tags: []string{"ss-in"}},
		{AllUsers: true},
	} {
		if _, err := f.api.CloseConnections(context.Background(), bad); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%v: want InvalidArgument, got %v", bad, err)
		}
	}
	if _, err := f.api.CloseConnections(context.Background(), &handlercmd.CloseConnectionsRequest{Users: []string{"alice@conns.test"}, Tags: []string{"ss-in", "missing"}}); status.Code(err) != codes.NotFound {
		t.Errorf("unknown tag: want NotFound, got %v", err)
	}
	if ended(alice[0], 300*time.Millisecond) {
		t.Fatal("a refused call closed alice")
	}

	// FR-005: alice's TCP and UDP on every inbound, case-insensitively; bob stays
	closed := f.close(t, &handlercmd.CloseConnectionsRequest{Users: []string{"ALICE@conns.test"}, Reason: "contract"})
	if c := closed["ss-in"].Closed; c.Tcp != 2 || c.UdpSessions != 1 || closed["ss-in"].Pending != 0 {
		t.Errorf("closed on ss-in %v", closed["ss-in"])
	}
	for _, c := range alice {
		if !ended(c, 3*time.Second) {
			t.Error("alice's connection still open")
		}
	}
	if err := echoOnce(bob); err != nil {
		t.Errorf("bob after alice's closure: %v", err)
	}
	if again := f.close(t, &handlercmd.CloseConnectionsRequest{Users: []string{"alice@conns.test"}}); again["ss-in"].Closed.Tcp != 0 {
		t.Errorf("closing is idempotent: %v", again["ss-in"])
	}

	// a Mux.Cool carrier closes with its streams
	closed = f.close(t, &handlercmd.CloseConnectionsRequest{Users: []string{"carol@conns.test"}, Tags: []string{"vless-in"}})
	if c := closed["vless-in"].Closed; c.Tcp != 1 || c.MuxCoolCarriers != 1 || len(closed) != 1 {
		t.Errorf("carol %v", closed)
	}
	for _, c := range carol {
		if !ended(c, 3*time.Second) {
			t.Error("a stream on carol's carrier still open")
		}
	}

	// FR-006: a relayed user loses its stream, the owner's connection stays; then the owner's closes
	closed = f.close(t, &handlercmd.CloseConnectionsRequest{Users: []string{"relayed@conns.test"}, Tags: []string{"hy2-in"}})
	if c := closed["hy2-in"].Closed; c.Hy2Streams != 1 || c.Hy2Connections != 0 {
		t.Errorf("relayed %v", c)
	}
	if !ended(relayed, 3*time.Second) {
		t.Error("the relayed stream still open")
	}
	f.eventually(t, "owner's connection stays", func(r map[string]*handlercmd.InboundConnections) bool {
		return userCounts(r["hy2-in"], "owner@conns.test").Hy2Connections == 1 && userCounts(r["hy2-in"], "relayed@conns.test").Hy2Streams == 0
	})
	if err := echoOnce(erin); err != nil {
		t.Errorf("erin after the relayed closure: %v", err)
	}
	relayedAgain := f.hold(t, "relayed")
	closed = f.close(t, &handlercmd.CloseConnectionsRequest{Users: []string{"owner@conns.test"}, Tags: []string{"hy2-in"}})
	if c := closed["hy2-in"].Closed; c.Hy2Connections != 1 {
		t.Errorf("owner %v", c)
	}
	if !ended(relayedAgain, 3*time.Second) {
		t.Error("a relayed stream outlived its owner's connection")
	}

	// all_users with an exclusion: dave and erin go, bob stays
	closed = f.close(t, &handlercmd.CloseConnectionsRequest{AllUsers: true, Tags: []string{"ss-in", "trojan-in", "hy2-in"}, ExcludeUsers: []string{"bob@conns.test"}})
	if closed["trojan-in"].Closed.Tcp != 1 || closed["hy2-in"].Closed.Hy2Connections < 1 || closed["ss-in"].Closed.Tcp != 0 {
		t.Errorf("all users but bob: %v", closed)
	}
	for name, c := range map[string]net.Conn{"dave": dave, "erin": erin} {
		if !ended(c, 3*time.Second) {
			t.Errorf("%s still open", name)
		}
	}
	if err := echoOnce(bob); err != nil {
		t.Errorf("bob was excluded: %v", err)
	}
	f.eventually(t, "only bob and the unbound doors left", func(r map[string]*handlercmd.InboundConnections) bool {
		ss, tr, hy := r["ss-in"], r["trojan-in"], r["hy2-in"]
		return ss.Counts.Tcp == 1 && userCounts(ss, "bob@conns.test").Tcp == 1 && tr.Counts.Tcp == 0 &&
			userCounts(hy, "erin@conns.test").Hy2Connections == 0 && userCounts(hy, "erin@conns.test").Hy2Streams == 0
	})

	// user_rows with users selects rows; tags select inbounds
	resp, err := f.api.GetConnections(context.Background(), &handlercmd.GetConnectionsRequest{Tags: []string{"ss-in"}, UserRows: true, Users: []string{"BOB@conns.test"}})
	if err != nil || len(resp.Inbounds) != 1 || len(resp.Inbounds[0].Users) != 1 || resp.Inbounds[0].Users[0].User != "bob@conns.test" {
		t.Errorf("one inbound, one row: %v %v", resp, err)
	}
	if _, err := f.api.GetConnections(context.Background(), &handlercmd.GetConnectionsRequest{Tags: []string{"missing"}}); status.Code(err) != codes.NotFound {
		t.Errorf("unknown tag: want NotFound, got %v", err)
	}
}

// A default configuration answers with zero counts and closes nothing.
func TestConnectionsContractDefault(t *testing.T) {
	apiPort, doorPort := quicStatsPort(t), quicStatsPort(t)
	raw, err := json.Marshal(map[string]any{
		"api":       map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort), "services": []string{"HandlerService"}},
		"inbounds":  []any{map[string]any{"tag": "door", "protocol": "dokodemo-door", "listen": "127.0.0.1", "port": doorPort, "settings": map[string]any{"address": "127.0.0.1", "port": 1}}},
		"outbounds": []any{map[string]any{"protocol": "freedom"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	x, err := core.StartInstance("json", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", apiPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	api := handlercmd.NewHandlerServiceClient(conn)
	resp, err := api.GetConnections(context.Background(), &handlercmd.GetConnectionsRequest{UserRows: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Inbounds) != 1 || resp.Inbounds[0].Tag != "door" || resp.Inbounds[0].Counts.Tcp != 0 || len(resp.Inbounds[0].Users) != 0 {
		t.Errorf("default report %v", resp)
	}
	closed, err := api.CloseConnections(context.Background(), &handlercmd.CloseConnectionsRequest{Users: []string{"nobody@conns.test"}})
	if err != nil || len(closed.Inbounds) != 1 || closed.Inbounds[0].Closed.Tcp != 0 {
		t.Errorf("default close %v %v", closed, err)
	}
}
