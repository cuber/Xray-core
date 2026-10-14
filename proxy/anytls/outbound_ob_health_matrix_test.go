package anytls_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/app/observatory/burst"
	obsCmd "github.com/xtls/xray-core/app/observatory/command"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

func TestAnyTLSOutboundHealthThroughControlAPI(t *testing.T) {
	for _, window := range []int{1, 2, 20} {
		for _, firstFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/initialFailure=%v", window, firstFails), func(t *testing.T) {
				var status atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(int(status.Load())) }))
				t.Cleanup(server.Close)
				var address string
				f, config := outboundFixture(t, "", func(c map[string]any) {
					api := c["api"].(map[string]any)
					address = api["listen"].(string)
					api["services"] = []string{"HandlerService", "StatsService", "ObservatoryService"}
					c["burstObservatory"] = map[string]any{"pingGroups": []any{map[string]any{
						"subjectSelector": []string{"health-client"}, "pingConfig": map[string]any{
							"destination": "http://health.invalid:8080/generate_204", "interval": "50ms", "timeout": "1s", "sampling": window,
						},
					}}}
					for _, value := range c["outbounds"].([]any) {
						out := value.(map[string]any)
						if out["tag"] == "a" {
							out["settings"].(map[string]any)["redirect"] = server.Listener.Addr().String()
						}
					}
				})
				observer := f.instance.GetFeature(extension.ObservatoryType()).(*burst.Observer)
				if err := observer.Close(); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				out := proto.Clone(config).(*core.OutboundHandlerConfig)
				out.Tag = "health-client"
				if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: out}); err != nil {
					t.Fatal(err)
				}
				conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				client := obsCmd.NewObservatoryServiceClient(conn)
				read := func() *observatory.OutboundStatus {
					t.Helper()
					response, err := client.GetOutboundStatus(ctx, &obsCmd.GetOutboundStatusRequest{})
					if err != nil {
						t.Fatal(err)
					}
					rows := response.GetStatus().GetStatus()
					if len(rows) == 0 {
						return nil
					}
					if len(rows) != 1 || rows[0].OutboundTag != out.Tag {
						t.Fatalf("unexpected observations: %v", rows)
					}
					return rows[0]
				}
				if read() != nil {
					t.Fatal("empty history fabricated an observation")
				}
				failed, streak := false, 0
				codes := []int32{204, 503, 204, 503, 204, 204, 204}
				if firstFails {
					codes = append([]int32{503}, codes...)
				}
				for step, code := range codes {
					status.Store(code)
					observer.Check([]string{out.Tag})
					if code != 204 {
						failed = true
						streak = 0
					} else {
						streak++
					}
					want := code == 204 && (!failed || streak >= min(3, window))
					row := read()
					if row == nil || row.Alive != want {
						t.Fatalf("step=%d code=%d alive=%v want=%v", step, code, row, want)
					}
				}
				deadline := time.NewTimer(5 * time.Second)
				defer deadline.Stop()
				tick := time.NewTicker(10 * time.Millisecond)
				defer tick.Stop()
				for {
					row := read()
					if row != nil && row.GetHealthPing().GetAll() == 0 {
						if row.Alive {
							t.Fatal("stale history remained alive")
						}
						break
					}
					select {
					case <-tick.C:
					case <-deadline.C:
						t.Fatal("history did not expire")
					}
				}
			})
		}
	}
}
