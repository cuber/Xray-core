package anytls_test

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
)

func TestAnyTLSDefaultFreedomPrivateProtection(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit-allow=%v", allow), func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var accepted atomic.Int64
			joined := make(chan struct{})
			go func() {
				defer close(joined)
				for {
					c, err := listener.Accept()
					if err != nil {
						return
					}
					accepted.Add(1)
					c.SetDeadline(time.Now().Add(time.Second))
					c.Write([]byte("K"))
					c.Close()
				}
			}()
			t.Cleanup(func() { listener.Close(); <-joined })
			f := newFixture(t, true, true, func(config map[string]any) {
				settings := map[string]any{}
				if allow {
					settings["finalRules"] = []any{map[string]any{"action": "allow"}}
				}
				config["outbounds"] = append(config["outbounds"].([]any), map[string]any{"tag": "private-control", "protocol": "freedom", "settings": settings})
				r := config["routing"].(map[string]any)
				r["rules"] = append([]any{map[string]any{"inboundTag": []string{"anytls-test"}, "outboundTag": "private-control"}}, r["rules"].([]any)...)
			})
			if err := f.alter("alice", "private-test-secret", false); err != nil {
				t.Fatal(err)
			}
			client := f.client(t, "private-test-secret")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := client.DialContext(ctx, M.ParseSocksaddr(listener.Addr().String()))
			if err != nil {
				t.Fatal(err)
			}
			c.SetDeadline(time.Now().Add(time.Second))
			c.Write([]byte("probe"))
			var marker [1]byte
			_, err = c.Read(marker[:])
			if allow && (err != nil || marker[0] != 'K') {
				t.Errorf("explicit allow did not reach control endpoint: %q, %v", marker, err)
			}
			if !allow && err == nil {
				t.Errorf("default private rule leaked business response: %q", marker)
			}
			c.Close()
			client.Close()
			f.instance.Close()
			want := int64(0)
			if allow {
				want = 1
			}
			if got := accepted.Load(); got != want {
				t.Fatalf("private endpoint accepts=%d want=%d", got, want)
			}
		})
	}
}
