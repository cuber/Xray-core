package command_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/stats"
	statsCmd "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestDomainTrafficRPCDisabledAndEnabledEmptyKeepOrdinaryStats(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled-empty"
		}
		t.Run(name, func(t *testing.T) {
			m, err := stats.NewManager(context.Background(), &stats.Config{DomainTraffic: &stats.DomainTrafficConfig{Enabled: enabled}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { m.Close() })
			counter, err := m.RegisterCounter("user>>>alice>>>traffic>>>uplink")
			if err != nil {
				t.Fatal(err)
			}
			counter.Add(123)
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
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c := statsCmd.NewStatsServiceClient(conn)
			domains, err := c.GetDomainTrafficBuckets(ctx, &statsCmd.GetDomainTrafficBucketsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if domains.Enabled != enabled || len(domains.Buckets) != 0 || domains.LatestSequence != 0 || domains.OldestSequence != 0 {
				t.Fatalf("empty state: %v", domains)
			}
			if enabled && domains.BootId == "" {
				t.Fatal("enabled stream missing boot identity")
			}
			ordinary, err := c.GetStats(ctx, &statsCmd.GetStatsRequest{Name: "user>>>alice>>>traffic>>>uplink"})
			if err != nil {
				t.Fatal(err)
			}
			if ordinary.Stat.Value != 123 {
				t.Fatalf("ordinary Stats = %d", ordinary.Stat.Value)
			}
		})
	}
}
