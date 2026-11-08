package hysteria_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	handlercmd "github.com/xtls/xray-core/app/proxyman/command"
	statscmd "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
)

// Core spec024 FR-003, FR-004: one owner connection carries its own user and two relayed users, each
// routed and counted as that user; a refused relay fails; a user removed at runtime is refused at once.

func relayFreePort(t *testing.T, network string) int {
	t.Helper()
	if network == "udp" {
		conn, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		return conn.LocalAddr().(*net.UDPAddr).Port
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func relayPKI(t *testing.T) (ca, cert, key []string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caCert := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "relay fixture CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caCert, caCert, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{"relay.test"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	lines := func(kind string, der []byte) []string {
		return strings.Split(strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))), "\n")
	}
	return lines("CERTIFICATE", caDER), lines("CERTIFICATE", leafDER), lines("PRIVATE KEY", keyDER)
}

// relayEcho answers every TCP connection with its mark, then echoes; on the same port, every UDP datagram
// comes back behind the mark.
func relayEcho(t *testing.T, mark string) int {
	t.Helper()
	var listener net.Listener
	var packets net.PacketConn
	for tries := 0; packets == nil; tries++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		if p, err := net.ListenPacket("udp", l.Addr().String()); err == nil {
			listener, packets = l, p
		} else if l.Close(); tries > 20 {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { listener.Close(); packets.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := packets.ReadFrom(buf)
			if err != nil {
				return
			}
			packets.WriteTo(append([]byte(mark), buf[:n]...), from)
		}
	}()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if _, err := conn.Write([]byte(mark)); err != nil {
					return
				}
				io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

type relayFixture struct {
	doors   map[string]int
	handler handlercmd.HandlerServiceClient
	stats   statscmd.StatsServiceClient
	client  handlercmd.HandlerServiceClient
	// clientStats reads the client's outbound traffic counters.
	clientStats statscmd.StatsServiceClient
}

var relayUsers = []string{"trunk", "a", "b", "c"}

func newRelayFixture(t *testing.T) *relayFixture {
	t.Helper()
	ca, cert, key := relayPKI(t)
	serverTLS := map[string]any{"alpn": []string{"h3"}, "certificates": []any{map[string]any{"certificate": cert, "key": key}}}
	clientTLS := map[string]any{
		"alpn": []string{"h3"}, "serverName": "relay.test", "disableSystemRoot": true,
		"certificates": []any{map[string]any{"usage": "verify", "certificate": ca}},
	}
	hy2Port := relayFreePort(t, "udp")
	serverAPI, clientAPI := relayFreePort(t, "tcp"), relayFreePort(t, "tcp")
	email := func(user string) string { return user + "@relay.test" }

	var clients, rules, outbounds []any
	for _, user := range relayUsers {
		client := map[string]any{"auth": "secret-" + user, "email": email(user)}
		if user == "trunk" {
			client["relay"] = []string{email("a"), email("b")}
		}
		clients = append(clients, client)
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"hy2-in"}, "user": []string{email(user)}, "outboundTag": "to-" + user})
		outbounds = append(outbounds, map[string]any{
			"tag": "to-" + user, "protocol": "freedom", "sendThrough": "127.0.0.1",
			"settings": map[string]any{
				"redirect":   fmt.Sprintf("127.0.0.1:%d", relayEcho(t, strings.ToUpper(user[:1]))),
				"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}},
			},
		})
	}
	server, err := json.Marshal(map[string]any{
		"log":    map[string]any{"loglevel": "error", "access": "none"},
		"stats":  map[string]any{},
		"api":    map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", serverAPI), "services": []string{"HandlerService", "StatsService"}},
		"policy": map[string]any{"levels": map[string]any{"0": map[string]any{"statsUserUplink": true, "statsUserDownlink": true}}},
		"inbounds": []any{map[string]any{
			"tag": "hy2-in", "protocol": "hysteria", "listen": "127.0.0.1", "port": hy2Port,
			"settings":       map[string]any{"version": 2, "clients": clients},
			"streamSettings": map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": serverTLS, "hysteriaSettings": map[string]any{"version": 2}},
		}},
		"outbounds": outbounds,
		"routing":   map[string]any{"rules": rules},
	})
	if err != nil {
		t.Fatal(err)
	}

	f := &relayFixture{doors: map[string]int{}}
	var doors, doorRules []any
	clientOutbounds := []any{map[string]any{
		"tag": "owner", "protocol": "hysteria", "sendThrough": "127.0.0.1",
		"settings": map[string]any{"version": 2, "address": "127.0.0.1", "port": hy2Port},
		"streamSettings": map[string]any{
			"network": "hysteria", "security": "tls", "tlsSettings": clientTLS,
			"hysteriaSettings": map[string]any{"version": 2, "auth": "secret-trunk", "session": map[string]any{
				"minConnections": 1, "maxConnections": 4, "streamsPerConnection": 256, "scaleUpStreams": 128,
			}},
			"finalmask": map[string]any{"quicParams": map[string]any{"keepAlivePeriod": 10}},
		},
	}}
	// relay-c is a client but not on trunk's list; relay-u is no client at all.
	for _, relay := range []struct{ tag, auth string }{
		{"owner", ""},
		{"relay-a", "secret-a"},
		{"relay-b", "secret-b"},
		{"relay-c", "secret-c"},
		{"relay-u", "secret-nobody"},
	} {
		f.doors[relay.tag] = relayFreePort(t, "tcp")
		f.doors["udp-"+relay.tag] = relayFreePort(t, "udp")
		doors = append(doors, map[string]any{
			"tag": "door-" + relay.tag, "protocol": "dokodemo-door", "listen": "127.0.0.1", "port": f.doors[relay.tag],
			"settings": map[string]any{"address": "127.0.0.1", "port": 9, "network": "tcp"},
		}, map[string]any{
			"tag": "udp-door-" + relay.tag, "protocol": "dokodemo-door", "listen": "127.0.0.1", "port": f.doors["udp-"+relay.tag],
			"settings": map[string]any{"address": "127.0.0.1", "port": 9, "network": "udp"},
		})
		doorRules = append(doorRules, map[string]any{
			"type": "field", "inboundTag": []string{"door-" + relay.tag, "udp-door-" + relay.tag},
			"outboundTag": relay.tag,
		})
		if relay.auth != "" {
			clientOutbounds = append(clientOutbounds, map[string]any{
				"tag": relay.tag, "protocol": "hysteria",
				"settings": map[string]any{"version": 2, "relay": map[string]any{"session": "owner", "auth": relay.auth}},
			})
		}
	}
	client, err := json.Marshal(map[string]any{
		"log":       map[string]any{"loglevel": "error", "access": "none"},
		"stats":     map[string]any{},
		"api":       map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", clientAPI), "services": []string{"HandlerService", "StatsService"}},
		"policy":    map[string]any{"system": map[string]any{"statsOutboundUplink": true, "statsOutboundDownlink": true}},
		"inbounds":  doors,
		"outbounds": clientOutbounds,
		"routing":   map[string]any{"rules": doorRules},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{server, client} {
		x, err := core.StartInstance("json", raw)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { x.Close() })
	}
	dial := func(port int) *grpc.ClientConn {
		conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	f.handler = handlercmd.NewHandlerServiceClient(dial(serverAPI))
	f.client = handlercmd.NewHandlerServiceClient(dial(clientAPI))
	f.stats = statscmd.NewStatsServiceClient(dial(serverAPI))
	f.clientStats = statscmd.NewStatsServiceClient(dial(clientAPI))
	return f
}

// traffic is a client outbound's uplink and downlink counters.
func (f *relayFixture) traffic(t *testing.T, tag string) (up, down int64) {
	t.Helper()
	for i, dir := range []string{"uplink", "downlink"} {
		resp, err := f.clientStats.GetStats(context.Background(), &statscmd.GetStatsRequest{Name: "outbound>>>" + tag + ">>>traffic>>>" + dir})
		if err != nil {
			t.Fatalf("%s %s: %v", tag, dir, err)
		}
		if i == 0 {
			up = resp.Stat.Value
		} else {
			down = resp.Stat.Value
		}
	}
	return up, down
}

// owner is the client's session owner entry.
func (f *relayFixture) owner(t *testing.T) *handlercmd.QuicHandlerStats {
	t.Helper()
	resp, err := f.client.GetQuicStats(context.Background(), &handlercmd.GetQuicStatsRequest{Tag: "owner"})
	if err != nil || len(resp.Handlers) != 1 {
		t.Fatalf("owner: %v, %v", resp, err)
	}
	return resp.Handlers[0]
}

// mark sends a message through a door and returns the echo server's mark, or an error.
func (f *relayFixture) mark(door string) (string, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", f.doors[door]), 3*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		return "", err
	}
	got := make([]byte, 5)
	if _, err := io.ReadFull(conn, got); err != nil {
		return "", err
	}
	if string(got[1:]) != "ping" {
		return "", fmt.Errorf("echo %q", got)
	}
	return string(got[:1]), nil
}

