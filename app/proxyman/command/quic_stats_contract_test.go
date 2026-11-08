package command_test

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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/xtls/xray-core/app/proxyman"
	handlercmd "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/hysteria"
)

// Core spec021 FR-007 (CP-7): GetQuicStats on a started instance through a real gRPC server.
// door-tuned reaches an echo server through hy2-tuned (copies, a 2 ms ACK delay) and door-plain
// through hy2-plain (defaults), both into hy2-in.

func quicStatsPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func quicStatsUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

// quicStatsPKI returns a CA and a leaf for quic-stats.test, as PEM lines.
func quicStatsPKI(t *testing.T) (ca, cert, key []string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caCert := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "quic-stats fixture CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caCert, caCert, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{"quic-stats.test"},
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

type quicStatsFixture struct {
	api           handlercmd.HandlerServiceClient
	doors         map[string]int // door tag -> port
	hy2Port       int
	echoListener  net.Listener
	tunedEmail    string
	inboundConfig *core.InboundHandlerConfig
}

func newQuicStatsFixture(t *testing.T) *quicStatsFixture {
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

	ca, cert, key := quicStatsPKI(t)
	serverTLS := map[string]any{"alpn": []string{"h3"}, "certificates": []any{map[string]any{"certificate": cert, "key": key}}}
	clientTLS := map[string]any{
		"alpn": []string{"h3"}, "serverName": "quic-stats.test", "disableSystemRoot": true,
		"certificates": []any{map[string]any{"usage": "verify", "certificate": ca}},
	}
	f := &quicStatsFixture{
		doors:   map[string]int{"door-tuned": quicStatsPort(t), "door-plain": quicStatsPort(t)},
		hy2Port: quicStatsUDPPort(t), echoListener: echo, tunedEmail: "tuned@quic-stats.test",
	}
	apiPort := quicStatsPort(t)
	echoPort := echo.Addr().(*net.TCPAddr).Port
	hy2Out := func(tag, auth string, quicParams map[string]any) map[string]any {
		stream := map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": clientTLS, "hysteriaSettings": map[string]any{"version": 2, "auth": auth}}
		if quicParams != nil {
			stream["finalmask"] = map[string]any{"quicParams": quicParams}
		}
		return map[string]any{
			"tag": tag, "protocol": "hysteria", "sendThrough": "127.0.0.1",
			"settings":       map[string]any{"version": 2, "address": "127.0.0.1", "port": f.hy2Port},
			"streamSettings": stream,
		}
	}
	door := func(tag string) map[string]any {
		return map[string]any{
			"tag": tag, "protocol": "dokodemo-door", "listen": "127.0.0.1", "port": f.doors[tag],
			"settings": map[string]any{"address": "127.0.0.1", "port": echoPort, "network": "tcp"},
		}
	}
	raw, err := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "error", "access": "none"},
		"api": map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort), "services": []string{"HandlerService"}},
		"inbounds": []any{
			door("door-tuned"), door("door-plain"),
			map[string]any{
				"tag": "hy2-in", "protocol": "hysteria", "listen": "127.0.0.1", "port": f.hy2Port,
				"settings": map[string]any{"version": 2, "clients": []any{
					map[string]any{"auth": "tuned-secret", "email": f.tunedEmail},
					map[string]any{"auth": "plain-secret", "email": "plain@quic-stats.test"},
				}},
				"streamSettings": map[string]any{
					"network": "hysteria", "security": "tls", "tlsSettings": serverTLS, "hysteriaSettings": map[string]any{"version": 2},
					"finalmask": map[string]any{"quicParams": map[string]any{"maxAckDelayMs": 2}},
				},
			},
		},
		"outbounds": []any{
			map[string]any{
				"tag": "direct", "protocol": "freedom", "sendThrough": "127.0.0.1",
				"settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}},
			},
			hy2Out("hy2-tuned", "tuned-secret", map[string]any{
				"maxAckDelayMs": 2, "disableChromeParrot": true, "ptoBackoffLimit": 1, "duplicateMaxSize": 300, "duplicateTail": true,
			}),
			hy2Out("hy2-plain", "plain-secret", nil),
		},
		"routing": map[string]any{"rules": []any{
			map[string]any{"type": "field", "inboundTag": []string{"door-tuned"}, "outboundTag": "hy2-tuned"},
			map[string]any{"type": "field", "inboundTag": []string{"door-plain"}, "outboundTag": "hy2-plain"},
		}},
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

// echo sends a message through a door and reads it back.
func (f *quicStatsFixture) echo(t *testing.T, door string, size int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", f.doors[door]), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	msg := []byte(strings.Repeat("q", size))
	if _, err := conn.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, size)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("echo through %s: %v", door, err)
	}
}

