package dispatcher

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	coreStats "github.com/xtls/xray-core/app/stats"
	statsCmd "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestAtomicStatsDispatcherRPCExactTotals(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			m, err := coreStats.NewManager(context.Background(), &coreStats.Config{DomainTraffic: &coreStats.DomainTrafficConfig{Enabled: enabled, BucketIntervalSeconds: 1}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { m.Close() })
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			s := grpc.NewServer()
			statsCmd.RegisterStatsServiceServer(s, statsCmd.NewStatsServer(m))
			done := make(chan struct{})
			go func() { defer close(done); s.Serve(l) }()
			t.Cleanup(func() { s.Stop(); <-done })
			conn, err := grpc.NewClient(l.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			client := statsCmd.NewStatsServiceClient(conn)
			const workers = 128
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					ctx := session.ContextWithInbound(context.Background(), &session.Inbound{User: &protocol.MemoryUser{Email: "alice"}})
					ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: xnet.TCPDestination(xnet.DomainAddress("example.test"), 443)}})
					link := WrapLink(ctx, trafficContractPolicy{}, m, &transport.Link{Reader: &trafficContractReader{payload: make([]byte, 100), err: io.EOF}, Writer: buf.Discard})
					cache := &cachedReader{reader: link.Reader.(buf.TimeoutReader)}
					defer cache.Interrupt()
					b := buf.New()
					defer b.Release()
					if err := cache.Cache(b, time.Second); err != io.EOF {
						t.Errorf("sniff error = %v", err)
					}
					mb, err := cache.ReadMultiBuffer()
					if err != nil || mb.Len() != 100 {
						t.Errorf("replay payload = %d, %v", mb.Len(), err)
					}
					buf.ReleaseMulti(mb)
					if err := link.Writer.WriteMultiBuffer(buf.MergeBytes(nil, make([]byte, 200))); err != nil {
						t.Error(err)
					}
				}()
			}
			close(start)
			wg.Wait()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for direction, want := range map[string]int64{"uplink": workers * 100, "downlink": workers * 200} {
				got, err := client.GetStats(ctx, &statsCmd.GetStatsRequest{Name: "user>>>alice>>>traffic>>>" + direction})
				if err != nil {
					t.Fatal(err)
				}
				if got.Stat.Value != want {
					t.Fatalf("%s = %d, want %d", direction, got.Stat.Value, want)
				}
			}
			// Close the last real-time bucket; earlier buckets are included too.
			if enabled {
				time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second)) + 10*time.Millisecond)
			}
			got, err := client.GetDomainTrafficBuckets(ctx, &statsCmd.GetDomainTrafficBucketsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			var up, down uint64
			var cursor uint64
			for _, b := range got.Buckets {
				if b.Sequence <= cursor || b.BootId != got.BootId {
					t.Fatal("invalid stream identity/sequence")
				}
				cursor = b.Sequence
				for _, e := range b.Entries {
					if e.User != "alice" || e.Domain != "example.test" {
						t.Fatalf("attribution: %v", e)
					}
					up += e.UplinkBytes
					down += e.DownlinkBytes
				}
			}
			if got.Enabled != enabled {
				t.Fatal("enablement changed")
			}
			if enabled && (up != workers*100 || down != workers*200) {
				t.Fatalf("domain total = %d/%d", up, down)
			}
			if !enabled && len(got.Buckets) != 0 {
				t.Fatal("disabled buckets")
			}
			repeat, err := client.GetDomainTrafficBuckets(ctx, &statsCmd.GetDomainTrafficBucketsRequest{AfterBootId: got.BootId, AfterSequence: cursor})
			if err != nil {
				t.Fatal(err)
			}
			if len(repeat.Buckets) != 0 || repeat.HasGap {
				t.Fatal("cursor replayed bytes or reported gap")
			}
		})
	}
}
