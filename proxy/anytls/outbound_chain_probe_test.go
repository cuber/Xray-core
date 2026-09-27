package anytls_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/app/observatory/burst"
	stats "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
)

// Exercise business isolation and named probes through a remote UDS for every
// supported outer protocol; direct dial/TLS/auth stalls have separate tests.
func TestAnyTLSOutboundChainProbeTimeoutIsolation(t *testing.T) {
	for _, protocol := range []string{"socks", "vless", "hysteria", "anytls"} {
		for _, selector := range []string{"proxySettings", "dialerProxy"} {
			t.Run(selector+"/"+protocol, func(t *testing.T) {
				entered := make(chan struct{}, 1)
				httpDone := make(chan struct{}, 8)
				var requests, active atomic.Int64
				var stall atomic.Bool
				stall.Store(true)
				dir, err := os.MkdirTemp("/tmp", "chain-probe-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(dir) })
				path := filepath.Join(dir, "http.sock")
				listener, err := net.Listen("unix", path)
				if err != nil {
					t.Fatal(err)
				}
				server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					active.Add(1)
					defer func() { active.Add(-1); httpDone <- struct{}{} }()
					uri := r.URL.RequestURI()
					if r.Method != http.MethodGet || (uri != "/generate_204?ob=healthy" && uri != "/generate_204?ob=failure") {
						t.Errorf("unexpected chained probe: %s %s", r.Method, uri)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if uri == "/generate_204?ob=failure" && stall.Load() {
						entered <- struct{}{}
						<-r.Context().Done()
						return
					}
					w.WriteHeader(http.StatusNoContent)
				})}
				served := make(chan struct{})
				go func() { defer close(served); server.Serve(listener) }()
				t.Cleanup(func() { server.Close(); obCountsWait(t, served, "UDS HTTP Serve") })
				f, _ := outboundFixture(t, selector+"/"+protocol, func(config map[string]any) {
					for _, raw := range config["outbounds"].([]any) {
						out := raw.(map[string]any)
						if out["tag"] == "b" {
							out["settings"].(map[string]any)["redirect"] = "unix:" + path
						}
					}
				})
				ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), core.XrayKey(1), f.instance), 30*time.Second)
				defer cancel()
				manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
				// Reuse the completion-only alias, not a substituted tagged dialer.
				// Probe and both authenticated users share the same native client pool.
				observed := &obCountsOutbound{Handler: manager.GetHandler("client"), done: make(chan struct{}, 8)}
				if err := manager.AddHandler(ctx, observed); err != nil {
					t.Fatal(err)
				}
				probeCtx, stopObserver := context.WithCancel(ctx)
				defer stopObserver()
				newProbe := func(group string) *burst.HealthPing {
					return burst.NewHealthPing(probeCtx, f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher), &burst.HealthPingConfig{
						Destination: "http://beta.test:8080/generate_204?ob=" + group,
						HttpMethod:  http.MethodGet, KeepAlive: true,
						// Keep the healthy control sample valid across the timeout and joins.
						Timeout: int64(2 * time.Second), SamplingCount: 1, Interval: int64(10 * time.Second),
					})
				}
				healthy, failing := newProbe("healthy"), newProbe("failure")
				assertHealth := func(ping *burst.HealthPing, want bool) {
					t.Helper()
					result := ping.Results[observed.Tag()]
					if result == nil || result.Get().Alive != want {
						t.Fatalf("probe health want alive=%v, result=%v", want, result)
					}
				}
				joinProbe := func() {
					obCountsWait(t, observed.done, "chained native probe dispatch")
					obCountsWait(t, httpDone, "remote HTTP handler")
				}
				type businessFlow struct {
					user     string
					client   *engine.Client
					conn     net.Conn
					up, down int64
					first    bool
				}
				flows := make([]*businessFlow, 0, 2)
				for _, user := range []string{"alice", "bob"} {
					if err := f.alter(user, user+"-secret", false); err != nil {
						t.Fatal(err)
					}
					client := f.client(t, user+"-secret")
					conn, err := client.DialContext(ctx, M.Socksaddr{Fqdn: "alpha.test", Port: 80})
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					flows = append(flows, &businessFlow{user: user, client: client, conn: conn, first: true})
				}
				round := func(phase string) {
					t.Helper()
					for _, flow := range flows {
						payload := flow.user + ":" + phase
						want := payload
						if flow.first {
							want = "A" + payload
							flow.first = false
						}
						flow.conn.SetDeadline(time.Now().Add(3 * time.Second))
						if _, err := io.WriteString(flow.conn, payload); err != nil {
							t.Fatalf("%s/%s write: %v", flow.user, phase, err)
						}
						got := make([]byte, len(want))
						if _, err := io.ReadFull(flow.conn, got); err != nil || string(got) != want {
							t.Fatalf("%s/%s response=%q want=%q: %v", flow.user, phase, got, want, err)
						}
						flow.up += int64(len(payload))
						flow.down += int64(len(want))
					}
				}
				assertUsers := func() {
					t.Helper()
					for _, flow := range flows {
						for direction, want := range map[string]int64{"uplink": flow.up, "downlink": flow.down} {
							result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>" + flow.user + ">>>traffic>>>" + direction})
							if err != nil || result.GetStat().GetValue() != want {
								t.Fatalf("%s %s accounting=%v want=%d: %v", flow.user, direction, result, want, err)
							}
						}
					}
				}
				round("before")
				if err := healthy.Check([]string{observed.Tag()}); err != nil {
					t.Fatal(err)
				}
				joinProbe()
				assertHealth(healthy, true)
				assertUsers()

				checkDone := make(chan struct{})
				var checkErr error
				started := time.Now()
				go func() {
					defer close(checkDone)
					checkErr = failing.Check([]string{observed.Tag()})
				}()
				defer func() { stopObserver(); obCountsWait(t, checkDone, "owned health-check worker") }()
				obCountsWait(t, entered, "chained HTTP response stall")
				round("during-stall")
				if err := healthy.Check([]string{observed.Tag()}); err != nil {
					t.Fatal(err)
				}
				assertHealth(healthy, true)
				select {
				case <-checkDone:
					t.Fatal("failed to overlap live business and healthy probe with stalled HTTP response")
				default:
				}
				obCountsWait(t, checkDone, "HTTP timeout result")
				if checkErr != nil || time.Since(started) > 5*time.Second {
					t.Fatalf("unbounded/failed check: elapsed=%s err=%v", time.Since(started), checkErr)
				}
				assertHealth(failing, false)
				assertHealth(healthy, true)
				// Join both concurrent probes, including the remote canceled handler.
				// A dead health sample alone is not evidence of underlying cleanup.
				joinProbe()
				joinProbe()
				if active.Load() != 0 {
					t.Fatal("HTTP timeout left a remote handler active")
				}
				round("after-timeout")
				assertUsers()
				stall.Store(false)
				if err := failing.Check([]string{observed.Tag()}); err != nil {
					t.Fatal(err)
				}
				joinProbe()
				assertHealth(failing, true)
				assertHealth(healthy, true)
				stopObserver()
				round("after-observer-cancel")
				for _, flow := range flows {
					payload := flow.user + ":fresh-stream"
					if got, err := exchange(flow.client, "alpha.test", payload); err != nil || got != "A"+payload {
						t.Fatalf("%s fresh business after cancellation: %q %v", flow.user, got, err)
					}
					flow.up += int64(len(payload))
					flow.down += int64(len(payload) + 1)
				}
				assertUsers()
				for _, flow := range flows {
					flow.conn.Close()
					flow.client.Close()
				}
				closed := make(chan struct{})
				go func() {
					defer close(closed)
					if err := f.instance.Close(); err != nil {
						t.Error(err)
					}
				}()
				obCountsWait(t, closed, "chained Core shutdown")
				server.Close()
				if active.Load() != 0 || requests.Load() != 4 {
					t.Fatalf("remote HTTP active=%d requests=%d, want 0/4 (no replay)", active.Load(), requests.Load())
				}
			})
		}
	}
}
