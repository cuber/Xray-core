package anytls_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/app/observatory/burst"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/outbound"
	"google.golang.org/protobuf/proto"
)

func TestAnyTLSOutboundScheduledBalancerUDSIsolation(t *testing.T) {
	for _, entrance := range []string{"proxySettings", "dialerProxy"} {
		t.Run(entrance, func(t *testing.T) {
			var stalled atomic.Bool
			var active atomic.Int64
			entered, exited := make(chan struct{}, 8), make(chan struct{}, 8)
			dir, err := os.MkdirTemp("/tmp", "ob-compose-")
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
				active.Add(1)
				defer active.Add(-1)
				switch r.URL.RequestURI() {
				case "/generate_204?ob=good":
					w.WriteHeader(204)
				case "/generate_204?ob=bad":
					if stalled.Load() {
						entered <- struct{}{}
						<-r.Context().Done()
						exited <- struct{}{}
						return
					}
					w.WriteHeader(503)
				default:
					t.Errorf("unexpected remote UDS URI: %s", r.URL.RequestURI())
					w.WriteHeader(400)
				}
			})}
			served := make(chan struct{})
			go func() { defer close(served); server.Serve(listener) }()
			t.Cleanup(func() { server.Close(); obCountsWait(t, served, "UDS server") })
			f, config := outboundFixture(t, entrance+"/anytls", func(c map[string]any) {
				groups := []any{}
				for _, group := range []string{"good", "bad"} {
					groups = append(groups, map[string]any{"subjectSelector": []string{"measured-" + group}, "pingConfig": map[string]any{
						"destination": "http://beta.test:8080/generate_204?ob=" + group,
						"interval":    "3s", "timeout": "10s", "sampling": 20, "keepAlive": true,
					}})
				}
				c["burstObservatory"] = map[string]any{"pingGroups": groups}
				r := c["routing"].(map[string]any)
				r["balancers"] = []any{map[string]any{"tag": "observed", "selector": []string{"measured-"}, "strategy": map[string]any{"type": "leastPing"}}}
				rule := r["rules"].([]any)[0].(map[string]any)
				delete(rule, "outboundTag")
				rule["balancerTag"] = "observed"
				for _, raw := range c["outbounds"].([]any) {
					out := raw.(map[string]any)
					if out["tag"] == "b" {
						out["settings"].(map[string]any)["redirect"] = "unix:" + path
					}
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			observer := f.instance.GetFeature(extension.ObservatoryType()).(*burst.Observer)
			if err := observer.Close(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { observer.Close() })
			manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
			aliases := map[string]*chainCountsAlias{}
			for _, group := range []string{"good", "bad"} {
				out := proto.Clone(config).(*core.OutboundHandlerConfig)
				out.Tag = "path-" + group
				if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: out}); err != nil {
					t.Fatal(err)
				}
				a := &chainCountsAlias{Handler: manager.GetHandler(out.Tag), tag: "measured-" + group, done: make(chan struct{}, 64)}
				if err := manager.AddHandler(ctx, a); err != nil {
					t.Fatal(err)
				}
				aliases[group] = a
			}
			observer.Check([]string{"measured-good", "measured-bad"})
			obCountsWait(t, aliases["good"].done, "initial healthy probe")
			obCountsWait(t, aliases["bad"].done, "initial failed probe")
			assertHealth := func() {
				t.Helper()
				value, err := observer.GetObservation(ctx)
				if err != nil {
					t.Fatal(err)
				}
				rows := value.(*observatory.ObservationResult).Status
				if len(rows) != 2 {
					t.Fatalf("observations=%v", rows)
				}
				for _, row := range rows {
					if row.Alive != (row.OutboundTag == "measured-good") {
						t.Fatalf("independent group health: %v", row)
					}
					if row.OutboundTag == "measured-bad" && (row.HealthPing.All != 1 || row.HealthPing.Fail != 1) {
						t.Fatalf("canceled scheduled probe published a late sample: %v", row)
					}
				}
			}
			assertHealth()
			flows := []net.Conn{}
			users := []string{"alice", "carol"}
			up, down := make([]int64, 2), make([]int64, 2)
			for _, user := range users {
				if err := f.alter(user, user+"-secret", false); err != nil {
					t.Fatal(err)
				}
				conn, err := f.client(t, user+"-secret").DialContext(ctx, M.Socksaddr{Fqdn: "alpha.test", Port: 80})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { conn.Close() })
				flows = append(flows, conn)
			}
			round := func(phase string, first bool) {
				t.Helper()
				for i, conn := range flows {
					payload := fmt.Sprintf("%s/%s", users[i], phase)
					want := payload
					if first {
						want = "A" + want
					}
					conn.SetDeadline(time.Now().Add(3 * time.Second))
					if _, err := io.WriteString(conn, payload); err != nil {
						t.Fatal(err)
					}
					got := make([]byte, len(want))
					if _, err := io.ReadFull(conn, got); err != nil || string(got) != want {
						t.Fatalf("business %s: got=%q want=%q err=%v", phase, got, want, err)
					}
					up[i] += int64(len(payload))
					down[i] += int64(len(want))
				}
			}
			round("before", true)
			if aliases["good"].opened.Load() != 3 || aliases["bad"].opened.Load() != 1 {
				t.Fatal("real inbound routing did not choose the healthy balancer candidate")
			}
			stalled.Store(true)
			if err := observer.Start(); err != nil {
				t.Fatal(err)
			}
			obCountsWait(t, entered, "scheduled remote UDS stall")
			round("during-probe", false)
			started := time.Now()
			if err := observer.Close(); err != nil {
				t.Fatal(err)
			}
			if time.Since(started) >= 2*time.Second {
				t.Fatal("observer stop waited for the ten-second probe timeout")
			}
			obCountsWait(t, exited, "canceled remote UDS handler")
			assertHealth()
			round("after-stop", false)
			for i, user := range users {
				for direction, want := range map[string]int64{"uplink": up[i], "downlink": down[i]} {
					result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>" + user + ">>>traffic>>>" + direction})
					if err != nil || result.GetStat().GetValue() != want {
						t.Fatalf("%s/%s got=%v want=%d err=%v", user, direction, result, want, err)
					}
				}
			}
			closed := make(chan struct{})
			go func() {
				defer close(closed)
				if err := f.instance.Close(); err != nil {
					t.Error(err)
				}
			}()
			obCountsWait(t, closed, "instance shutdown with business still open")
			for _, alias := range aliases {
				for range alias.opened.Load() {
					// Initial checks were already joined above.
					if alias.closed.Load() == alias.opened.Load() {
						break
					}
					obCountsWait(t, alias.done, "remaining native dispatch")
				}
				if alias.closed.Load() != alias.opened.Load() {
					t.Fatal("dispatch survived instance shutdown")
				}
			}
			if err := server.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			obCountsWait(t, served, "UDS Serve shutdown")
			if active.Load() != 0 {
				t.Fatal("remote HTTP handler survived shutdown")
			}
		})
	}
}
