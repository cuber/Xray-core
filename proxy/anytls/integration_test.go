package anytls_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
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

	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/proxy/anytls"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type integrationFixture struct {
	address       string
	api           handler.HandlerServiceClient
	stats         stats.StatsServiceClient
	tls           *tls.Config
	inbound       map[string]any
	caPEM         []byte
	caFingerprint string
}

func unusedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return p
}

func unusedTCPUDPPort(t *testing.T) int {
	t.Helper()
	for attempt := 0; attempt < 100; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		packet, err := net.ListenPacket("udp", listener.Addr().String())
		listener.Close()
		if err == nil {
			packet.Close()
			return port
		}
	}
	t.Fatal("could not reserve a port available for both TCP and UDP")
	return 0
}

func echoTCP(t *testing.T, prefix string) string {
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
				conn.SetDeadline(time.Now().Add(15 * time.Minute))
				conn.Write([]byte(prefix))
				// Keep the fixture out of Linux's process-wide splice pipe pool;
				// resource tests measure the proxy, not cached echo-server pipes.
				io.Copy(conn, struct{ io.Reader }{conn})
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

func newFixture(t *testing.T, userStats, domainStats bool, customize ...func(map[string]any)) *integrationFixture {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "anytls.test"},
		DNSNames: []string{"anytls.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(99), Subject: pkix.Name{CommonName: "AnyTLS test CA"},
		NotBefore: cert.NotBefore, NotAfter: cert.NotAfter,
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	der, err := x509.CreateCertificate(rand.Reader, cert, ca, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certPEM = append(certPEM, caPEM...)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	p, apiPort := unusedTCPUDPPort(t), unusedPort(t)
	inbound := map[string]any{
		"tag": "anytls-test", "listen": "127.0.0.1", "port": p, "protocol": "anytls",
		"settings": map[string]any{"clients": []any{}, "maxSessionsPerUser": 64},
		"streamSettings": map[string]any{"network": "raw", "security": "tls", "tlsSettings": map[string]any{
			"certificates": []any{map[string]any{"certificate": strings.Split(string(certPEM), "\n"), "key": strings.Split(string(keyPEM), "\n")}},
		}},
	}
	redirect := func(tag, addr string) any {
		return map[string]any{"tag": tag, "protocol": "freedom", "settings": map[string]any{"redirect": addr, "ipsBlocked": []string{}}}
	}
	config := map[string]any{
		"log":   map[string]any{"loglevel": "error", "access": "none"},
		"api":   map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort), "services": []string{"HandlerService", "StatsService"}},
		"stats": map[string]any{"domainTraffic": map[string]any{"enabled": domainStats, "bucketIntervalSeconds": 1}},
		"policy": map[string]any{
			"levels": map[string]any{"0": map[string]any{"handshake": 2, "connIdle": 10, "uplinkOnly": 1, "downlinkOnly": 1, "statsUserUplink": userStats, "statsUserDownlink": userStats}},
			"system": map[string]any{"statsInboundUplink": true, "statsInboundDownlink": true},
		},
		"outbounds": []any{redirect("a", echoTCP(t, "A")), redirect("b", echoTCP(t, "B")), redirect("udp", echoUDP(t))},
		"routing": map[string]any{"rules": []any{
			map[string]any{"type": "field", "network": "udp", "outboundTag": "udp"},
			map[string]any{"type": "field", "user": []string{"bob"}, "outboundTag": "b"},
			map[string]any{"type": "field", "domain": []string{"full:beta.test"}, "outboundTag": "b"},
		}},
	}
	for _, apply := range customize {
		apply(config)
	}
	raw, _ := json.Marshal(config)
	x, err := core.StartInstance("json", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	grpcConn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", apiPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { grpcConn.Close() })
	f := &integrationFixture{
		address: fmt.Sprintf("127.0.0.1:%d", p), api: handler.NewHandlerServiceClient(grpcConn),
		stats: stats.NewStatsServiceClient(grpcConn), tls: &tls.Config{RootCAs: roots, ServerName: "anytls.test"}, inbound: inbound,
		caPEM: caPEM, caFingerprint: fmt.Sprintf("%x", sha256.Sum256(caDER)),
	}
	if err := f.addInbound(t, inbound); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *integrationFixture) addInbound(t *testing.T, value map[string]any) error {
	t.Helper()
	raw, _ := json.Marshal(value)
	var c conf.InboundDetourConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}
	config, err := c.Build()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = f.api.AddInbound(ctx, &handler.AddInboundRequest{Inbound: config})
	return err
}

