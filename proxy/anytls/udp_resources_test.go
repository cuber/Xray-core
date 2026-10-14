package anytls_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"
	stats "github.com/xtls/xray-core/app/stats/command"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
)

func openUoT(t *testing.T, c *engine.Client) *uot.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := c.DialContext(ctx, M.Socksaddr{Fqdn: uot.MagicAddress})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	req := uot.Request{Destination: M.ParseSocksaddr("127.0.0.1:53")}
	if err := uot.WriteRequest(conn, req); err != nil {
		t.Fatal(err)
	}
	return uot.NewConn(conn, req)
}

func TestAnyTLSUoTConcurrentAccounting(t *testing.T) {
	f := newFixture(t, true, true)
	var workers sync.WaitGroup
	errors := make(chan error, 2)
	for _, user := range []string{"alice", "bob"} {
		if err := f.alter(user, user+"-accounting", false); err != nil {
			t.Fatal(err)
		}
		c := openUoT(t, f.client(t, user+"-accounting"))
		workers.Add(1)
		go func(user string, c *uot.Conn) {
			defer workers.Done()
			defer c.Close()
			for range 10 {
				if err := exchangeUoT(c, "same-domain.test", []byte(user)); err != nil {
					errors <- err
					return
				}
			}
		}(user, c)
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, user := range []string{"alice", "bob"} {
		for _, dir := range []string{"uplink", "downlink"} {
			value, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>" + user + ">>>traffic>>>" + dir})
			if err != nil || value.GetStat().GetValue() != int64(len(user)*10) {
				t.Fatal("UDP framing counted or crossed users", user, dir, err)
			}
		}
	}
	time.Sleep(1100 * time.Millisecond)
	snapshot, err := f.stats.GetDomainTrafficBuckets(ctx, &stats.GetDomainTrafficBucketsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	totals := map[string][2]uint64{}
	for _, bucket := range snapshot.Buckets {
		for _, entry := range bucket.Entries {
			if entry.Domain != "same-domain.test" {
				t.Fatalf("unexpected UDP domain attribution: %s", entry.Domain)
			}
			v := totals[entry.User]
			totals[entry.User] = [2]uint64{v[0] + entry.UplinkBytes, v[1] + entry.DownlinkBytes}
		}
	}
	for _, user := range []string{"alice", "bob"} {
		n := uint64(len(user) * 10)
		if totals[user] != [2]uint64{n, n} {
			t.Fatal("UDP domain counters differ", user, totals[user])
		}
	}
}

func exchangeUoT(c *uot.Conn, target string, payload []byte) error {
	if err := c.WritePacket(B.As(payload), M.ParseSocksaddr(target+":53")); err != nil {
		return err
	}
	b := B.NewSize(8192)
	defer b.Release()
	from, err := c.ReadPacket(b)
	if err != nil {
		return err
	}
	if from != M.ParseSocksaddr(target+":53") || !bytes.Equal(b.Bytes(), payload) {
		return fmt.Errorf("UDP target/payload mismatch: %v", from)
	}
	return nil
}

func TestAnyTLSUoTResourceRounds(t *testing.T) {
	var baseline resourceSample
	for round := range 4 {
		t.Run(fmt.Sprintf("round-%d", round), func(t *testing.T) {
			f := newFixture(t, true, true, func(config map[string]any) {
				if os.Getenv("ANYTLS_TEST_DEBUG") != "" {
					config["log"] = map[string]any{"loglevel": "debug", "access": "none"}
				}
				level := config["policy"].(map[string]any)["levels"].(map[string]any)["0"].(map[string]any)
				// Explicit amd64-equivalent policy even on an arm64 test host.
				level["bufferSize"] = 512
				level["connIdle"] = 120
				// Keep the IPv4 fixture isolated from macOS dual-stack ephemeral
				// ports that can overlap other processes' IPv4-only UDP listeners.
				config["outbounds"].([]any)[2].(map[string]any)["sendThrough"] = "127.0.0.1"
			})
			clients := make([]*engine.Client, 2)
			packets := make([]*uot.Conn, 2)
			for i, user := range []string{"alice", "bob"} {
				if err := f.alter(user, user+"-uot", false); err != nil {
					t.Fatal(err)
				}
				clients[i] = f.client(t, user+"-uot")
				packets[i] = openUoT(t, clients[i])
				for target := range 64 {
					if err := exchangeUoT(packets[i], fmt.Sprintf("target-%d.test", target), []byte(user)); err != nil {
						t.Fatalf("user=%s target=%d: %v", user, target, err)
					}
				}
			}
			t.Logf("128 real UoT destinations: %+v", sampleResources(t))
			overflow := openUoT(t, clients[1])
			if err := exchangeUoT(overflow, "overflow.test", []byte("overflow")); err == nil {
				t.Fatal("shared target admission limit bypassed")
			}
			overflow.Close()
			if got, err := exchange(clients[1], "alpha.test", "tcp"); err != nil || got != "Btcp" {
				t.Fatal("UDP saturation broke unrelated TCP", got, err)
			}
			if err := f.alter("alice", "", true); err != nil {
				t.Fatal(err)
			}
			if err := exchangeUoT(packets[0], "target-0.test", []byte("revoked")); err == nil {
				t.Fatal("revoked UoT association remained usable")
			}
			if err := exchangeUoT(packets[1], "target-0.test", []byte("bob-survives")); err != nil {
				t.Fatal("revocation broke another user", err)
			}
			// Release is asynchronous; wait on observable successful admission,
			// not a sleep that assumes the workers have already exited.
			deadline := time.Now().Add(3 * time.Second)
			var recovered *uot.Conn
			for {
				recovered = openUoT(t, clients[1])
				if err := exchangeUoT(recovered, "recovered.test", []byte("bob")); err == nil {
					break
				}
				recovered.Close()
				if time.Now().After(deadline) {
					t.Fatal("revocation did not restore admission within three seconds")
				}
				time.Sleep(10 * time.Millisecond)
			}
			for target := range 63 {
				if err := exchangeUoT(recovered, fmt.Sprintf("new-%d.test", target), []byte("bob")); err != nil {
					t.Fatalf("recovered target=%d failed: %v", target, err)
				}
			}
			if err := f.alter("bob", "", true); err != nil {
				t.Fatal(err)
			}
		})
		deadline := time.Now().Add(5 * time.Second)
		var after resourceSample
		for {
			after = sampleResources(t)
			if round == 0 || (after.fd <= baseline.fd+2 && after.goroutines <= baseline.goroutines+2) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("UoT cleanup did not recover: baseline=%+v after=%+v", baseline, after)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Logf("round %d after cleanup: %+v", round, after)
		if round == 0 {
			baseline = after
		} else if after.heap > baseline.heap+4*1024*1024 {
			t.Fatalf("UoT retained heap grew: baseline=%+v after=%+v", baseline, after)
		}
	}
}

func TestAnyTLSUoTSlowConsumerRevocation(t *testing.T) {
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { udp.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		payload := make([]byte, 8192)
		for {
			_, addr, err := udp.ReadFrom(payload)
			if err != nil {
				return
			}
			for range 64 {
				if _, err := udp.WriteTo(payload, addr); err != nil {
					return
				}
			}
		}
	}()
	f := newFixture(t, true, true, func(config map[string]any) {
		level := config["policy"].(map[string]any)["levels"].(map[string]any)["0"].(map[string]any)
		level["bufferSize"] = -1
		config["outbounds"].([]any)[2].(map[string]any)["sendThrough"] = "127.0.0.1"
		config["outbounds"].([]any)[2].(map[string]any)["settings"].(map[string]any)["redirect"] = udp.LocalAddr().String()
	})
	if err := f.alter("alice", "slow-uot", false); err != nil {
		t.Fatal(err)
	}
	c := openUoT(t, f.client(t, "slow-uot"))
	for target := range 64 {
		if err := c.WritePacket(B.As([]byte("flood")), M.Socksaddr{Fqdn: fmt.Sprintf("slow-%d.test", target), Port: 53}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	t.Logf("unread UoT response burst: %+v", sampleResources(t))
	if err := f.alter("alice", "", true); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	b := B.NewSize(8192)
	defer b.Release()
	for {
		b.Reset()
		if _, err := c.ReadPacket(b); err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatal("revoked slow consumer did not close")
			}
			break
		}
	}
	udp.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("burst fixture leaked")
	}
}
