package anytls_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory/burst"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/infra/conf"
)

func TestAnyTLSOutboundObservatoryUDS(t *testing.T) {
	testAnyTLSOutboundObservatoryUDS(t, "http://must-not-resolve.invalid:8080")
}

// Run only in an isolated network namespace: the workstation owns port 8080.
func TestAnyTLSOutboundProbeLoopbackTrap(t *testing.T) {
	if os.Getenv("ANYTLS_LOOPBACK_TRAP") != "1" {
		t.Skip("set ANYTLS_LOOPBACK_TRAP=1 inside an isolated network namespace")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})}
	joined := make(chan struct{})
	go func() { defer close(joined); server.Serve(listener) }()
	t.Cleanup(func() { server.Close(); <-joined })
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
	t.Cleanup(client.CloseIdleConnections)
	response, err := client.Get("http://127.0.0.1:8080/control")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable || hits.Load() != 1 {
		t.Fatal("local trap control failed")
	}
	testAnyTLSOutboundObservatoryUDS(t, "http://127.0.0.1:8080")
	if hits.Load() != 1 {
		t.Fatalf("native outbound bypassed remote UDS: local trap hits=%d", hits.Load())
	}
}

func testAnyTLSOutboundObservatoryUDS(t *testing.T, root string) {
	t.Helper()
	if os.PathSeparator != '/' {
		t.Skip("Unix socket fixture")
	}
	f, _ := outboundFixture(t, "")
	// A deliberately nonexistent probe hostname proves the request traverses
	// the outbound and remote UDS redirect, not a local/direct HTTP shortcut.
	dir, err := os.MkdirTemp("/tmp", "anytls-ob-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "probe.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	var status atomic.Int32
	var redirected atomic.Int32
	status.Store(204)
	seen := make(chan string, 32)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-target" {
			redirected.Add(1)
			w.WriteHeader(204)
			return
		}
		seen <- r.Method + " " + r.URL.RequestURI()
		if status.Load() == 408 {
			<-r.Context().Done()
			return
		}
		if status.Load() == 302 {
			w.Header().Set("Location", "/redirect-target")
		}
		w.WriteHeader(int(status.Load()))
	})}
	served := make(chan struct{})
	go func() { defer close(served); server.Serve(listener) }()
	t.Cleanup(func() { server.Close(); <-served })
	raw, _ := json.Marshal(map[string]any{"tag": "a", "protocol": "freedom", "settings": map[string]any{"redirect": "unix:" + path, "ipsBlocked": []string{}}})
	var config conf.OutboundDetourConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	built, err := config.Build()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), core.XrayKey(1), f.instance))
	defer cancel()
	if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: built}); err != nil {
		t.Fatal(err)
	}
	dispatcher := f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher)
	for _, method := range []string{"GET", "HEAD"} {
		for _, keep := range []bool{false, true} {
			for _, samples := range []int32{1, 2, 3, 20} {
				h := burst.NewHealthPing(ctx, dispatcher, &burst.HealthPingConfig{
					Destination: root + "/generate_204?ob=remote&keepalive=1",
					HttpMethod:  method, KeepAlive: keep, Timeout: int64(200 * time.Millisecond), SamplingCount: samples, Interval: int64(time.Second),
				})
				successes := int32(0)
				failed := false
				for i, code := range []int32{204, 503, 204, 204, 204, 200, 302, 404, 408, 204, 204, 204} {
					status.Store(code)
					started := time.Now()
					if err := h.Check([]string{"client"}); err != nil {
						t.Fatal(err)
					}
					if time.Since(started) > time.Second {
						t.Fatal("probe exceeded bounded timeout")
					}
					if code == 204 {
						successes++
					} else {
						successes = 0
						failed = true
					}
					want := code == 204 && (!failed || successes >= min(samples, 3))
					if got := h.Results["client"].Get().Alive; got != want {
						t.Fatalf("%s keep=%v window=%d sample=%d code=%d alive=%v", method, keep, samples, i, code, got)
					}
					select {
					case got := <-seen:
						if got != fmt.Sprintf("%s /generate_204?ob=remote&keepalive=1", method) {
							t.Fatal("probe query changed", got)
						}
					case <-time.After(time.Second):
						t.Fatal("remote UDS did not receive probe")
					}
				}
			}
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("probe followed redirect to a healthy target", redirected.Load())
	}
}
