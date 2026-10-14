package stats

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

func domainTrafficTotals(s DomainTrafficSnapshot) (up, down uint64) {
	for _, b := range s.Buckets {
		up = saturatingAdd(up, saturatingAdd(b.OtherUplinkBytes, b.UnknownUplinkBytes))
		down = saturatingAdd(down, saturatingAdd(b.OtherDownlinkBytes, b.UnknownDownlinkBytes))
		for _, e := range b.Entries {
			up = saturatingAdd(up, e.UplinkBytes)
			down = saturatingAdd(down, e.DownlinkBytes)
		}
	}
	return
}

func TestDomainTrafficLRUDiscardsUncollectedHistory(t *testing.T) {
	d, now := newTestDomainTraffic(t, 1)
	d.Record("a.test", 100, 200, "alice")
	*now = time.Unix(105, 0).UTC()
	early := d.Snapshot("", 0, 1)
	*now = time.Unix(106, 0).UTC()
	d.Record("b.test", 30, 40, "bob")
	*now = time.Unix(110, 0).UTC()
	incremental := d.Snapshot(early.BootID, early.Buckets[0].Sequence, 1)
	late := d.Snapshot("", 0, 0)
	u, v := domainTrafficTotals(early)
	x, y := domainTrafficTotals(incremental)
	if u+x != 130 || v+y != 240 {
		t.Fatalf("early consumer = %d/%d", u+x, v+y)
	}
	if u, v = domainTrafficTotals(late); u != 30 || v != 40 {
		t.Fatalf("late consumer = %d/%d, want retained 30/40", u, v)
	}
	if len(late.Buckets) != 2 || len(late.Buckets[0].Entries) != 0 || late.Buckets[0].OtherUplinkBytes != 0 || late.Buckets[0].OtherDownlinkBytes != 0 {
		t.Fatalf("eviction retained bytes/other or erased cursor bucket: %+v", late)
	}
	if early.Buckets[0].Entries[0].User != "alice" || early.Buckets[0].Entries[0].UplinkBytes != 100 {
		t.Fatal("previously returned snapshot was mutated")
	}
	t.Log("early=130/240, late=30/40; evicted 100/200 is deliberately lost only to late collection; other=0")
}

func TestDomainTrafficBoundariesIdleAndRollback(t *testing.T) {
	d, now := newTestDomainTraffic(t, 8)
	d.retention = 300 * time.Second
	d.Record("a.test", 1, 2, "alice")
	*now = time.Unix(104, 999999999).UTC()
	if s := d.Snapshot("", 0, 0); len(s.Buckets) != 0 || s.LatestSequence != 1 {
		t.Fatalf("open bucket: %+v", s)
	}
	*now = time.Unix(105, 0).UTC()
	first := d.Snapshot("", 0, 0)
	if len(first.Buckets) != 1 {
		t.Fatal("closed boundary missing")
	}
	// A backward wall clock must not reopen an already consumed bucket.
	*now = time.Unix(99, 0).UTC()
	d.Record("b.test", 3, 4, "bob")
	s := d.Snapshot(first.BootID, 1, 1)
	if len(s.Buckets) != 0 || s.LatestSequence != 2 {
		t.Fatalf("rollback created an old closed bucket: %+v", s)
	}
	*now = time.Unix(110, 0).UTC()
	s = d.Snapshot(first.BootID, 1, 1)
	if len(s.Buckets) != 1 || s.Buckets[0].Sequence != 2 || s.Buckets[0].StartUnix != 105 {
		t.Fatalf("rollback lost cursor order: %+v", s)
	}
	*now = time.Unix(170, 0).UTC()
	d.Record("c.test", 5, 6, "alice")
	*now = time.Unix(175, 0).UTC()
	s = d.Snapshot(first.BootID, 2, 1)
	if s.HasGap || len(s.Buckets) != 1 || s.Buckets[0].Sequence != 3 {
		t.Fatalf("idle created false gap: %+v", s)
	}
	*now = time.Unix(400, 0).UTC()
	if s = d.Snapshot("", 0, 0); len(s.Buckets) != 3 {
		t.Fatal("start == cutoff should remain")
	}
	*now = time.Unix(405, 0).UTC()
	if s = d.Snapshot("", 0, 0); len(s.Buckets) != 2 || s.Buckets[0].StartUnix != 105 {
		t.Fatalf("cutoff trim: %+v", s)
	}
	*now = time.Unix(90, 0).UTC()
	s = d.Snapshot("", 0, 0)
	if len(s.Buckets) != 2 {
		t.Fatal("rollback changed published visibility or restored expired bucket")
	}
	if _, ok := d.lastSeen["alice\x00a.test"]; ok {
		t.Fatal("expired LRU index retained")
	}
}