func (f *integrationFixture) alter(email, password string, remove bool) error {
	var op *serial.TypedMessage
	if remove {
		op = serial.ToTypedMessage(&handler.RemoveUserOperation{Email: email})
	} else {
		op = serial.ToTypedMessage(&handler.AddUserOperation{User: &protocol.User{Email: email, Account: serial.ToTypedMessage(&anytls.Account{Password: password})}})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := f.api.AlterInbound(ctx, &handler.AlterInboundRequest{Tag: "anytls-test", Operation: op})
	return err
}

func (f *integrationFixture) client(t *testing.T, password string) *engine.Client {
	t.Helper()
	c, err := engine.NewClient(engine.ClientOptions{Password: password, DialOut: func(ctx context.Context) (net.Conn, error) {
		return (&tls.Dialer{Config: f.tls, NetDialer: &net.Dialer{Timeout: 3 * time.Second}}).DialContext(ctx, "tcp", f.address)
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func exchange(c *engine.Client, domain, payload string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, err := c.DialContext(ctx, M.Socksaddr{Fqdn: domain, Port: 80})
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		return "", err
	}
	result := make([]byte, len(payload)+1)
	_, err = io.ReadFull(conn, result)
	return string(result), err
}

func TestAnyTLSControlRoutingStatistics(t *testing.T) {
	f := newFixture(t, true, true)
	for _, user := range []string{"alice", "bob"} {
		if err := f.alter(user, user+"-secret", false); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.alter("duplicate-password", "alice-secret", false); err == nil {
		t.Fatal("duplicate password accepted via gRPC")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	users, err := f.api.GetInboundUsers(ctx, &handler.GetInboundUserRequest{Tag: "anytls-test"})
	if err != nil || len(users.Users) != 2 {
		t.Fatal("runtime users", users, err)
	}
	count, err := f.api.GetInboundUsersCount(ctx, &handler.GetInboundUserRequest{Tag: "anytls-test"})
	if err != nil || count.Count != 2 {
		t.Fatal("runtime count", count, err)
	}
	list, err := f.api.ListInbounds(ctx, &handler.ListInboundsRequest{})
	if err != nil || len(list.Inbounds) != 1 {
		t.Fatal("runtime inbounds", list, err)
	}
	decoded, err := list.Inbounds[0].ProxySettings.GetInstance()
	if _, ok := decoded.(*anytls.ServerConfig); err != nil || !ok {
		t.Fatal("AnyTLS proto not decoded", err)
	}
	alice, bob := f.client(t, "alice-secret"), f.client(t, "bob-secret")
	for _, tc := range []struct {
		client                *engine.Client
		domain, payload, want string
	}{
		{alice, "alpha.test", "hello", "Ahello"},
		{alice, "beta.test", "world!", "Bworld!"},
		{bob, "alpha.test", "bob", "Bbob"},
	} {
		got, err := exchange(tc.client, tc.domain, tc.payload)
		if err != nil || got != tc.want {
			t.Fatalf("exchange %s: %q %v", tc.domain, got, err)
		}
	}
	for name, expected := range map[string]int64{
		"user>>>alice>>>traffic>>>uplink": 11, "user>>>alice>>>traffic>>>downlink": 13,
		"user>>>bob>>>traffic>>>uplink": 3, "user>>>bob>>>traffic>>>downlink": 4,
	} {
		result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: name})
		if err != nil || result.Stat.Value != expected {
			t.Fatalf("%s: %v expected %d (%v)", name, result, expected, err)
		}
	}
	for direction, min := range map[string]int64{"uplink": 14, "downlink": 17} {
		result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "inbound>>>anytls-test>>>traffic>>>" + direction})
		if err != nil || result.Stat.Value <= min {
			t.Fatal("inbound counter must include protocol overhead", result, err)
		}
	}
	time.Sleep(1100 * time.Millisecond)
	buckets, err := f.stats.GetDomainTrafficBuckets(ctx, &stats.GetDomainTrafficBucketsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	totals := map[string][2]uint64{}
	for _, bucket := range buckets.Buckets {
		for _, entry := range bucket.Entries {
			key := entry.User + "/" + entry.Domain
			previous := totals[key]
			totals[key] = [2]uint64{previous[0] + entry.UplinkBytes, previous[1] + entry.DownlinkBytes}
		}
	}
	for key, expected := range map[string][2]uint64{"alice/alpha.test": {5, 6}, "alice/beta.test": {6, 7}, "bob/alpha.test": {3, 4}} {
		if totals[key] != expected {
			t.Fatalf("domain stats %s = %v want %v", key, totals[key], expected)
		}
	}
	if err := f.alter("alice", "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange(alice, "alpha.test", "denied"); err == nil {
		t.Fatal("removed user still works")
	}
	if got, err := exchange(bob, "alpha.test", "ok"); err != nil || got != "Bok" {
		t.Fatal("unrelated user disrupted", got, err)
	}
	if err := f.alter("alice", "new-secret", false); err != nil {
		t.Fatal(err)
	}
	if got, err := exchange(f.client(t, "new-secret"), "alpha.test", "new"); err != nil || got != "Anew" {
		t.Fatal("replacement user failed", got, err)
	}
	if _, err := f.api.RemoveInbound(ctx, &handler.RemoveInboundRequest{Tag: "anytls-test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange(bob, "alpha.test", "closed"); err == nil {
		t.Fatal("removed inbound still accepts traffic")
	}
	if err := f.addInbound(t, f.inbound); err != nil {
		t.Fatal("listener leaked after removal", err)
	}
}

func TestAnyTLSUoT(t *testing.T) {
	f := newFixture(t, true, true)
	if err := f.alter("udp-user", "udp-secret", false); err != nil {
		t.Fatal(err)
	}
	c := f.client(t, "udp-secret")
	conn, err := c.DialContext(context.Background(), M.Socksaddr{Fqdn: uot.MagicAddress})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	request := uot.Request{Destination: M.ParseSocksaddr("127.0.0.1:53")}
	if err := uot.WriteRequest(conn, request); err != nil {
		t.Fatal(err)
	}
	packets := uot.NewConn(conn, request)
	var total int64
	for i, size := range []int{1, 512, 8192} {
		payload := bytes.Repeat([]byte{byte(i + 1)}, size)
		dest := M.Socksaddr{Fqdn: fmt.Sprintf("udp-%d.test", i), Port: 53}
		if err := packets.WritePacket(B.As(payload), dest); err != nil {
			t.Fatal(err)
		}
		response := B.NewSize(65535)
		_, err := packets.ReadPacket(response)
		if err != nil || !bytes.Equal(response.Bytes(), payload) {
			t.Fatalf("UDP payload %d: got %d bytes (%v)", size, response.Len(), err)
		}
		response.Release()
		total += int64(size)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, direction := range []string{"uplink", "downlink"} {
		result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>udp-user>>>traffic>>>" + direction})
		if err != nil || result.Stat.Value != total {
			t.Fatal("UoT counter contains framing or lost payload", result, total, err)
		}
	}
}
