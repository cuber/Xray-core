package stats

import (
	"testing"
	"time"
)

func TestDomainTrafficUserIsolation(t *testing.T) {
	d := newDomainTraffic(&DomainTrafficConfig{Enabled: true, MaxDomains: 2})
	now := time.Unix(100, 0)
	d.clock = func() time.Time { return now }
	d.Record("example.com", 1, 2, "alice")
	now = now.Add(time.Second)
	d.Record("example.com", 3, 4, "bob")
	now = now.Add(5 * time.Second)
	snapshot := d.Snapshot("", 0, 0)
	if len(snapshot.Buckets) != 1 || len(snapshot.Buckets[0].Entries) != 2 {
		t.Fatalf("users collapsed: %+v", snapshot)
	}
	users := map[string]uint64{}
	for _, e := range snapshot.Buckets[0].Entries {
		users[e.User] = e.UplinkBytes
	}
	if users["alice"] != 1 || users["bob"] != 3 {
		t.Fatal(users)
	}
	d.Record("other.com", 5, 6, "bob")
	now = now.Add(5 * time.Second)
	snapshot = d.Snapshot("", 0, 0)
	if snapshot.Buckets[0].OtherUplinkBytes != 1 {
		t.Fatalf("wrong LRU: %+v", snapshot)
	}
	for _, b := range snapshot.Buckets {
		for _, e := range b.Entries {
			if e.User == "alice" {
				t.Fatal("evicted user retained")
			}
		}
	}
}
