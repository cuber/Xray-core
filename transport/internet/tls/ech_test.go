package tls

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	stdtls "crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/xtls/xray-core/testing/networktest"
	"github.com/xtls/xray-core/testing/servers/dnsfixture"
	"golang.org/x/crypto/cryptobyte"
)

func localECHKey(t *testing.T) (stdtls.EncryptedClientHelloKey, []byte) {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var b cryptobyte.Builder
	b.AddUint16(0xfe0d)
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint8(1)
		b.AddUint16(0x20)
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(key.PublicKey().Bytes()) })
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddUint16(1); b.AddUint16(1) })
		b.AddUint8(0)
		b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes([]byte("public.test")) })
		b.AddUint16(0)
	})
	config, err := b.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var list cryptobyte.Builder
	list.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(config) })
	data, err := list.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return stdtls.EncryptedClientHelloKey{Config: config, PrivateKey: key.Bytes()}, data
}

func checkECHCache(t *testing.T, key string) {
	t.Helper()
	cache, ok := GlobalECHConfigCache.Load(key)
	if !ok {
		t.Fatal("ECH config cache not found")
	}
	if !cache.UpdateLock.TryLock() {
		t.Fatal("ECH cache update lock still held")
	}
	cache.UpdateLock.Unlock()
	if record := cache.configRecord.Load(); record == nil || record.err != nil || len(record.config) == 0 {
		t.Fatalf("invalid ECH cache record: %v", record)
	}
}

func concurrentECHRequests(t *testing.T, config *Config, target string, roots *x509.CertPool, dial func(context.Context, string, string) (net.Conn, error)) {
	t.Helper()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tlsConfig := config.GetTLSConfig()
			tlsConfig.NextProtos = []string{"http/1.1"}
			if roots != nil {
				tlsConfig.RootCAs = roots
			}
			transport := &http.Transport{TLSClientConfig: tlsConfig, DialContext: dial}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
			response, err := client.Get(target)
			if err != nil {
				t.Errorf("ECH request: %v", err)
				return
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, 65536))
			if err != nil {
				t.Error(err)
				return
			}
			if response.StatusCode != 200 || response.TLS == nil || !response.TLS.ECHAccepted || !strings.Contains(string(body), "sni=encrypted") {
				t.Errorf("ECH was not accepted: status=%d body=%q", response.StatusCode, body)
			}
		}()
	}
	wg.Wait()
}

func TestECHDial(t *testing.T) {
	key, list := localECHKey(t)
	cert, roots := dnsfixture.Certificate(t, "secret.test", "public.test")
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || !r.TLS.ECHAccepted || r.TLS.ServerName != "secret.test" {
			t.Error("server did not accept inner SNI")
			http.Error(w, "ECH required", 400)
			return
		}
		_, _ = io.WriteString(w, "sni=encrypted\n")
	}))
	server.TLS = &stdtls.Config{Certificates: []stdtls.Certificate{cert}, EncryptedClientHelloKeys: []stdtls.EncryptedClientHelloKey{key}, MinVersion: stdtls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	var queries atomic.Int32
	address := dnsfixture.Start(t, "udp", dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		queries.Add(1)
		m := new(dns.Msg).SetReply(q)
		m.Answer = []dns.RR{&dns.HTTPS{SVCB: dns.SVCB{Hdr: dns.RR_Header{Name: "secret.test.", Rrtype: dns.TypeHTTPS, Class: dns.ClassINET, Ttl: 300}, Priority: 1, Target: ".", Value: []dns.SVCBKeyValue{&dns.SVCBECHConfig{ECH: list}}}}}
		_ = w.WriteMsg(m)
	}))
	resolver := "udp://" + address
	cacheKey := ECHCacheKey(resolver, "secret.test", nil)
	t.Cleanup(func() { GlobalECHConfigCache.Delete(cacheKey) })
	config := &Config{ServerName: "secret.test", EchConfigList: resolver}
	concurrentECHRequests(t, config, "https://secret.test/", roots, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	checkECHCache(t, cacheKey)
	if got := queries.Load(); got != 1 {
		t.Fatalf("concurrent cache: got %d DNS queries, want 1", got)
	}
	cache, _ := GlobalECHConfigCache.Load(cacheKey)
	if !bytes.Equal(cache.configRecord.Load().config, list) {
		t.Fatal("cached ECH config differs from DNS record")
	}
}

func TestECHDialFail(t *testing.T) {
	address := dnsfixture.Start(t, "udp", dns.HandlerFunc(func(w dns.ResponseWriter, _ *dns.Msg) { _, _ = w.Write([]byte{0, 0, 0, 0, 0xff, 0xff}) }))
	resolver := "udp://" + address
	key := ECHCacheKey(resolver, "secret.test", nil)
	t.Cleanup(func() { GlobalECHConfigCache.Delete(key) })
	config := &Config{ServerName: "secret.test", EchConfigList: resolver, EchForceQuery: "half"}
	config.GetTLSConfig()
	cache, ok := GlobalECHConfigCache.Load(key)
	if !ok {
		t.Fatal("ECH config cache not found")
	}
	if record := cache.configRecord.Load(); record == nil || record.err == nil {
		t.Fatal("expected cached DNS error")
	}
}

func TestPublicECHDial(t *testing.T) {
	p := networktest.Enable(t)
	resolver := os.Getenv("XRAY_TEST_ECH_DNS")
	if resolver == "" {
		resolver = "https://cloudflare-dns.com/dns-query"
	}
	config := &Config{ServerName: "cloudflare.com", EchConfigList: "encryptedsni.com+" + resolver}
	key := ECHCacheKey(resolver, "encryptedsni.com", nil)
	t.Cleanup(func() { GlobalECHConfigCache.Delete(key) })
	t.Cleanup(func() {
		clientKey := ECHCacheKey(resolver, "", nil)
		if client, ok := clientForECHDOH.Load(clientKey); ok {
			client.CloseIdleConnections()
			clientForECHDOH.Delete(clientKey)
		}
	})
	concurrentECHRequests(t, config, "https://cloudflare.com/cdn-cgi/trace", nil, p.DialContext)
	checkECHCache(t, key)
}
