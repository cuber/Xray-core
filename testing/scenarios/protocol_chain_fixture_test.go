package scenarios

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/stats"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/transport/internet"
)

var protocolChainProtocols = []string{"ss2", "vless", "hy2", "anytls"}

type protocolChainPKI struct {
	ca, cert, key []string
}

func protocolChainCertificate(t *testing.T) protocolChainPKI {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "protocol-chain fixture CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{"protocol-chain.test"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	lines := func(kind string, der []byte) []string {
		return strings.Split(strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))), "\n")
	}
	return protocolChainPKI{ca: lines("CERTIFICATE", caDER), cert: lines("CERTIFICATE", der), key: lines("PRIVATE KEY", keyDER)}
}

func protocolChainSecret(hop, user int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("protocol-chain-only/hop=%d/user=%d", hop, user)))
	return base64.StdEncoding.EncodeToString(sum[:16])
}

func protocolChainID(hop, user int) string {
	return fmt.Sprintf("938a1320-9478-415c-bf21-%012d", hop*10+user+1)
}

func protocolChainEmail(hop, user int) string {
	return fmt.Sprintf("chain-hop%d-user%d@test", hop, user)
}

func protocolChainBase(ins, outs []any) map[string]any {
	return map[string]any{
		"log": map[string]any{"loglevel": "error"}, "inbounds": ins, "outbounds": outs, "stats": map[string]any{},
		"policy": map[string]any{
			"levels": map[string]any{"0": map[string]any{
				"handshake": 2, "connIdle": 180, "uplinkOnly": 1, "downlinkOnly": 1,
				"statsUserUplink": true, "statsUserDownlink": true, "bufferSize": 32,
			}},
			"system": map[string]any{"statsInboundUplink": true, "statsInboundDownlink": true, "statsOutboundUplink": true, "statsOutboundDownlink": true},
		},
	}
}

func protocolChainFreedom() map[string]any {
	return map[string]any{
		"tag": "exit", "protocol": "freedom", "sendThrough": "127.0.0.1",
		"settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}},
	}
}

func protocolChainPair(t *testing.T, pki protocolChainPKI, protocol string, hop, port, user int, mux bool) (map[string]any, map[string]any) {
	t.Helper()
	serverTLS := map[string]any{"certificates": []any{map[string]any{"certificate": pki.cert, "key": pki.key}}}
	clientTLS := map[string]any{
		"serverName": "protocol-chain.test", "disableSystemRoot": true,
		"certificates": []any{map[string]any{"usage": "verify", "certificate": pki.ca}},
	}
	in := map[string]any{"tag": "in", "listen": "127.0.0.1", "port": port}
	out := map[string]any{"tag": fmt.Sprintf("hop%d", hop)}
	stream := func(tlsSettings map[string]any) map[string]any {
		return map[string]any{"network": "raw", "security": "tls", "tlsSettings": tlsSettings}
	}
	in["streamSettings"], out["streamSettings"] = stream(serverTLS), stream(clientTLS)
	var clients []any
	for u := range 2 {
		c := map[string]any{"email": protocolChainEmail(hop, u), "level": 0}
		switch protocol {
		case "ss2":
			c["password"] = protocolChainSecret(hop, u)
		case "vless":
			c["id"] = protocolChainID(hop, u)
		case "hy2":
			c["auth"] = protocolChainSecret(hop, u)
		case "anytls":
			c["password"] = protocolChainSecret(hop, u)
		default:
			t.Fatalf("unknown chain protocol %q", protocol)
		}
		clients = append(clients, c)
	}
	switch protocol {
	case "ss2":
		in["protocol"], out["protocol"] = "shadowsocks", "shadowsocks"
		delete(in, "streamSettings")
		out["streamSettings"] = map[string]any{"network": "raw"}
		in["settings"] = map[string]any{"method": "2022-blake3-aes-128-gcm", "password": protocolChainSecret(hop, 9), "clients": clients, "network": "tcp,udp"}
		out["settings"] = map[string]any{"address": "127.0.0.1", "port": port, "method": "2022-blake3-aes-128-gcm", "password": protocolChainSecret(hop, 9) + ":" + protocolChainSecret(hop, user)}
	case "vless":
		in["protocol"], out["protocol"] = "vless", "vless"
		in["settings"] = map[string]any{"decryption": "none", "clients": clients}
		out["settings"] = map[string]any{"address": "127.0.0.1", "port": port, "id": protocolChainID(hop, user), "encryption": "none"}
		out["mux"] = map[string]any{"enabled": mux, "concurrency": 8, "xudpConcurrency": 8}
	case "hy2":
		in["protocol"], out["protocol"] = "hysteria", "hysteria"
		in["settings"] = map[string]any{"version": 2, "clients": clients}
		out["settings"] = map[string]any{"version": 2, "address": "127.0.0.1", "port": port}
		out["sendThrough"] = "127.0.0.1"
		serverTLS["alpn"], clientTLS["alpn"] = []string{"h3"}, []string{"h3"}
		in["streamSettings"] = map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": serverTLS, "hysteriaSettings": map[string]any{"version": 2}}
		out["streamSettings"] = map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": clientTLS, "hysteriaSettings": map[string]any{"version": 2, "auth": protocolChainSecret(hop, user)}}
	case "anytls":
		in["protocol"], out["protocol"] = "anytls", "anytls"
		in["settings"] = map[string]any{"clients": clients}
		out["settings"] = map[string]any{"address": "127.0.0.1", "port": port, "password": protocolChainSecret(hop, user)}
	}
	if mux && protocol != "vless" {
		t.Fatal("outer Mux is restricted to the explicit VLESS layer")
	}
	if hop > 0 {
		out["streamSettings"].(map[string]any)["sockopt"] = map[string]any{"dialerProxy": fmt.Sprintf("hop%d", hop-1)}
	}
	return in, out
}

