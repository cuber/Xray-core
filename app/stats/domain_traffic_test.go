package stats

import (
	"context"
	"testing"
	"time"
)

func newTestDomainTraffic(t *testing.T, maxDomains int) (*domainTraffic, *time.Time) {
	t.Helper()
	now := time.Unix(100, 0).UTC()
	d := newDomainTraffic(&DomainTrafficConfig{
		Enabled:               true,
		BucketIntervalSeconds: 5,
		RetentionSeconds:      15,
		MaxDomains:            uint32(maxDomains),
	})
	d.clock = func() time.Time { return now }
	return d, &now
}

func TestDomainTrafficBucketsNormalizeAndTrackDirections(t *testing.T) {
	d, now := newTestDomainTraffic(t, 512)
	d.Record(" GitHub.COM. ", 10, 20)
	d.Record("", 1, 2)

	if got := d.Snapshot("", 0, 0); len(got.Buckets) != 0 {
		t.Fatalf("current bucket was returned before completion: %+v", got.Buckets)
	}
	*now = time.Unix(105, 0).UTC()
	got := d.Snapshot("", 0, 0)
	if len(got.Buckets) != 1 {
		t.Fatalf("got %d buckets, want 1", len(got.Buckets))
	}
	bucket := got.Buckets[0]
	if bucket.Sequence != 1 || bucket.StartUnix != 100 || bucket.EndUnix != 105 {
		t.Fatalf("unexpected bucket metadata: %+v", bucket)
	}
	if len(bucket.Entries) != 1 || bucket.Entries[0].Domain != "github.com" {
		t.Fatalf("unexpected entries: %+v", bucket.Entries)
	}
	if bucket.Entries[0].UplinkBytes != 10 || bucket.Entries[0].DownlinkBytes != 20 {
		t.Fatalf("unexpected github traffic: %+v", bucket.Entries[0])
	}
	if bucket.UnknownUplinkBytes != 1 || bucket.UnknownDownlinkBytes != 2 {
		t.Fatalf("unexpected unknown traffic: %+v", bucket)
	}
}

func TestDomainTrafficSequenceHasNoFalseGapAcrossEmptyBuckets(t *testing.T) {
	d, now := newTestDomainTraffic(t, 512)
	d.Record("a.example", 1, 2)
	*now = time.Unix(105, 0).UTC()
	first := d.Snapshot("", 0, 0)
	if len(first.Buckets) != 1 {
		t.Fatalf("got %d first buckets, want 1", len(first.Buckets))
	}
	*now = time.Unix(115, 0).UTC()
	d.Record("b.example", 3, 4)
	*now = time.Unix(120, 0).UTC()
	second := d.Snapshot(first.BootID, first.LatestSequence, 0)
	if second.HasGap {
		t.Fatal("empty time buckets were incorrectly reported as a sequence gap")
	}
	if len(second.Buckets) != 1 || second.Buckets[0].Sequence != 2 {
		t.Fatalf("unexpected incremental response: %+v", second)
	}
}

func TestDomainTrafficLRUUsesLastSeenAndMovesEvictedBytesToOther(t *testing.T) {
	d, now := newTestDomainTraffic(t, 2)
	d.Record("a.example", 10, 20)
	d.Record("b.example", 30, 40)
	*now = time.Unix(104, 0).UTC()
	d.Record("a.example", 1, 2)
	*now = time.Unix(105, 0).UTC()
	d.Record("c.example", 50, 60)
	*now = time.Unix(110, 0).UTC()

	got := d.Snapshot("", 0, 0)
	if len(got.Buckets) != 2 {
		t.Fatalf("got %d buckets, want 2", len(got.Buckets))
	}
	first := got.Buckets[0]
	if len(first.Entries) != 1 || first.Entries[0].Domain != "a.example" {
		t.Fatalf("a.example should be retained: %+v", first.Entries)
	}
	if first.OtherUplinkBytes != 30 || first.OtherDownlinkBytes != 40 {
		t.Fatalf("evicted b.example was not moved to other: %+v", first)
	}
	second := got.Buckets[1]
	if len(second.Entries) != 1 || second.Entries[0].Domain != "c.example" {
		t.Fatalf("c.example should be retained: %+v", second.Entries)
	}
}

func TestDomainTrafficRetentionAndCursorAreIdempotent(t *testing.T) {
	d, now := newTestDomainTraffic(t, 512)
	d.Record("a.example", 1, 2)
	*now = time.Unix(105, 0).UTC()
	first := d.Snapshot("", 0, 0)
	repeated := d.Snapshot(first.BootID, 0, 0)
	if len(repeated.Buckets) != 1 || repeated.Buckets[0].Sequence != first.Buckets[0].Sequence {
		t.Fatalf("same cursor should return stable data: %+v", repeated)
	}
	*now = time.Unix(120, 0).UTC()
	got := d.Snapshot(first.BootID, first.LatestSequence, 0)
	if len(got.Buckets) != 0 {
		t.Fatalf("old bucket was not pruned: %+v", got.Buckets)
	}
}

func TestDomainTrafficDisabledStatsManagerKeepsStatsAvailable(t *testing.T) {
	m, err := NewManager(context.Background(), &Config{})
	if err != nil {
		t.Fatal(err)
	}
	if m.DomainTrafficEnabled() {
		t.Fatal("domain traffic should be disabled by default")
	}
	if _, err := m.RegisterCounter("normal"); err != nil {
		t.Fatalf("normal stats unavailable while domain traffic is disabled: %v", err)
	}
}

func TestDomainTrafficConfigRejectsUnsafeLimits(t *testing.T) {
	if _, err := NewManager(context.Background(), &Config{DomainTraffic: &DomainTrafficConfig{
		Enabled: true, MaxDomains: defaultDomainTrafficMaxDomains + 1,
	}}); err == nil {
		t.Fatal("expected max_domains validation error")
	}
	if _, err := NewManager(context.Background(), &Config{DomainTraffic: &DomainTrafficConfig{
		Enabled: true, BucketIntervalSeconds: 10, RetentionSeconds: 5,
	}}); err == nil {
		t.Fatal("expected retention validation error")
	}
}