func TestDomainTrafficPagedCursorOpenLatestGapAndBoot(t *testing.T) {
	d, now := newTestDomainTraffic(t, 16)
	for i := 0; i < 3; i++ {
		*now = time.Unix(int64(100+i*5), 0).UTC()
		d.Record(fmt.Sprintf("d%d.test", i), uint64(i+1), uint64(i+2), "alice")
	}
	first := d.Snapshot("", 0, 1)
	if len(first.Buckets) != 1 || first.Buckets[0].Sequence != 1 || first.LatestSequence != 3 {
		t.Fatalf("first page: %+v", first)
	}
	if !reflect.DeepEqual(first, d.Snapshot(first.BootID, 0, 1)) {
		t.Fatal("same cursor changed without writes")
	}
	second := d.Snapshot(first.BootID, 1, 1)
	if len(second.Buckets) != 1 || second.Buckets[0].Sequence != 2 {
		t.Fatalf("second page: %+v", second)
	}
	if open := d.Snapshot(first.BootID, 2, 1); len(open.Buckets) != 0 || open.LatestSequence != 3 {
		t.Fatalf("open latest: %+v", open)
	}
	*now = time.Unix(115, 0).UTC()
	if s := d.Snapshot(first.BootID, 2, 1); len(s.Buckets) != 1 || s.Buckets[0].Sequence != 3 {
		t.Fatalf("third page: %+v", s)
	}
	if s := d.Snapshot(first.BootID, math.MaxUint64, 1); s.HasGap || len(s.Buckets) != 0 {
		t.Fatalf("future cursor overflowed: %+v", s)
	}
	*now = time.Unix(125, 0).UTC()
	if s := d.Snapshot(first.BootID, 0, 1); !s.HasGap || s.OldestSequence != 3 || len(s.Buckets) != 1 {
		t.Fatalf("expired cursor: %+v", s)
	}
	other, _ := newTestDomainTraffic(t, 16)
	other.clock = func() time.Time { return *now }
	other.Record("fresh.test", 7, 8, "bob")
	*now = time.Unix(130, 0).UTC()
	if s := other.Snapshot(first.BootID, math.MaxUint64, 1); s.BootID == first.BootID || s.HasGap || len(s.Buckets) != 1 || s.Buckets[0].Sequence != 1 {
		t.Fatalf("new boot: %+v", s)
	}
}

func TestDomainTrafficAuthenticatedUnknownAndTieEviction(t *testing.T) {
	d, now := newTestDomainTraffic(t, 2)
	d.Record("", 1, 2)
	d.Record("", 3, 4, "alice")
	d.Record("same.test", 5, 6, "bob")
	if len(d.lastSeen) != 2 {
		t.Fatal("authenticated unknown must consume capacity; anonymous unknown must not")
	}
	d.Record("new.test", 7, 8, "carol")
	*now = time.Unix(105, 0).UTC()
	s := d.Snapshot("", 0, 0)
	if len(s.Buckets) != 1 || len(s.Buckets[0].Entries) != 2 || len(d.lastSeen) != 2 {
		t.Fatalf("tie capacity: %+v", s)
	}
	b := s.Buckets[0]
	if b.OtherUplinkBytes != 0 || b.OtherDownlinkBytes != 0 || b.UnknownUplinkBytes != 1 || b.UnknownDownlinkBytes != 2 {
		t.Fatalf("eviction altered anonymous counters: %+v", b)
	}
	retained := map[string]bool{}
	for _, e := range b.Entries {
		retained[e.User] = true
	}
	if !retained["carol"] || retained["alice"] == retained["bob"] {
		t.Fatalf("tie must remove exactly one old identity: %v", retained)
	}
}

func TestDomainTrafficSaturatingCounters(t *testing.T) {
	d, now := newTestDomainTraffic(t, 2)
	d.Record("a.test", math.MaxUint64-1, math.MaxUint64-2, "alice")
	d.Record("a.test", 10, 10, "alice")
	d.Record("", math.MaxUint64-1, math.MaxUint64-2)
	d.Record("", 10, 10)
	*now = time.Unix(105, 0).UTC()
	b := d.Snapshot("", 0, 0).Buckets[0]
	if b.Entries[0].UplinkBytes != math.MaxUint64 || b.Entries[0].DownlinkBytes != math.MaxUint64 || b.UnknownUplinkBytes != math.MaxUint64 || b.UnknownDownlinkBytes != math.MaxUint64 {
		t.Fatalf("wrapped counters: %+v", b)
	}
	if saturatingAdd(math.MaxUint64, 0) != math.MaxUint64 || saturatingAdd(0, 7) != 7 {
		t.Fatal("saturation boundary")
	}
}

func TestDomainTrafficConcurrentRecordSnapshotAndCompleteStats(t *testing.T) {
	m, err := NewManager(context.Background(), &Config{DomainTraffic: &DomainTrafficConfig{Enabled: true, MaxDomains: 2}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	m.domainTraffic.clock = func() time.Time { return time.Unix(100, 0).UTC() }
	counter, err := m.RegisterCounter("user>>>alice>>>traffic>>>uplink")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				counter.Add(1)
				m.RecordDomainTraffic(fmt.Sprintf("%d-%d.test", worker, i), 1, 2, "alice")
				m.DomainTrafficBuckets("", 0, 1)
			}
		}(worker)
	}
	wg.Wait()
	m.domainTraffic.clock = func() time.Time { return time.Unix(105, 0).UTC() }
	s := m.DomainTrafficBuckets("", 0, 0)
	if counter.Value() != 2000 {
		t.Fatalf("LRU altered full Stats: %d", counter.Value())
	}
	if len(m.domainTraffic.lastSeen) != 2 || len(s.Buckets) != 1 || len(s.Buckets[0].Entries) != 2 {
		t.Fatalf("unbounded concurrent cache: %+v", s)
	}
	if u, v := domainTrafficTotals(s); u != 2 || v != 4 {
		t.Fatalf("domain cache should retain only latest two records: %d/%d", u, v)
	}
}
