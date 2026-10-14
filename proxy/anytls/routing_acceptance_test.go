package anytls_test

import (
	"context"
	"io"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
)

func TestAnyTLSUserDomainRouting(t *testing.T) {
	f := newFixture(t, true, true, func(config map[string]any) {
		config["routing"] = map[string]any{"rules": []any{
			map[string]any{"type": "field", "inboundTag": []string{"anytls-test"}, "network": "tcp", "user": []string{"domain:landing.test"}, "outboundTag": "b"},
		}}
	})
	for _, user := range []string{"alice@landing.test", "alice@other.test"} {
		if err := f.alter(user, user+"-secret", false); err != nil {
			t.Fatal(err)
		}
		want := "Ahello"
		if user == "alice@landing.test" {
			want = "Bhello"
		}
		if got, err := exchange(f.client(t, user+"-secret"), "alpha.test", "hello"); err != nil || got != want {
			t.Fatal("user domain route mismatch", user, got, err)
		}
	}
}

func TestAnyTLSSniffedDomain(t *testing.T) {
	f := newFixture(t, true, true, func(config map[string]any) {
		config["routing"] = map[string]any{"rules": []any{
			map[string]any{"type": "field", "domain": []string{"full:sniff.test"}, "outboundTag": "b"},
		}}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := f.api.RemoveInbound(ctx, &handler.RemoveInboundRequest{Tag: "anytls-test"}); err != nil {
		t.Fatal(err)
	}
	f.inbound["sniffing"] = map[string]any{"enabled": true, "destOverride": []string{"http"}}
	if err := f.addInbound(t, f.inbound); err != nil {
		t.Fatal(err)
	}
	if err := f.alter("sniff", "sniff-secret", false); err != nil {
		t.Fatal(err)
	}
	c, err := f.client(t, "sniff-secret").DialContext(ctx, M.ParseSocksaddr("192.0.2.1:80"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	payload := "GET / HTTP/1.1\r\nHost: sniff.test\r\n\r\n"
	if _, err := io.WriteString(c, payload); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len(payload)+1)
	if _, err := io.ReadFull(c, reply); err != nil || string(reply) != "B"+payload {
		t.Fatal("sniffed route not applied", err)
	}
	if _, err := io.WriteString(c, "after-sniff"); err != nil {
		t.Fatal(err)
	}
	second := make([]byte, len("after-sniff"))
	if _, err := io.ReadFull(c, second); err != nil || string(second) != "after-sniff" {
		t.Fatal("post-sniff data mismatch", err)
	}
	time.Sleep(1100 * time.Millisecond)
	snapshot, err := f.stats.GetDomainTrafficBuckets(ctx, &stats.GetDomainTrafficBucketsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var up, down, unknownUp uint64
	for _, bucket := range snapshot.Buckets {
		for _, entry := range bucket.Entries {
			if entry.User != "sniff" {
				t.Fatal("incorrect sniffed attribution", entry.Domain, entry.User)
			}
			// Existing dispatcher accounting observes the first upload before
			// sniffing; do not invent a retroactive reassignment for AnyTLS.
			if entry.Domain == "unknown" {
				unknownUp += entry.UplinkBytes
				if entry.DownlinkBytes != 0 {
					t.Fatal("post-sniff downlink remained unknown")
				}
				continue
			}
			if entry.Domain != "sniff.test" {
				t.Fatal("unexpected domain", entry.Domain)
			}
			up += entry.UplinkBytes
			down += entry.DownlinkBytes
		}
	}
	if unknownUp != uint64(len(payload)) || up != uint64(len(second)) || down != uint64(len(payload)+1+len(second)) {
		t.Fatal("sniffed stats mismatch", up, down, unknownUp)
	}
}