// datagram sends one datagram through a UDP door and returns the mark it comes back with.
func (f *relayFixture) datagram(door string) (string, error) {
	conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", f.doors["udp-"+door]))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	buf := make([]byte, 64)
	for try := 0; try < 3; try++ {
		if _, err := conn.Write([]byte("dgram")); err != nil {
			return "", err
		}
		conn.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
		n, err := conn.Read(buf)
		if err == nil {
			if string(buf[1:n]) != "dgram" {
				return "", fmt.Errorf("datagram %q", buf[:n])
			}
			return string(buf[:1]), nil
		}
	}
	return "", fmt.Errorf("no answer through %s", door)
}

// Core spec024 FR-008 (CP-7): GetQuicStats on the started instances through a real gRPC server.
func TestRelaySessionQuicStatsContract(t *testing.T) {
	f := newRelayFixture(t)
	if _, err := f.mark("relay-a"); err != nil {
		t.Fatal(err)
	}
	f.mark("relay-c")
	resp, err := f.client.GetQuicStats(context.Background(), &handlercmd.GetQuicStatsRequest{Connections: true})
	if err != nil {
		t.Fatal(err)
	}
	byTag := make(map[string]*handlercmd.QuicHandlerStats)
	for _, h := range resp.Handlers {
		byTag[h.Tag] = h
	}
	owner := byTag["owner"]
	if owner == nil || owner.Session == nil || owner.Session.Min != 1 || owner.Session.Max != 4 || owner.Session.Active != 1 {
		t.Fatalf("owner %v", owner)
	}
	if len(owner.Connections) != 1 || owner.Connections[0].State != "active" {
		t.Fatalf("owner connections %v", owner.Connections)
	}
	bindings := make(map[uint32]string)
	for _, b := range owner.Connections[0].Relay {
		bindings[b.Index] = b.Status + " " + b.User
	}
	want := map[uint32]string{1: "ok a@relay.test", 2: "ok b@relay.test", 3: "denied ", 4: "unknown "}
	for index, w := range want {
		if bindings[index] != w {
			t.Errorf("binding %d: %q, want %q", index, bindings[index], w)
		}
	}
	for tag, w := range map[string]string{"relay-a": "bound a@relay.test", "relay-b": "bound b@relay.test", "relay-c": "denied ", "relay-u": "unknown "} {
		h := byTag[tag]
		if h == nil || h.Relay == nil || h.Relay.Session != "owner" || h.Relay.Status+" "+h.Relay.User != w || h.Totals != nil {
			t.Errorf("%s: %v, want %q", tag, h, w)
		}
	}

	server, err := f.handler.GetQuicStats(context.Background(), &handlercmd.GetQuicStatsRequest{Connections: true})
	if err != nil {
		t.Fatal(err)
	}
	rows := server.Handlers[0].Connections
	if len(rows) != 1 || rows[0].User != "trunk@relay.test" || len(rows[0].Relay) != 2 ||
		rows[0].Relay[0].User != "a@relay.test" || rows[0].Relay[1].User != "b@relay.test" {
		t.Errorf("server rows %v", rows)
	}
	for _, r := range []*handlercmd.GetQuicStatsResponse{resp, server} {
		if raw, _ := json.Marshal(r); strings.Contains(string(raw), "secret-") {
			t.Errorf("a credential in the report: %s", raw)
		}
	}
}

