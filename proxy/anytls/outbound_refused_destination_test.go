package anytls_test

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestAnyTLSOutboundRefusedDestinationFreshRecovery(t *testing.T) {
	for _, mode := range []string{"", "proxySettings/socks", "dialerProxy/socks", "proxySettings/vless", "dialerProxy/vless", "proxySettings/anytls", "dialerProxy/anytls", "proxySettings/hysteria", "dialerProxy/hysteria"} {
		t.Run(mode, func(t *testing.T) {
			reserved, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reserved.Close() })
			address := reserved.Addr().String()
			f, _ := outboundFixture(t, mode, func(c map[string]any) {
				for _, raw := range c["outbounds"].([]any) {
					out := raw.(map[string]any)
					if out["tag"] == "a" {
						out["settings"].(map[string]any)["redirect"] = address
					}
				}
			})
			if err := reserved.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.alter("alice", "refused-secret", false); err != nil {
				t.Fatal(err)
			}
			client := f.client(t, "refused-secret")
			if got, err := exchange(client, "alpha.test", "refused-id"); err == nil {
				t.Fatalf("closed destination unexpectedly served %q", got)
			}
			listener, err := net.Listen("tcp4", address)
			if err != nil {
				t.Fatal(err)
			}
			var workers sync.WaitGroup
			var sockets sync.Map
			var mu sync.Mutex
			var receipts []string
			accepted := make(chan struct{})
			go func() {
				defer close(accepted)
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					sockets.Store(conn, struct{}{})
					workers.Add(1)
					go func() {
						defer workers.Done()
						defer sockets.Delete(conn)
						defer conn.Close()
						conn.SetDeadline(time.Now().Add(5 * time.Second))
						p := make([]byte, len("fresh-id"))
						if _, err := io.ReadFull(conn, p); err != nil {
							t.Error(err)
							return
						}
						mu.Lock()
						receipts = append(receipts, string(p))
						mu.Unlock()
						if _, err := conn.Write(append([]byte("A"), p...)); err != nil {
							t.Error(err)
							return
						}
						if extra, err := io.ReadAll(conn); err != nil || len(extra) != 0 {
							t.Errorf("extra destination bytes=%q err=%v", extra, err)
						}
					}()
				}
			}()
			var stop sync.Once
			shutdown := func(force bool) {
				stop.Do(func() {
					listener.Close()
					obCountsWait(t, accepted, "destination listener")
					closeSockets := func() { sockets.Range(func(key, _ any) bool { key.(net.Conn).Close(); return true }) }
					if force {
						closeSockets()
					}
					done := make(chan struct{})
					go func() { workers.Wait(); close(done) }()
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("destination handler survived native shutdown")
						closeSockets()
						obCountsWait(t, done, "destination handlers")
					}
				})
			}
			t.Cleanup(func() { shutdown(true) })
			if got, err := exchange(client, "alpha.test", "fresh-id"); err != nil || got != "Afresh-id" {
				t.Fatalf("first new request after refusal: %q %v", got, err)
			}
			client.Close()
			if err := f.instance.Close(); err != nil {
				t.Fatal(err)
			}
			shutdown(false)
			mu.Lock()
			defer mu.Unlock()
			if len(receipts) != 1 || receipts[0] != "fresh-id" {
				t.Fatalf("destination replay/duplicate receipts: %q", receipts)
			}
		})
	}
}
