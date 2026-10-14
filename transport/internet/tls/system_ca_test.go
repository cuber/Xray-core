package tls_test

import (
	"context"
	gotls "crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/protocol/tls/cert"
	xraytls "github.com/xtls/xray-core/transport/internet/tls"
)

func TestSystemOnlyCATrustSelection(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("isolated system trust fixture uses Linux SSL_CERT_FILE, not host trust-store edits")
	}
	if os.Getenv("XRAY_TEST_SYSTEM_CA_CHILD") != "1" {
		root, _ := cert.MustGenerate(nil, cert.Authority(true), cert.KeyUsage(x509.KeyUsageCertSign))
		leaf, _ := cert.MustGenerate(root, cert.DNSNames("system-only.example.test"))
		rootPEM, _ := root.ToPEM()
		leafPEM, keyPEM := leaf.ToPEM()
		dir := t.TempDir()
		emptyRoots := filepath.Join(dir, "empty-roots")
		if err := os.Mkdir(emptyRoots, 0700); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{"root.pem": rootPEM, "leaf.pem": leafPEM, "key.pem": keyPEM} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		// A fresh process prevents x509's once-cached system roots from another test
		// from masking which trust source the production config actually selects.
		t.Setenv("XRAY_TEST_SYSTEM_CA_CHILD", "1")
		t.Setenv("SSL_CERT_FILE", filepath.Join(dir, "root.pem"))
		t.Setenv("SSL_CERT_DIR", emptyRoots)
		t.Setenv("GODEBUG", "x509usefallbackroots=0")
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSystemOnlyCATrustSelection$", "-test.v", "-test.count=1")
		output, err := cmd.CombinedOutput()
		t.Logf("isolated trust process:\n%s", output)
		if err != nil {
			t.Fatal("isolated trust process failed", err)
		}
		return
	}

	dir := filepath.Dir(os.Getenv("SSL_CERT_FILE"))
	pair, err := gotls.LoadX509KeyPair(filepath.Join(dir, "leaf.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	rootPEM, err := os.ReadFile(filepath.Join(dir, "root.pem"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "verified")
	}))
	server.TLS = &gotls.Config{Certificates: []gotls.Certificate{pair}}
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	for _, tc := range []struct {
		name                        string
		system, explicit, wrongName bool
	}{
		{name: "bundle-rejects-system-only-root"},
		{name: "system-accepts-system-only-root", system: true},
		{name: "explicit-root-independent-control", explicit: true},
		{name: "system-rejects-wrong-name", system: true, wrongName: true},
		{name: "explicit-rejects-wrong-name", explicit: true, wrongName: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := &xraytls.Config{UseSystemCa: tc.system, ServerName: "system-only.example.test"}
			if tc.explicit {
				config.Certificate = []*xraytls.Certificate{{Certificate: rootPEM, Usage: xraytls.Certificate_AUTHORITY_VERIFY}}
			}
			if tc.wrongName {
				config.ServerName = "wrong.example.test"
			}
			tlsConfig := config.GetTLSConfig()
			if tlsConfig.InsecureSkipVerify {
				t.Fatal("fixture must retain normal TLS verification")
			}
			transport := &http.Transport{TLSClientConfig: tlsConfig}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			response, err := client.Get(server.URL)
			if response != nil {
				defer response.Body.Close()
			}
			if tc.wrongName {
				var nameError x509.HostnameError
				if !errors.As(err, &nameError) {
					t.Fatalf("expected hostname rejection, got %v", err)
				}
			} else if !tc.system && !tc.explicit {
				var trustError x509.UnknownAuthorityError
				if !errors.As(err, &trustError) {
					t.Fatalf("expected unknown-root rejection, got %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				if err != nil || string(body) != "verified" || len(response.TLS.VerifiedChains) == 0 {
					t.Fatal("verified TLS response missing", err)
				}
			}
		})
	}
}
