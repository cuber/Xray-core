package anytls_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	handler "github.com/xtls/xray-core/app/proxyman/command"
)

type resourceSample struct {
	heap       uint64
	fd         int
	goroutines int
}

func sampleResources(t *testing.T) resourceSample {
	t.Helper()
	// Two collections also retire sync.Pool's victim cache, so comparisons
	// measure retained objects rather than the previous round's buffer pool.
	runtime.GC()
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return resourceSample{memory.HeapAlloc, openFDCount(t), runtime.NumGoroutine()}
}

func TestAnyTLSResourceRounds(t *testing.T) {
	var baseline resourceSample
	// Round zero warms the transport, gRPC and crypto pools before comparison.
	for round := range 4 {
		t.Run(fmt.Sprintf("round-%d", round), func(t *testing.T) {
			f := newFixture(t, true, true)
			var flows []net.Conn
			t.Cleanup(func() {
				for _, conn := range flows {
					conn.Close()
				}
			})
			for _, user := range []string{"alice", "bob"} {
				if err := f.alter(user, user+"-resource-test", false); err != nil {
					t.Fatal(err)
				}
				client := f.client(t, user+"-resource-test")
				for range 50 {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					conn, err := client.DialContext(ctx, M.Socksaddr{Fqdn: "alpha.test", Port: 80})
					cancel()
					if err != nil {
						t.Fatal(err)
					}
					flows = append(flows, conn)
					conn.SetDeadline(time.Now().Add(5 * time.Second))
					if _, err := conn.Write([]byte("load")); err != nil {
						t.Fatal(err)
					}
					var reply [5]byte
					if _, err := io.ReadFull(conn, reply[:]); err != nil {
						t.Fatal(err)
					}
					want := "Aload"
					if user == "bob" {
						want = "Bload"
					}
					if string(reply[:]) != want {
						t.Fatal("resource workload crossed user routes")
					}
				}
			}
			t.Logf("100 live streams: %+v", sampleResources(t))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := f.api.RemoveInbound(ctx, &handler.RemoveInboundRequest{Tag: "anytls-test"}); err != nil {
				t.Fatal("listener did not drain within five seconds", err)
			}
		})
		deadline := time.Now().Add(5 * time.Second)
		var after resourceSample
		for {
			after = sampleResources(t)
			var stacks bytes.Buffer
			if err := pprof.Lookup("goroutine").WriteTo(&stacks, 2); err != nil {
				t.Fatal(err)
			}
			// Test functions themselves live in anytls_test, not these packages.
			active := bytes.Contains(stacks.Bytes(), []byte("/proxy/anytls.(*")) ||
				bytes.Contains(stacks.Bytes(), []byte("/proxy/anytls/internal/engine.(*"))
			// Closed transports and stopped timer callbacks can remain reachable
			// briefly after their goroutines exit. Apply the same bounded drain
			// period to heap reclamation, without relaxing the retained-heap cap.
			if !active && (round == 0 || (after.fd <= baseline.fd+2 && after.goroutines <= baseline.goroutines+2 && after.heap <= baseline.heap+4*1024*1024)) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("round %d did not drain: baseline=%+v after=%+v\n%s", round, baseline, after, stacks.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Logf("round %d after cleanup: %+v", round, after)
		if round == 0 {
			baseline = after
		} else if after.heap > baseline.heap+4*1024*1024 {
			t.Fatalf("retained heap exceeded warm baseline by 4 MiB: %+v -> %+v", baseline, after)
		}
	}
}
