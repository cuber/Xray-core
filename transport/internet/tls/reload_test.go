package tls

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	gotls "crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/testing/servers/dnsfixture"
	"golang.org/x/crypto/ocsp"
	"google.golang.org/protobuf/proto"
)

func TestCertificateHotReloadConcurrentHandshake(t *testing.T) {
	first, roots := dnsfixture.Certificate(t, "reload.test")
	template := *first.Leaf
	template.SerialNumber = big.NewInt(2)
	secondDER, err := x509.CreateCertificate(rand.Reader, &template, &template, first.Leaf.PublicKey, first.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	secondLeaf, err := x509.ParseCertificate(secondDER)
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(secondLeaf)
	key, err := x509.MarshalPKCS8PrivateKey(first.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: first.Certificate[0]})
	secondPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: secondDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	entry := &Certificate{Certificate: certPEM, Key: keyPEM, CertificatePath: certPath, KeyPath: keyPath, OcspStapling: 1}
	source := &Config{Certificate: []*Certificate{entry}, RejectUnknownSni: true}
	before := proto.Clone(source)
	serverConfig := source.GetTLSConfig()
	hello := &gotls.ClientHelloInfo{ServerName: "reload.test"}
	original, err := serverConfig.GetCertificate(hello)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for ctx.Err() == nil {
				left, right := net.Pipe()
				server, client := gotls.Server(left, serverConfig), gotls.Client(right, &gotls.Config{RootCAs: roots, ServerName: "reload.test"})
				result := make(chan error, 1)
				go func() { result <- server.HandshakeContext(ctx); left.Close() }()
				err := client.HandshakeContext(ctx)
				right.Close()
				serverErr := <-result
				if ctx.Err() == nil && (err != nil || serverErr != nil) {
					t.Errorf("verified handshake failed: client=%v server=%v", err, serverErr)
					return
				}
			}
		})
	}
	defer func() { cancel(); workers.Wait() }()
	replace := func(data []byte) {
		t.Helper()
		temporary := filepath.Join(dir, "next.pem")
		if err := os.WriteFile(temporary, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temporary, certPath); err != nil {
			t.Fatal(err)
		}
	}
	replace(secondPEM)
	deadline := time.Now().Add(4 * time.Second)
	for {
		selected, err := serverConfig.GetCertificate(hello)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(selected.Certificate[0], secondDER) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real file reload never published second certificate")
		}
		time.Sleep(time.Millisecond)
	}
	if !bytes.Equal(original.Certificate[0], first.Certificate[0]) {
		t.Fatal("published old certificate mutated")
	}
	if _, err := serverConfig.GetCertificate(&gotls.ClientHelloInfo{ServerName: "unknown.test"}); err == nil {
		t.Fatal("reload bypassed rejectUnknownSNI")
	}
	replace([]byte("invalid certificate"))
	time.Sleep(1200 * time.Millisecond)
	selected, err := serverConfig.GetCertificate(hello)
	if err != nil || !bytes.Equal(selected.Certificate[0], secondDER) {
		t.Fatal("invalid reload displaced last valid certificate")
	}
	if !proto.Equal(source, before) {
		t.Fatal("reload mutated shared protobuf configuration")
	}
	cancel()
	workers.Wait()
	// Missing files stop this real ticker on its next poll.
	if err := os.Remove(certPath); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
}

func TestOCSPRefreshPreservesPublishedSnapshot(t *testing.T) {
	var response atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/ocsp-response")
		_, _ = w.Write(response.Load().([]byte))
	}))
	defer server.Close()
	certificate, _ := dnsfixture.Certificate(t, "ocsp.test")
	template := *certificate.Leaf
	template.OCSPServer = []string{server.URL}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, certificate.Leaf.PublicKey, certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	makeResponse := func(offset time.Duration) []byte {
		t.Helper()
		data, err := ocsp.CreateResponse(leaf, leaf, ocsp.Response{
			Status: ocsp.Good, SerialNumber: leaf.SerialNumber,
			ThisUpdate: time.Now().Add(offset), NextUpdate: time.Now().Add(time.Hour),
		}, certificate.PrivateKey.(crypto.Signer))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ocsp.ParseResponseForCert(data, leaf, leaf); err != nil {
			t.Fatal(err)
		}
		return data
	}
	first, second := makeResponse(-time.Minute), makeResponse(0)
	response.Store(first)
	key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	// Explicit issuer avoids any certificate download outside the fixture.
	certPEM = append(certPEM, certPEM...)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	config := (&Config{Certificate: []*Certificate{{Certificate: certPEM, Key: keyPEM, CertificatePath: certPath, KeyPath: keyPath, OcspStapling: 1}}}).GetTLSConfig()
	hello := &gotls.ClientHelloInfo{ServerName: "ocsp.test"}
	waitFor := func(want []byte) *gotls.Certificate {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			cert, err := config.GetCertificate(hello)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(cert.OCSPStaple, want) {
				return cert
			}
			if time.Now().After(deadline) {
				t.Fatal("OCSP refresh not published")
			}
			time.Sleep(time.Millisecond)
		}
	}
	original := waitFor(first)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if !bytes.Equal(original.OCSPStaple, first) {
				t.Error("published OCSP snapshot mutated")
				return
			}
		}
	}()
	defer func() { close(stop); <-done }()
	response.Store(second)
	updated := waitFor(second)
	if original == updated || !bytes.Equal(original.OCSPStaple, first) {
		t.Fatal("OCSP refresh changed an in-flight certificate")
	}
	if err := os.Remove(certPath); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
}
