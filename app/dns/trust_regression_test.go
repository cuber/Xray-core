package dns

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	utls "github.com/refraction-networking/utls"
	"github.com/xtls/xray-core/testing/servers/dnsfixture"
	"golang.org/x/net/http2"
)

func TestDNSTrustAndFailedHandshakeFDs(t *testing.T) {
	for _, mode := range []string{"trusted", "unknown-root", "wrong-san"} {
		t.Run(mode, func(t *testing.T) {
			cert, roots := dnsfixture.Certificate(t)
			if mode == "wrong-san" {
				leaf := *cert.Leaf
				leaf.IPAddresses = nil
				leaf.DNSNames = []string{"wrong.invalid"}
				der, err := x509.CreateCertificate(rand.Reader, &leaf, &leaf, cert.Leaf.PublicKey, cert.PrivateKey)
				if err != nil {
					t.Fatal(err)
				}
				cert.Certificate = [][]byte{der}
				cert.Leaf, err = x509.ParseCertificate(der)
				if err != nil {
					t.Fatal(err)
				}
				roots = x509.NewCertPool()
				roots.AddCert(cert.Leaf)
			}
			if mode == "unknown-root" {
				roots = x509.NewCertPool()
			}
			t.Run("doh", func(t *testing.T) {
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
				server.EnableHTTP2 = true
				server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
				server.Config.ErrorLog = log.New(io.Discard, "", 0)
				server.StartTLS()
				defer server.Close()
				u, _ := url.Parse(server.URL)
				s := newDoHNameServer(u, nil, false, false, false, 0, nil, &utls.Config{RootCAs: roots})
				defer s.cacheController.cacheCleanup.Close()
				defer s.httpClient.CloseIdleConnections()
				transport := s.httpClient.Transport.(*http2.Transport)
				attempts := 1
				if mode != "trusted" {
					attempts = 100
				}
				before := openFDCount(t)
				for range attempts {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
					c, err := transport.DialTLSContext(ctx, "tcp", u.Host, nil)
					cancel()
					if c != nil {
						c.Close()
					}
					assertTrustResult(t, mode, err)
				}
				// Let the server retire its final accepted socket, without GC masking leaks.
				deadline := time.Now().Add(2 * time.Second)
				after := openFDCount(t)
				for after > before+2 && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
					after = openFDCount(t)
				}
				t.Logf("handshakes=%d FDs before=%d after=%d", attempts, before, after)
				if after > before+2 {
					t.Fatalf("failed handshake leaked FDs: %d -> %d", before, after)
				}
			})
			t.Run("doq", func(t *testing.T) {
				listener, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"doq"}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				u, _ := url.Parse("quic://" + listener.Addr().String())
				s, err := newQUICNameServer(u, false, false, 0, nil, &tls.Config{RootCAs: roots})
				if err != nil {
					t.Fatal(err)
				}
				defer s.cacheController.cacheCleanup.Close()
				c, err := s.openConnection()
				if c != nil {
					defer c.CloseWithError(0, "test complete")
				}
				assertTrustResult(t, mode, err)
			})
		})
	}
}

func assertTrustResult(t *testing.T, mode string, err error) {
	t.Helper()
	if mode == "trusted" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err == nil {
		t.Fatal("untrusted handshake succeeded")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("failure was not certificate validation: %v", err)
	}
	t.Logf("%s rejected: %v", mode, err)
}

func openFDCount(t *testing.T) int {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Logf("FD enumeration unavailable on %s; certificate trust assertions still run", runtime.GOOS)
		return -1
	}
	path := "/dev/fd"
	if runtime.GOOS == "linux" {
		path = "/proc/self/fd"
	}
	directory, err := os.Open(path)
	if err != nil {
		t.Fatalf("cannot open FD directory: %v", err)
	}
	defer directory.Close()
	entries, err := directory.Readdirnames(-1)
	if err != nil {
		t.Fatalf("cannot verify FD ownership: %v", err)
	}
	return len(entries)
}