// A relay's streams and UDP sessions count under the relay's own outbound tag, never under its owner's, although
// they run on the owner's connections.
func TestRelaySessionCountsTrafficUnderEachRelay(t *testing.T) {
	f := newRelayFixture(t)
	if got, err := f.mark("relay-a"); err != nil || got != "A" {
		t.Fatalf("relay-a: %q, %v", got, err)
	}
	if got, err := f.datagram("relay-b"); err != nil || got != "B" {
		t.Fatalf("relay-b: %q, %v", got, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		aUp, aDown := f.traffic(t, "relay-a")
		bUp, bDown := f.traffic(t, "relay-b")
		if aUp > 0 && aDown > 0 && bUp > 0 && bDown > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay counters: relay-a %d/%d, relay-b %d/%d", aUp, aDown, bUp, bDown)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if up, down := f.traffic(t, "owner"); up != 0 || down != 0 {
		t.Errorf("owner counted the relays' traffic: %d/%d", up, down)
	}
	if got, err := f.mark("owner"); err != nil || got != "T" {
		t.Fatalf("owner: %q, %v", got, err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for up, down := f.traffic(t, "owner"); up == 0 || down == 0; up, down = f.traffic(t, "owner") {
		if time.Now().After(deadline) {
			t.Fatalf("owner counters after its own stream: %d/%d", up, down)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRelaySessionCarriesUDPAsEachUser(t *testing.T) {
	f := newRelayFixture(t)
	for door, want := range map[string]string{"owner": "T", "relay-a": "A", "relay-b": "B"} {
		if got, err := f.datagram(door); err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", door, got, err, want)
		}
	}
	for _, door := range []string{"relay-c", "relay-u"} {
		if got, err := f.datagram(door); err == nil {
			t.Errorf("%s: a refused relay answered %q", door, got)
		}
	}
	if owner := f.owner(t); owner.ConnectionsTotal != 1 {
		t.Errorf("one connection for the UDP sessions of three users: %v", owner)
	}
}

func TestRelaySessionRoutesEachStreamAsItsUser(t *testing.T) {
	f := newRelayFixture(t)
	for door, want := range map[string]string{"owner": "T", "relay-a": "A", "relay-b": "B"} {
		for i := 0; i < 3; i++ {
			got, err := f.mark(door)
			if err != nil || got != want {
				t.Fatalf("%s: got %q, %v; want %q", door, got, err, want)
			}
		}
	}
	for _, door := range []string{"relay-c", "relay-u"} {
		if got, err := f.mark(door); err == nil {
			t.Errorf("%s: a refused relay went through to %q", door, got)
		}
	}
	// One QUIC connection carried all of it, on both ends.
	if owner := f.owner(t); owner.ConnectionsTotal != 1 || owner.ConnectionsActive != 1 {
		t.Errorf("client: want one connection, got %v", owner)
	}
	server, err := f.handler.GetQuicStats(context.Background(), &handlercmd.GetQuicStatsRequest{})
	if err != nil || len(server.Handlers) != 1 || server.Handlers[0].ConnectionsTotal != 1 {
		t.Errorf("server: want one connection, got %v, %v", server, err)
	}
	// Every stream released its count when it closed.
	deadline := time.Now().Add(5 * time.Second)
	for f.owner(t).Session.StreamsOpen != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("streams still counted open: %v", f.owner(t).Session)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, user := range []string{"a", "b", "trunk"} {
		resp, err := f.stats.GetStats(context.Background(), &statscmd.GetStatsRequest{Name: "user>>>" + user + "@relay.test>>>traffic>>>uplink"})
		if err != nil || resp.Stat.Value == 0 {
			t.Errorf("%s: uplink %v, %v", user, resp, err)
		}
	}

	// b removed at runtime: refused on the live connection, a unaffected.
	if _, err := f.handler.AlterInbound(context.Background(), &handlercmd.AlterInboundRequest{
		Tag: "hy2-in", Operation: serial.ToTypedMessage(&handlercmd.RemoveUserOperation{Email: "b@relay.test"}),
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := f.mark("relay-b"); err == nil {
		t.Errorf("a removed user still relayed to %q", got)
	}
	if got, err := f.mark("relay-a"); err != nil || got != "A" {
		t.Errorf("relay-a after removing b: %q, %v", got, err)
	}
}