func protocolChainJSON(t *testing.T, config map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type protocolChainCore struct {
	x         *core.Instance
	once      sync.Once
	listeners *protocolChainListenerOwner
}

func protocolChainStart(t *testing.T, config map[string]any) *protocolChainCore {
	t.Helper()
	parsed, err := core.LoadConfig("json", bytes.NewReader(protocolChainJSON(t, config)))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := protocolChainExpectedListeners(config)
	if err != nil {
		t.Fatal(err)
	}
	listeners := protocolChainWatchListeners(t, expected)
	defer listeners.release()
	x, err := core.New(parsed)
	if err != nil {
		t.Fatal(err)
	}
	c := &protocolChainCore{x: x, listeners: listeners}
	t.Cleanup(func() { c.close(t) })
	if err := x.Start(); err != nil {
		t.Fatal(err)
	}
	if err := listeners.check(false); err != nil {
		t.Fatal(err)
	}
	return c
}

func protocolChainWait(t *testing.T, done <-chan struct{}, budget time.Duration, label string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(budget):
		stack := make([]byte, 2<<20)
		n := runtime.Stack(stack, true)
		t.Fatalf("%s did not retire in %s\n%s", label, budget, stack[:n])
	}
}

func (c *protocolChainCore) close(t *testing.T) {
	t.Helper()
	c.once.Do(func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := c.x.Close(); err != nil {
				t.Errorf("Core.Close: %v", err)
			}
		}()
		protocolChainWait(t, done, 5*time.Second, "Core.Close")
		if err := c.listeners.check(true); err != nil {
			t.Fatal(err)
		}
	})
}

// Reserve both transport ports together; no dial-based readiness polling.
func protocolChainPort(t *testing.T) int {
	t.Helper()
	// A port allocated by one transport is not guaranteed free on the other.
	// Discard allocation collisions only; never retry business requests.
	for range 32 {
		u, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := u.LocalAddr().(*net.UDPAddr).Port
		ln, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		u.Close()
		if errors.Is(err, syscall.EADDRINUSE) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		ln.Close()
		return port
	}
	t.Fatal("could not reserve a joint TCP/UDP fixture port")
	return 0
}

type protocolChainReceipt struct {
	wire     []byte
	hold     bool
	count    int
	seen     chan struct{}
	seenOnce sync.Once
}

type protocolChainBackend struct {
	tcp      net.Listener
	udp      net.PacketConn
	mu       sync.Mutex
	receipts map[string]*protocolChainReceipt
	sockets  map[net.Conn]bool
	wg       sync.WaitGroup
	once     sync.Once
	accepted chan struct{}
	serial   int
	invalid  []string
}

