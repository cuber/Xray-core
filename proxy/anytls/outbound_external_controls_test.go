package anytls_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"golang.org/x/net/proxy"
)

// These controls deliberately start no Core instance: neither leg may fall back
// to Core's vendored engine or accidentally take a DIRECT loopback rule.
func TestAnyTLSExternalControlPairs(t *testing.T) {
	for _, clientKind := range []string{"SINGBOX", "MIHOMO"} {
		serverKind := "MIHOMO"
		if clientKind == "MIHOMO" {
			serverKind = "SINGBOX"
		}
		t.Run(clientKind+"_to_"+serverKind, func(t *testing.T) {
			clientBin, serverBin := externalControlBinary(t, clientKind), externalControlBinary(t, serverKind)
			dir := t.TempDir()
			certPath, keyPath, caPath, fingerprint := externalControlCertificate(t, dir)
			port, socksPort := unusedPort(t), unusedTCPUDPPort(t)
			var server, client map[string]any
			if serverKind == "SINGBOX" {
				server = map[string]any{"log": map[string]any{"level": "error"}, "inbounds": []any{map[string]any{
					"type": "anytls", "listen": "127.0.0.1", "listen_port": port,
					"users": []any{map[string]any{"name": "control", "password": "control-secret"}},
					"tls":   map[string]any{"enabled": true, "certificate_path": certPath, "key_path": keyPath},
				}}, "outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}, "route": map[string]any{"final": "direct"}}
			} else {
				server = map[string]any{"mode": "rule", "log-level": "error", "listeners": []any{map[string]any{
					"name": "anytls", "type": "anytls", "listen": "127.0.0.1", "port": port,
					"users": map[string]string{"control": "control-secret"}, "certificate": certPath, "private-key": keyPath,
				}}, "rules": []string{"MATCH,DIRECT"}}
			}
			if clientKind == "SINGBOX" {
				client = map[string]any{"log": map[string]any{"level": "error"}, "inbounds": []any{map[string]any{
					"type": "socks", "listen": "127.0.0.1", "listen_port": socksPort,
				}}, "outbounds": []any{map[string]any{
					"type": "anytls", "tag": "proxy", "server": "127.0.0.1", "server_port": port, "password": "control-secret",
					"tls": map[string]any{"enabled": true, "server_name": "anytls.test", "certificate_path": caPath},
				}}, "route": map[string]any{"final": "proxy"}}
			} else {
				client = map[string]any{"socks-port": socksPort, "bind-address": "127.0.0.1", "mode": "rule", "log-level": "error",
					"proxies": []any{map[string]any{"name": "proxy", "type": "anytls", "server": "127.0.0.1", "port": port,
						"password": "control-secret", "sni": "anytls.test", "fingerprint": fingerprint, "skip-cert-verify": false, "udp": true}},
					"rules": []string{"MATCH,proxy"}}
			}
			stopServer := startExternalControl(t, serverBin, serverKind, filepath.Join(dir, "server"), server, port)
			startExternalControl(t, clientBin, clientKind, filepath.Join(dir, "client"), client, socksPort)
			socks := fmt.Sprintf("127.0.0.1:%d", socksPort)
			dialer, err := proxy.SOCKS5("tcp", socks, nil, &net.Dialer{Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			target, udpTarget := externalControlEcho(t, "tcp"), externalControlEcho(t, "udp")
			// The first full exchange waits for Mihomo's tunnel Running state,
			// which is later than listener bind. Acceptance exchanges never retry.
			deadline := time.Now().Add(10 * time.Second)
			for {
				err = externalControlTCP(dialer, target, []byte("ready"))
				if err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("control forwarding readiness:", err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			for _, size := range []int{1, 8192, 65535, 65536, 1024 * 1024} {
				payload := make([]byte, size)
				if _, err := rand.Read(payload); err != nil {
					t.Fatal(err)
				}
				if err := externalControlTCP(dialer, target, payload); err != nil {
					t.Fatalf("TCP size=%d: %v", size, err)
				}
			}
			externalControlUDP(t, socks, udpTarget)
			stopServer()
			if err := externalControlTCP(dialer, target, []byte("must-not-bypass")); err == nil {
				t.Fatal("control succeeded without AnyTLS server: unexpected direct bypass")
			}
		})
	}
}

func externalControlBinary(t *testing.T, kind string) string {
	t.Helper()
	binary := os.Getenv("ANYTLS_" + kind)
	if binary == "" {
		if os.Getenv("ANYTLS_STRICT") == "1" {
			t.Fatal("strict acceptance requires ANYTLS_" + kind)
		}
		t.Skip("set ANYTLS_" + kind + " to a cached executable")
	}
	return binary
}

func externalControlCertificate(t *testing.T, dir string) (string, string, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "External control CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"anytls.test"},
		NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	certPath, keyPath, caPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"), filepath.Join(dir, "ca.pem")
	for path, data := range map[string][]byte{
		certPath: append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), caPEM...),
		keyPath:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), caPath: caPEM,
	} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return certPath, keyPath, caPath, fmt.Sprintf("%x", sha256.Sum256(caDER))
}

func startExternalControl(t *testing.T, binary, kind, dir string, config map[string]any, port int) func() {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	recordAnyTLSFixture(t, kind, raw)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"run", "-c", path}
	if kind == "MIHOMO" {
		// Certificates live beside the client/server config directories. Keep
		// Mihomo's file-access sandbox enabled with that private fixture root.
		args = []string{"-d", filepath.Dir(dir), "-f", path}
	}
	logPath := filepath.Join(dir, "process.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		cancel()
		log.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done; log.Close() }) }
	t.Cleanup(func() {
		stop()
		if t.Failed() {
			data, _ := os.ReadFile(logPath)
			t.Logf("%s diagnostics:\n%s", dir, data)
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return stop
		}
		if time.Now().After(deadline) {
			t.Fatal("external process listener readiness timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func externalControlTCP(dialer proxy.Dialer, target string, payload []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dialer.(proxy.ContextDialer).DialContext(ctx, "tcp", target)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.Copy(conn, bytes.NewReader(payload)); err != nil {
		return err
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		return err
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("payload mismatch (%d bytes)", len(payload))
	}
	return nil
}

func externalControlUDP(t *testing.T, socks, target string) {
	t.Helper()
	control, err := net.DialTimeout("tcp", socks, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	control.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := control.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	var greeting [2]byte
	if _, err := io.ReadFull(control, greeting[:]); err != nil || greeting != [2]byte{5, 0} {
		t.Fatal("SOCKS greeting", greeting, err)
	}
	if _, err := control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	var header [3]byte
	if _, err := io.ReadFull(control, header[:]); err != nil || header != [3]byte{5, 0, 0} {
		t.Fatal("SOCKS associate", header, err)
	}
	relay, err := M.SocksaddrSerializer.ReadAddrPort(control)
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.DialUDP("udp", nil, relay.UDPAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	destination := M.ParseSocksaddr(target)
	for _, size := range []int{1, 512, 8192} {
		payload := make([]byte, size)
		if _, err := rand.Read(payload); err != nil {
			t.Fatal(err)
		}
		request := bytes.NewBuffer([]byte{0, 0, 0})
		if err := M.SocksaddrSerializer.WriteAddrPort(request, destination); err != nil {
			t.Fatal(err)
		}
		request.Write(payload)
		udp.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := udp.Write(request.Bytes()); err != nil {
			t.Fatal(err)
		}
		packet := make([]byte, 65535)
		n, err := udp.Read(packet)
		if err != nil {
			t.Fatal(err)
		}
		if n < 3 || !bytes.Equal(packet[:3], []byte{0, 0, 0}) {
			t.Fatal("invalid SOCKS UDP header")
		}
		response := bytes.NewReader(packet[3:n])
		from, err := M.SocksaddrSerializer.ReadAddrPort(response)
		got, readErr := io.ReadAll(response)
		if err != nil || readErr != nil || from != destination || !bytes.Equal(got, payload) {
			t.Fatalf("UDP size=%d source=%v want=%v error=%v/%v", size, from, destination, err, readErr)
		}
	}
}

// Own and join the echo workers; teardown does not rely on process exit or GC.
func externalControlEcho(t *testing.T, network string) string {
	t.Helper()
	var workers sync.WaitGroup
	if network == "udp" {
		conn, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			buffer := make([]byte, 65535)
			for {
				n, peer, err := conn.ReadFrom(buffer)
				if err != nil {
					return
				}
				if _, err := conn.WriteTo(buffer[:n], peer); err != nil {
					return
				}
			}
		}()
		t.Cleanup(func() { conn.Close(); workers.Wait() })
		return conn.LocalAddr().String()
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	connections := make(map[net.Conn]bool)
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { conn.Close(); mu.Lock(); delete(connections, conn); mu.Unlock() }()
				io.Copy(conn, struct{ io.Reader }{conn})
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-acceptDone
		mu.Lock()
		for conn := range connections {
			conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	return listener.Addr().String()
}