func (f *quicStatsFixture) stats(t *testing.T, tag string, connections bool) map[string]*handlercmd.QuicHandlerStats {
	t.Helper()
	resp, err := f.api.GetQuicStats(context.Background(), &handlercmd.GetQuicStatsRequest{Tag: tag, Connections: connections})
	if err != nil {
		t.Fatal(err)
	}
	byTag := make(map[string]*handlercmd.QuicHandlerStats)
	for _, h := range resp.Handlers {
		byTag[h.Direction+"/"+h.Tag] = h
	}
	return byTag
}

func TestQuicStatsContract(t *testing.T) {
	f := newQuicStatsFixture(t)
	for i := range 5 {
		f.echo(t, "door-tuned", 100+i)
		f.echo(t, "door-plain", 100+i)
	}
	f.echo(t, "door-tuned", 200000)

	all := f.stats(t, "", true)
	var keys []string
	for k := range all {
		keys = append(keys, k)
	}
	if len(all) != 3 || all["inbound/hy2-in"] == nil || all["outbound/hy2-tuned"] == nil || all["outbound/hy2-plain"] == nil {
		t.Fatalf("the Hy2 handlers only, got %v", keys)
	}

	// effective values: configured, and quic-go's defaults where unset
	tuned, plain, in := all["outbound/hy2-tuned"], all["outbound/hy2-plain"], all["inbound/hy2-in"]
	if e := tuned.Effective; e.MaxAckDelayMs != 2 || e.PtoBackoffLimit != 1 || e.DuplicateMaxSize != 300 || !e.DuplicateTail ||
		e.AckElicitingThreshold != 2 || e.InitialRttMs != 100 || e.LossTimeThreshold != 1.125 || e.LossPacketThreshold != 3 {
		t.Errorf("hy2-tuned effective %v", e)
	}
	if e := plain.Effective; e.MaxAckDelayMs != 25 || e.PtoBackoffLimit != 0 || e.DuplicateMaxSize != 0 || e.DuplicateTail {
		t.Errorf("hy2-plain effective %v", e)
	}
	if in.Effective.MaxAckDelayMs != 2 || in.Transport != "hysteria" {
		t.Errorf("hy2-in %v", in)
	}

	t.Logf("hy2-tuned %v; hy2-plain %v; hy2-in %v", tuned.Totals, plain.Totals, in.Totals)
	// counters: the tuned client copies, the listener drops the copies, the plain client copies nothing
	if c := tuned.Totals; c.PacketsSent == 0 || c.BytesSent == 0 || c.PacketsReceived == 0 || c.CopiesSent == 0 || c.CopyBytesSent == 0 {
		t.Errorf("hy2-tuned totals %v", c)
	}
	if c := plain.Totals; c.PacketsSent == 0 || c.CopiesSent != 0 {
		t.Errorf("hy2-plain totals %v", c)
	}
	if in.Totals.DuplicatesReceived == 0 {
		t.Errorf("hy2-in drops the tuned client's copies: %v", in.Totals)
	}
	for _, h := range []*handlercmd.QuicHandlerStats{tuned, plain} {
		if h.ConnectionsActive != 1 || h.ConnectionsTotal != 1 || len(h.Connections) != 1 || h.Truncated {
			t.Errorf("%s: one live connection, got %d active, %d total, %d rows", h.Tag, h.ConnectionsActive, h.ConnectionsTotal, len(h.Connections))
		}
	}
	users := map[string]bool{}
	for _, c := range in.Connections {
		users[c.User] = true
		if c.Remote == "" || c.SmoothedRttUs <= 0 {
			t.Errorf("row %v", c)
		}
	}
	if in.ConnectionsActive != 2 || !users[f.tunedEmail] || !users["plain@quic-stats.test"] {
		t.Errorf("hy2-in rows carry the users by email: %v", in.Connections)
	}
	if strings.Contains(fmt.Sprint(in), "tuned-secret") {
		t.Error("no auth secret in the report")
	}

	// a tag selects one handler; rows only on request
	one := f.stats(t, "hy2-tuned", false)
	if len(one) != 1 || one["outbound/hy2-tuned"] == nil || one["outbound/hy2-tuned"].Connections != nil {
		t.Errorf("one handler without rows, got %v", one)
	}
	for _, tag := range []string{"missing", "door-tuned"} {
		_, err := f.api.GetQuicStats(context.Background(), &handlercmd.GetQuicStatsRequest{Tag: tag})
		if status.Code(err) != codes.NotFound {
			t.Errorf("%s: want NotFound, got %v", tag, err)
		}
	}

	// replace the outbound's connection: restart the inbound, which closes it, and dial again
	before := tuned.Totals
	list, err := f.api.ListInbounds(context.Background(), &handlercmd.ListInboundsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var hy2In *core.InboundHandlerConfig
	for _, h := range list.Inbounds {
		if h.Tag == "hy2-in" {
			hy2In = h
		}
	}
	if hy2In == nil {
		t.Fatal("hy2-in not listed")
	}
	if _, err := f.api.RemoveInbound(context.Background(), &handlercmd.RemoveInboundRequest{Tag: "hy2-in"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.AddInbound(context.Background(), &handlercmd.AddInboundRequest{Inbound: hy2In}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if f.stats(t, "hy2-tuned", false)["outbound/hy2-tuned"].ConnectionsActive == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the outbound's connection did not close with the inbound")
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.echo(t, "door-tuned", 100)
	after := f.stats(t, "hy2-tuned", false)["outbound/hy2-tuned"]
	t.Logf("hy2-tuned after replacement: %d total, %d active, %v", after.ConnectionsTotal, after.ConnectionsActive, after.Totals)
	if after.ConnectionsTotal != 2 || after.ConnectionsActive != 1 {
		t.Errorf("a replaced connection: %d total, %d active", after.ConnectionsTotal, after.ConnectionsActive)
	}
	a, b := after.Totals, before
	if a.PacketsSent <= b.PacketsSent || a.BytesSent < b.BytesSent || a.PacketsReceived < b.PacketsReceived || a.BytesReceived < b.BytesReceived ||
		a.PacketsLost < b.PacketsLost || a.PtoCount < b.PtoCount || a.CopiesSent < b.CopiesSent || a.CopyBytesSent < b.CopyBytesSent ||
		a.DuplicatesReceived < b.DuplicatesReceived || a.SpuriousLosses < b.SpuriousLosses || a.CopiesSkipped < b.CopiesSkipped {
		t.Errorf("totals stay monotonic across replacement: before %v, after %v", b, a)
	}
	if restarted := f.stats(t, "hy2-in", false)["inbound/hy2-in"]; restarted.ConnectionsTotal != 1 {
		t.Errorf("a restarted inbound starts its own totals: %v", restarted)
	}
}

// Core spec025 FR-007 (CP-7, R4), FR-002: effective.padding on a started instance through a real gRPC server.
// hy2-in and hy2-plain are default; hy2-quiet-in, hy2-quiet and the session owner set disablePadding, and relay
// rides the owner. door-quiet and door-owner reach an echo with the field at both ends.
func TestQuicStatsPaddingContract(t *testing.T) {
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
	ca, cert, key := quicStatsPKI(t)
	serverTLS := map[string]any{"alpn": []string{"h3"}, "certificates": []any{map[string]any{"certificate": cert, "key": key}}}
	clientTLS := map[string]any{
		"alpn": []string{"h3"}, "serverName": "quic-stats.test", "disableSystemRoot": true,
		"certificates": []any{map[string]any{"usage": "verify", "certificate": ca}},
	}
	apiPort, plainPort, quietPort := quicStatsPort(t), quicStatsUDPPort(t), quicStatsUDPPort(t)
	doors := map[string]int{"door-quiet": quicStatsPort(t), "door-owner": quicStatsPort(t)}
	hy2In := func(tag string, port int, settings map[string]any, clients ...map[string]any) map[string]any {
		return map[string]any{
			"tag": tag, "protocol": "hysteria", "listen": "127.0.0.1", "port": port,
			"settings":       map[string]any{"version": 2, "clients": clients},
			"streamSettings": map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": serverTLS, "hysteriaSettings": settings},
		}
	}
	hy2Out := func(tag string, port int, settings map[string]any) map[string]any {
		return map[string]any{
			"tag": tag, "protocol": "hysteria", "sendThrough": "127.0.0.1",
			"settings": map[string]any{"version": 2, "address": "127.0.0.1", "port": port},
			"streamSettings": map[string]any{
				"network": "hysteria", "security": "tls", "tlsSettings": clientTLS, "hysteriaSettings": settings,
				"finalmask": map[string]any{"quicParams": map[string]any{"keepAlivePeriod": 10}},
			},
		}
	}
	door := func(tag string) map[string]any {
		return map[string]any{
			"tag": tag, "protocol": "dokodemo-door", "listen": "127.0.0.1", "port": doors[tag],
			"settings": map[string]any{"address": "127.0.0.1", "port": echo.Addr().(*net.TCPAddr).Port, "network": "tcp"},
		}
	}
	raw, err := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "error", "access": "none"},
		"api": map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort), "services": []string{"HandlerService"}},
		"inbounds": []any{
			door("door-quiet"), door("door-owner"),
			hy2In("hy2-in", plainPort, map[string]any{"version": 2}, map[string]any{"auth": "plain-secret", "email": "plain@quic-stats.test"}),
			hy2In("hy2-quiet-in", quietPort, map[string]any{"version": 2, "disablePadding": true},
				map[string]any{"auth": "quiet-secret", "email": "quiet@quic-stats.test"},
				map[string]any{"auth": "owner-secret", "email": "owner@quic-stats.test", "relay": []string{"relay@quic-stats.test"}},
				map[string]any{"auth": "relay-secret", "email": "relay@quic-stats.test"}),
		},
		"outbounds": []any{
			map[string]any{
				"tag": "direct", "protocol": "freedom", "sendThrough": "127.0.0.1",
				"settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}},
			},
			hy2Out("hy2-plain", plainPort, map[string]any{"version": 2, "auth": "plain-secret"}),
			hy2Out("hy2-quiet", quietPort, map[string]any{"version": 2, "auth": "quiet-secret", "disablePadding": true}),
			hy2Out("owner", quietPort, map[string]any{"version": 2, "auth": "owner-secret", "disablePadding": true, "session": map[string]any{
				"minConnections": 1, "maxConnections": 2, "streamsPerConnection": 64, "scaleUpStreams": 32,
			}}),
			map[string]any{"tag": "relay", "protocol": "hysteria", "settings": map[string]any{
				"version": 2, "relay": map[string]any{"session": "owner", "auth": "relay-secret"},
			}},
		},
		"routing": map[string]any{"rules": []any{
			map[string]any{"type": "field", "inboundTag": []string{"door-quiet"}, "outboundTag": "hy2-quiet"},
			map[string]any{"type": "field", "inboundTag": []string{"door-owner"}, "outboundTag": "relay"},
		}},
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
	f := &quicStatsFixture{api: handlercmd.NewHandlerServiceClient(conn), doors: doors}
	f.echo(t, "door-quiet", 100)
	f.echo(t, "door-owner", 100) // the relay's stream, on the owner's connection

	all := f.stats(t, "", false)
	for key, want := range map[string]string{
		"inbound/hy2-in": "random", "outbound/hy2-plain": "random",
		"inbound/hy2-quiet-in": "off", "outbound/hy2-quiet": "off", "outbound/owner": "off",
	} {
		h := all[key]
		if h == nil || h.Effective == nil || h.Effective.Padding != want {
			t.Errorf("%s: effective %v, want padding %s", key, h.GetEffective(), want)
		}
	}
	if relay := all["outbound/relay"]; relay == nil || relay.Effective != nil || relay.Relay.GetSession() != "owner" || relay.Relay.GetStatus() != "bound" {
		t.Errorf("relay: %v; a relay outbound has no effective, its owner's applies", relay)
	}
	if len(all) != 6 {
		t.Errorf("%d handlers: %v", len(all), all)
	}
	one := f.stats(t, "hy2-quiet", false)
	if len(one) != 1 || one["outbound/hy2-quiet"].GetEffective().GetPadding() != "off" {
		t.Errorf("one tag: %v", one)
	}
	if _, err := f.api.GetQuicStats(context.Background(), &handlercmd.GetQuicStatsRequest{Tag: "missing"}); status.Code(err) != codes.NotFound {
		t.Errorf("missing: want NotFound, got %v", err)
	}

	// FR-002, CP-2: the field comes back with the stream settings through ListInbounds and ListOutbounds.
	listed := map[string]bool{}
	inbounds, err := f.api.ListInbounds(context.Background(), &handlercmd.ListInboundsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range inbounds.Inbounds {
		receiver, err := h.ReceiverSettings.GetInstance()
		if err != nil {
			t.Fatal(err)
		}
		if ss := receiver.(*proxyman.ReceiverConfig).StreamSettings; ss != nil {
			listed["inbound/"+h.Tag] = hysteriaDisablePadding(t, ss)
		}
	}
	outbounds, err := f.api.ListOutbounds(context.Background(), &handlercmd.ListOutboundsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range outbounds.Outbounds {
		sender, err := h.SenderSettings.GetInstance()
		if err != nil {
			t.Fatal(err)
		}
		if ss := sender.(*proxyman.SenderConfig).StreamSettings; ss != nil {
			listed["outbound/"+h.Tag] = hysteriaDisablePadding(t, ss)
		}
	}
	for key, want := range map[string]bool{
		"inbound/hy2-in": false, "inbound/hy2-quiet-in": true,
		"outbound/hy2-plain": false, "outbound/hy2-quiet": true, "outbound/owner": true,
	} {
		if got, ok := listed[key]; !ok || got != want {
			t.Errorf("%s: disablePadding listed %v (%v), want %v", key, got, ok, want)
		}
	}
}

// hysteriaDisablePadding reads disable_padding from a handler's Hy2 transport settings.
func hysteriaDisablePadding(t *testing.T, ss *internet.StreamConfig) bool {
	t.Helper()
	for _, ts := range ss.TransportSettings {
		if ts.ProtocolName != "hysteria" {
			continue
		}
		instance, err := ts.Settings.GetInstance()
		if err != nil {
			t.Fatal(err)
		}
		return instance.(*hysteria.Config).GetDisablePadding()
	}
	return false
}