func protocolChainBackendStart(t *testing.T) *protocolChainBackend {
	t.Helper()
	b := &protocolChainBackend{receipts: make(map[string]*protocolChainReceipt), sockets: make(map[net.Conn]bool), accepted: make(chan struct{})}
	var err error
	b.tcp, err = net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b.udp, err = net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		b.tcp.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { b.stop(t, true) })
	b.wg.Add(2)
	go func() {
		defer b.wg.Done()
		defer close(b.accepted)
		for {
			c, err := b.tcp.Accept()
			if err != nil {
				return
			}
			b.mu.Lock()
			b.sockets[c] = true
			b.mu.Unlock()
			b.wg.Add(1)
			go func() {
				defer b.wg.Done()
				defer c.Close()
				defer func() { b.mu.Lock(); delete(b.sockets, c); b.mu.Unlock() }()
				for {
					var size [4]byte
					if _, err := io.ReadFull(c, size[:]); err != nil {
						return
					}
					n := binary.BigEndian.Uint32(size[:])
					if n > 1<<20 {
						b.mu.Lock()
						b.invalid = append(b.invalid, "oversized TCP frame")
						b.mu.Unlock()
						return
					}
					payload := make([]byte, n)
					if _, err := io.ReadFull(c, payload); err != nil {
						return
					}
					if b.received("tcp", payload) {
						continue
					}
					if _, err := c.Write(append(size[:], payload...)); err != nil {
						return
					}
				}
			}()
		}
	}()
	go func() {
		defer b.wg.Done()
		p := make([]byte, 65535)
		for {
			n, peer, err := b.udp.ReadFrom(p)
			if err != nil {
				return
			}
			if !b.received("udp", p[:n]) {
				if _, err := b.udp.WriteTo(p[:n], peer); err != nil {
					return
				}
			}
		}
	}()
	return b
}

func (b *protocolChainBackend) received(network string, payload []byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.receipts[network+string(payload)]
	if r == nil {
		b.invalid = append(b.invalid, fmt.Sprintf("unregistered %s payload (%d bytes)", network, len(payload)))
		return true
	}
	r.count++
	r.seenOnce.Do(func() { close(r.seen) })
	return r.hold
}

func (b *protocolChainBackend) request(network string, size int, hold bool) *protocolChainReceipt {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.serial++
	p := make([]byte, size)
	for i := range p {
		p[i] = byte(b.serial + 37*i)
	}
	r := &protocolChainReceipt{wire: p, hold: hold, seen: make(chan struct{})}
	b.receipts[network+string(p)] = r
	return r
}

func (b *protocolChainBackend) destination(network string) xnet.Destination {
	if network == "tcp" {
		return xnet.TCPDestination(xnet.LocalHostIP, xnet.Port(b.tcp.Addr().(*net.TCPAddr).Port))
	}
	return xnet.UDPDestination(xnet.LocalHostIP, xnet.Port(b.udp.LocalAddr().(*net.UDPAddr).Port))
}

