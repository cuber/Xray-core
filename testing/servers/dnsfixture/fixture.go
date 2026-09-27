// Package dnsfixture provides loopback-only DNS and TLS fixtures for tests.
package dnsfixture

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func Certificate(t testing.TB, names ...string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: names,
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, roots
}

// Start owns the socket before starting the server, avoiding free-port races.
func Start(t testing.TB, network string, handler dns.Handler) string {
	t.Helper()
	address, _ := StartWithStop(t, network, handler)
	return address
}

func StartWithStop(t testing.TB, network string, handler dns.Handler) (string, func()) {
	t.Helper()
	server := &dns.Server{Net: network, Handler: handler}
	var address string
	if network == "tcp" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server.Listener = listener
		address = listener.Addr().String()
	} else {
		conn, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server.PacketConn = conn
		address = conn.LocalAddr().String()
	}
	ready := make(chan struct{})
	done := make(chan error, 1)
	server.NotifyStartedFunc = func() { close(ready) }
	go func() { done <- server.ActivateAndServe() }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("DNS fixture start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("DNS fixture start timeout")
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			if err := server.Shutdown(); err != nil {
				t.Error(err)
			}
			if err := <-done; err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(stop)
	return address, stop
}
