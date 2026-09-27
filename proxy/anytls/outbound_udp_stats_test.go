package anytls_test

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing/common/uot"
	stats "github.com/xtls/xray-core/app/stats/command"
)

func TestAnyTLSOutboundUDPUserDomainAccounting(t *testing.T) {
	for _, mode := range []string{"", "proxySettings/socks", "dialerProxy/socks", "proxySettings/vless", "dialerProxy/vless", "proxySettings/anytls", "dialerProxy/anytls", "proxySettings/hysteria", "dialerProxy/hysteria"} {
		t.Run("chain="+mode, func(t *testing.T) {
			f, _ := outboundFixture(t, mode, func(config map[string]any) {
				for _, raw := range config["outbounds"].([]any) {
					out := raw.(map[string]any)
					if out["tag"] == "udp" {
						out["sendThrough"] = "127.0.0.1"
					}
				}
			})
			type flow struct {
				user    string
				packets *uot.Conn
				sizes   [2]int
			}
			var flows []flow
			want := map[string][2]uint64{}
			users := map[string]int64{}
			for i, user := range []string{"alice", "bob"} {
				if err := f.alter(user, user+"-udp-stat", false); err != nil {
					t.Fatal(err)
				}
				sizes := [][2]int{{1, 8192}, {37, 512}}[i]
				flows = append(flows, flow{user, openUoT(t, f.client(t, user+"-udp-stat")), sizes})
				for j, domain := range []string{"alpha.test", "beta.test"} {
					n := uint64(sizes[j] * 3)
					want[user+"/"+domain] = [2]uint64{n, n}
					remote := want["remote/"+domain]
					want["remote/"+domain] = [2]uint64{remote[0] + n, remote[1] + n}
					users[user] += int64(n)
					users["remote"] += int64(n)
				}
			}
			var workers sync.WaitGroup
			errors := make(chan error, len(flows))
			start := make(chan struct{})
			for _, f := range flows {
				workers.Add(1)
				go func() {
					defer workers.Done()
					defer f.packets.Close()
					<-start
					for round := range 3 {
						for j, domain := range []string{"alpha.test", "beta.test"} {
							payload := bytes.Repeat([]byte(f.user+fmt.Sprint(round)), f.sizes[j])[:f.sizes[j]]
							if err := exchangeUoT(f.packets, domain, payload); err != nil {
								errors <- fmt.Errorf("%s/%s: %w", f.user, domain, err)
								return
							}
						}
					}
				}()
			}
			close(start)
			workers.Wait()
			close(errors)
			for err := range errors {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for user, n := range users {
				for _, direction := range []string{"uplink", "downlink"} {
					result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>" + user + ">>>traffic>>>" + direction})
					if err != nil || result.GetStat().GetValue() != n {
						t.Fatalf("logical UDP %s/%s got=%v want=%d err=%v", user, direction, result, n, err)
					}
				}
			}
			// Domain snapshots contain only closed buckets. Wait for publication,
			// not an arbitrary fixed sleep; every read aggregates the same history.
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				result, err := f.stats.GetDomainTrafficBuckets(ctx, &stats.GetDomainTrafficBucketsRequest{})
				if err != nil {
					t.Fatal(err)
				}
				got := map[string][2]uint64{}
				for _, bucket := range result.Buckets {
					for _, entry := range bucket.Entries {
						if _, local := users[entry.User]; !local {
							continue
						}
						key := entry.User + "/" + entry.Domain
						v := got[key]
						got[key] = [2]uint64{v[0] + entry.UplinkBytes, v[1] + entry.DownlinkBytes}
					}
				}
				if reflect.DeepEqual(got, want) {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatalf("UDP domain bytes got=%v want=%v", got, want)
				case <-tick.C:
				}
			}
		})
	}
}
