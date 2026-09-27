package anytls_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	xrayTLS "github.com/xtls/xray-core/transport/internet/tls"
)

func TestAnyTLSCertificateAndAccessLog(t *testing.T) {
	dir := t.TempDir()
	access, errorPath := filepath.Join(dir, "access.log"), filepath.Join(dir, "error.log")
	f := newFixture(t, true, true, func(config map[string]any) {
		config["log"] = map[string]any{"loglevel": "debug", "access": access, "error": errorPath}
		config["routing"] = map[string]any{"rules": []any{
			map[string]any{"type": "field", "inboundTag": []string{"anytls-test"}, "network": "tcp", "user": []string{"domain:route.test"}, "outboundTag": "b"},
		}}
	})
	const password = "observability-test-secret"
	if err := f.alter("alice@route.test", password, false); err != nil {
		t.Fatal(err)
	}
	if got, err := exchange(f.client(t, password), "alpha.test", "log-test"); err != nil || got != "Blog-test" {
		t.Fatalf("combined domain-user/inbound/network route: %q %v", got, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, err := f.api.ListInbounds(ctx, &handler.ListInboundsRequest{})
	if err != nil || len(list.Inbounds) != 1 {
		t.Fatal("runtime certificate query", err)
	}
	decoded, err := list.Inbounds[0].ReceiverSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	receiver := decoded.(*proxyman.ReceiverConfig)
	var certificate []byte
	for _, setting := range receiver.StreamSettings.SecuritySettings {
		instance, err := setting.GetInstance()
		if err != nil {
			t.Fatal(err)
		}
		if config, ok := instance.(*xrayTLS.Config); ok && len(config.Certificate) > 0 {
			certificate = config.Certificate[0].Certificate
		}
	}
	block, _ := pem.Decode(certificate)
	if block == nil {
		t.Fatal("control plane did not expose the loaded TLS certificate")
	}
	conn, err := tls.Dial("tcp", f.address, f.tls)
	if err != nil {
		t.Fatal(err)
	}
	actual := conn.ConnectionState().PeerCertificates[0].Raw
	conn.Close()
	if !bytes.Equal(actual, block.Bytes) {
		t.Fatal("API certificate differs from the certificate actually served")
	}
	if _, err := exchange(f.client(t, "rejected-"+password), "alpha.test", "denied"); err == nil {
		t.Fatal("invalid authentication succeeded")
	}
	for {
		logData, err := os.ReadFile(access)
		if err == nil && strings.Contains(string(logData), "alice@route.test") && strings.Contains(string(logData), "alpha.test") && strings.Contains(string(logData), "anytls-test") {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("access log missing email, tag or destination", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	hash := sha256.Sum256([]byte(password))
	for _, path := range []string{access, errorPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{password, hex.EncodeToString(hash[:]), "PRIVATE KEY"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("authentication material leaked into logs")
			}
		}
	}
}
