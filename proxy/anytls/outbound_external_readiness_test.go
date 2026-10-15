package anytls_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/anytls"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
)

// Hold Mihomo in provider loading after listener bind. This makes the old
// TCP-connect-only readiness bug reproducible without racing process startup.
func TestAnyTLSMihomoStartupReadiness(t *testing.T) {
	binary := externalControlBinary(t, "MIHOMO")
	f, outbound := outboundFixture(t, "")
	dir := t.TempDir()
	cert := f.inbound["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)["certificates"].([]any)[0].(map[string]any)
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	for path, data := range map[string]string{certPath: strings.Join(cert["certificate"].([]string), "\n"), keyPath: strings.Join(cert["key"].([]string), "\n")} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	loading, release := make(chan struct{}), make(chan struct{})
	var loadOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loadOnce.Do(func() { close(loading) })
		select {
		case <-release:
			fmt.Fprintln(w, "payload:\n  - DOMAIN,unused.invalid")
		case <-r.Context().Done():
		}
	}))
	defer provider.Close()
	defer unblock()
	port := unusedPort(t)
	config := map[string]any{
		"mode": "rule", "log-level": "debug",
		"listeners": []any{map[string]any{"name": "anytls", "type": "anytls", "listen": "127.0.0.1", "port": port,
			"users": map[string]string{"test": "remote-secret"}, "certificate": certPath, "private-key": keyPath}},
		"rule-providers": map[string]any{"startup-barrier": map[string]any{
			"type": "http", "behavior": "classical", "url": provider.URL, "path": "startup.yaml", "proxy": "DIRECT"}},
		"rules": []string{"RULE-SET,startup-barrier,DIRECT", "MATCH,DIRECT"},
	}
	ready := make(chan struct{})
	go func() {
		defer close(ready)
		startExternalControl(t, binary, "MIHOMO", filepath.Join(dir, "server"), config, port)
	}()
	defer func() {
		unblock()
		select {
		case <-ready:
		case <-time.After(12 * time.Second):
			t.Error("startup helper did not join")
		}
	}()
	select {
	case <-loading:
	case <-time.After(5 * time.Second):
		t.Fatal("Mihomo did not reach provider barrier")
	}
	// The TLS/authenticated AnyTLS session succeeds, but the non-Running
	// tunnel closes its first business stream before dialing the destination.
	client, err := engine.NewClient(engine.ClientOptions{Password: "remote-secret", DialOut: func(ctx context.Context) (net.Conn, error) {
		return (&tls.Dialer{Config: f.tls, NetDialer: &net.Dialer{Timeout: time.Second}}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, M.ParseSocksaddr(externalControlEcho(t, "tcp")))
	if err != nil {
		t.Fatal("pre-Running AnyTLS handshake:", err)
	}
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = conn.Write([]byte("before-running"))
	if err == nil {
		var b [1]byte
		_, err = conn.Read(b[:])
	}
	conn.Close()
	client.Close()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("pre-Running business error=%v, want EOF", err)
	}
	t.Log("listener bound and AnyTLS authenticated, but provider barrier makes first business stream EOF")
	select {
	case <-ready:
		t.Fatal("startup helper returned before tunnel Running")
	default:
	}
	unblock()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("post-up readiness did not complete")
	}
	if t.Failed() {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, "server", "running")); err != nil {
		t.Fatal("readiness returned without post-up receipt:", err)
	}
	decoded, err := outbound.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	decoded.(*anytls.ClientConfig).Server.Port = uint32(port)
	outbound.ProxySettings = serial.ToTypedMessage(decoded)
	if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: outbound}); err != nil {
		t.Fatal(err)
	}
	assertExternalSequentialReuse(t, f, outbound, port)
}