func (b *protocolChainBackend) stop(t *testing.T, force bool) {
	t.Helper()
	b.once.Do(func() {
		b.tcp.Close()
		b.udp.Close()
		protocolChainWait(t, b.accepted, 5*time.Second, "backend accept loop")
		closeSockets := func() {
			b.mu.Lock()
			for c := range b.sockets {
				c.Close()
			}
			b.mu.Unlock()
		}
		if force {
			closeSockets()
		}
		done := make(chan struct{})
		go func() { b.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("backend connections did not naturally retire; forced cleanup is not acceptance")
			closeSockets()
			protocolChainWait(t, done, 5*time.Second, "forced backend worker cleanup")
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		if len(b.sockets) != 0 || len(b.invalid) != 0 {
			t.Errorf("retained backend sockets=%d; invalid receipts=%v", len(b.sockets), b.invalid)
		}
	})
}

func protocolChainExchange(ctx context.Context, client *protocolChainCore, backend *protocolChainBackend, network string, r *protocolChainReceipt) error {
	c, err := core.Dial(ctx, client.x, backend.destination(network))
	if err != nil {
		return err
	}
	return protocolChainExchangeConn(ctx, c, network, r)
}

func protocolChainExchangeConn(ctx context.Context, c net.Conn, network string, r *protocolChainReceipt) error {
	defer c.Close()
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { c.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	wire := r.wire
	if network == "tcp" {
		wire = make([]byte, 4+len(r.wire))
		binary.BigEndian.PutUint32(wire, uint32(len(r.wire)))
		copy(wire[4:], r.wire)
	}
	if network == "udp" {
		// core.Dial's net.Conn.Write splits oversized writes at buf.Size. A
		// boundary probe must submit one datagram, not two smaller datagrams.
		if writer, ok := c.(interface{ WriteMultiBuffer(buf.MultiBuffer) error }); ok {
			if err := writer.WriteMultiBuffer(buf.MultiBuffer{buf.FromBytes(wire)}); err != nil {
				return err
			}
		} else if n, err := c.Write(wire); err != nil || n != len(wire) {
			return fmt.Errorf("%s write %d/%d: %v", network, n, len(wire), err)
		}
	} else if n, err := c.Write(wire); err != nil || n != len(wire) {
		return fmt.Errorf("%s write %d/%d: %v", network, n, len(wire), err)
	}
	got := make([]byte, len(wire)+1)
	var n int
	var err error
	if network == "tcp" {
		n, err = io.ReadFull(c, got[:len(wire)])
	} else {
		n, err = c.Read(got)
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(got[:n], wire) {
		return fmt.Errorf("wrong %s destination/payload: got %d want %d bytes", network, n, len(wire))
	}
	return nil
}

type protocolChainFixture struct {
	servers       []*protocolChainCore
	configs       []map[string]any
	clients       []*protocolChainCore
	clientConfigs []map[string]any
	ports         []int
	back          []*protocolChainBackend
}

func protocolChainFixtureStart(t *testing.T, protocols []string, mux bool) *protocolChainFixture {
	t.Helper()
	f := &protocolChainFixture{}
	pki := protocolChainCertificate(t)
	for range 2 {
		f.back = append(f.back, protocolChainBackendStart(t))
	}
	for hop, protocol := range protocols {
		port := protocolChainPort(t)
		in, _ := protocolChainPair(t, pki, protocol, hop, port, 0, mux && hop == 0)
		config := protocolChainBase([]any{in}, []any{protocolChainFreedom()})
		f.ports = append(f.ports, port)
		f.configs = append(f.configs, config)
		f.servers = append(f.servers, protocolChainStart(t, config))
	}
	for user := range 2 {
		outs := make([]any, len(protocols))
		for hop, protocol := range protocols {
			_, out := protocolChainPair(t, pki, protocol, hop, f.ports[hop], user, mux && hop == 0)
			// The default handler is the final protocol. Its transport is dialed
			// recursively through all preceding native outbound handlers.
			outs[len(protocols)-1-hop] = out
		}
		config := protocolChainBase(nil, outs)
		f.clientConfigs = append(f.clientConfigs, config)
		f.clients = append(f.clients, protocolChainStart(t, config))
	}
	return f
}

func (f *protocolChainFixture) close(t *testing.T) {
	t.Helper()
	// Each owner checks its original RawConn, not a port a foreign socket may reuse.
	for _, c := range f.clients {
		c.close(t)
	}
	for _, c := range f.servers {
		c.close(t)
	}
	for _, b := range f.back {
		// No forced backend connection close on the success path. EOF must be
		// caused by the real client/Core shutdown, not by fixture cleanup.
		b.stop(t, false)
	}
}

func protocolChainUserBytes(c *protocolChainCore, hop, user int) [2]int64 {
	manager := c.x.GetFeature(stats.ManagerType()).(stats.Manager)
	var values [2]int64
	for i, direction := range []string{"uplink", "downlink"} {
		if counter := manager.GetCounter("user>>>" + protocolChainEmail(hop, user) + ">>>traffic>>>" + direction); counter != nil {
			values[i] = counter.Value()
		}
	}
	return values
}

type protocolChainDiagnosticDialer struct {
	internet.DefaultSystemDialer
	mu     sync.Mutex
	events []string
}

func (d *protocolChainDiagnosticDialer) event(value string) {
	d.mu.Lock()
	d.events = append(d.events, value)
	d.mu.Unlock()
}

func (d *protocolChainDiagnosticDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, opts *internet.SocketConfig) (net.Conn, error) {
	c, err := d.DefaultSystemDialer.Dial(ctx, source, dest, opts)
	if dest.Network != xnet.Network_UDP {
		return c, err
	}
	if err != nil {
		d.event(fmt.Sprintf("UDP dial source=%v dest=%s err=%v", source, dest, err))
		return nil, err
	}
	d.event(fmt.Sprintf("UDP open local=%s remote=%s requestedSource=%v", c.LocalAddr(), c.RemoteAddr(), source))
	return &protocolChainDiagnosticConn{Conn: c, owner: d}, nil
}

type protocolChainDiagnosticConn struct {
	net.Conn
	owner *protocolChainDiagnosticDialer
}

func (c *protocolChainDiagnosticConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.owner.event(fmt.Sprintf("UDP read local=%s remote=%s n=%d sha=%x err=%v", c.LocalAddr(), c.RemoteAddr(), n, sha256.Sum256(p[:n]), err))
	return n, err
}

func (c *protocolChainDiagnosticConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.owner.event(fmt.Sprintf("UDP write local=%s remote=%s n=%d sha=%x err=%v", c.LocalAddr(), c.RemoteAddr(), n, sha256.Sum256(p[:n]), err))
	return n, err
}

func (c *protocolChainDiagnosticConn) Close() error {
	err := c.Conn.Close()
	c.owner.event(fmt.Sprintf("UDP close local=%s remote=%s err=%v", c.LocalAddr(), c.RemoteAddr(), err))
	return err
}
