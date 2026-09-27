package burst

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestValidatePingGroups_NoOverlap(t *testing.T) {
	err := ValidatePingGroups([]*HealthPingGroup{
		{SubjectSelector: []string{"sjw-"}},
		{SubjectSelector: []string{"bus-"}},
		{SubjectSelector: []string{"ive-"}},
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidatePingGroups_PrefixConflict(t *testing.T) {
	err := ValidatePingGroups([]*HealthPingGroup{
		{SubjectSelector: []string{"sjw-"}},
		{SubjectSelector: []string{"sjw-ss"}}, // prefix of nothing, but sjw-ss tags also match sjw-
	})
	if err == nil {
		t.Fatal("expected overlap error, got nil")
	}
	msg := err.Error()
	for _, kw := range []string{`"sjw-"`, `"sjw-ss"`, "conflicts", "Fix:"} {
		if !strings.Contains(msg, kw) {
			t.Errorf("error message missing keyword %q; full error: %s", kw, msg)
		}
	}
}

func TestValidatePingGroups_ExactDuplicateAcrossGroups(t *testing.T) {
	err := ValidatePingGroups([]*HealthPingGroup{
		{SubjectSelector: []string{"sjw-"}},
		{SubjectSelector: []string{"sjw-"}},
	})
	if err == nil {
		t.Fatal("expected error for identical selector in two groups")
	}
	if !strings.Contains(err.Error(), "conflicts") {
		t.Errorf("expected 'conflicts' in error, got: %v", err)
	}
}

func TestValidatePingGroups_DuplicateWithinGroup(t *testing.T) {
	err := ValidatePingGroups([]*HealthPingGroup{
		{SubjectSelector: []string{"sjw-", "sjw-"}},
	})
	if err == nil {
		t.Fatal("expected duplicate-within-group error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected 'duplicate' in error, got: %v", err)
	}
}

func TestValidatePingGroups_EmptySelector(t *testing.T) {
	err := ValidatePingGroups([]*HealthPingGroup{
		{SubjectSelector: nil},
	})
	if err == nil {
		t.Fatal("expected empty-selector error")
	}
	if !strings.Contains(err.Error(), "empty selector") {
		t.Errorf("expected 'empty selector' in error, got: %v", err)
	}
}

func TestValidatePingGroups_EmptyList(t *testing.T) {
	if err := ValidatePingGroups(nil); err != nil {
		t.Fatalf("empty list should be valid (legacy no-op), got: %v", err)
	}
}

func TestValidatePingGroups_NestedPrefixesAcrossMultipleGroups(t *testing.T) {
	// a / a-b / a-b-c across 3 groups: a-b-c matches both a-b and a (transitively),
	// so the validator should catch the first pair it finds.
	err := ValidatePingGroups([]*HealthPingGroup{
		{SubjectSelector: []string{"a"}},
		{SubjectSelector: []string{"a-b"}},
		{SubjectSelector: []string{"a-b-c"}},
	})
	if err == nil {
		t.Fatal("expected error for nested prefix overlap")
	}
}

// Legacy `burstObservatory` with pingConfig but empty subjectSelector was
// historically a valid "disabled burst probe" config. The multi-group
// rewrite must preserve that no-op behavior rather than rejecting the
// config at load time.
func TestResolvePingGroups_LegacyEmptySelectorIsNoop(t *testing.T) {
	c := &Config{
		SubjectSelector: nil,
		PingConfig:      &HealthPingConfig{Destination: "http://example.com/generate_204"},
	}
	if got := resolvePingGroups(c); got != nil {
		t.Fatalf("expected nil groups for legacy empty-selector config; got %d groups", len(got))
	}
}

func TestResolvePingGroups_LegacyWithSelectorBuildsOneGroup(t *testing.T) {
	c := &Config{
		SubjectSelector: []string{"sjw-"},
		PingConfig:      &HealthPingConfig{Destination: "http://example.com/generate_204"},
	}
	got := resolvePingGroups(c)
	if len(got) != 1 || len(got[0].GetSubjectSelector()) != 1 || got[0].GetSubjectSelector()[0] != "sjw-" {
		t.Fatalf("expected single-group fallback with sjw- selector, got: %+v", got)
	}
}

// TestManagerWalkResults_CallbackRunsOutsideLock is a regression test for
// the CPU-100% lockup caused by running stats computation + the user
// callback under hp.access. If fn holds that lock, everything serializes;
// we fix that by snapshotting under the lock and calling fn afterwards.
//
// The test proves fn runs outside the lock by having fn try to PutResult
// on the same HealthPing it's walking. If fn ran under hp.access, PutResult
// would deadlock (the test times out). If fn runs outside, PutResult
// completes immediately.
func TestManagerWalkResults_CallbackRunsOutsideLock(t *testing.T) {
	hp := &HealthPing{
		Selector: []string{"sjw-"},
		Settings: &HealthPingSettings{SamplingCount: 2, Interval: time.Second},
	}
	// seed a result so WalkResults has something to visit
	hp.PutResult("sjw-ss", 10*time.Millisecond)
	m := &Manager{groups: []*HealthPing{hp}}

	done := make(chan struct{})
	go func() {
		m.WalkResults(func(tag string, _ HealthPingStats) {
			// If this line ran under hp.access, PutResult would deadlock on
			// sync.Mutex.Lock() since the goroutine already owns it.
			hp.PutResult(tag+"-echo", 20*time.Millisecond)
		})
		close(done)
	}()
	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("WalkResults callback appears to hold hp.access — deadlock")
	}
	// Sanity: the echo write succeeded.
	hp.access.Lock()
	_, ok := hp.Results["sjw-ss-echo"]
	hp.access.Unlock()
	if !ok {
		t.Fatal("PutResult inside callback did not take effect")
	}
}

// TestManagerWalkResults_StatsAreSnapshot verifies fn receives a value
// copy, so the caller can't race with a concurrent PutResult through a
// live pointer.
func TestManagerWalkResults_StatsAreSnapshot(t *testing.T) {
	hp := &HealthPing{
		Selector: []string{"a-"},
		Settings: &HealthPingSettings{SamplingCount: 3, Interval: time.Second},
	}
	hp.PutResult("a-x", 5*time.Millisecond)
	m := &Manager{groups: []*HealthPing{hp}}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.WalkResults(func(tag string, s HealthPingStats) {
			if s.All == 0 {
				t.Errorf("expected at least one sample, got 0")
			}
		})
	}()
	wg.Wait()
}

func TestManagerFindGroup_LongestPrefixWins(t *testing.T) {
	m := &Manager{groups: []*HealthPing{
		{Selector: []string{"sjw-"}},
		{Selector: []string{"sjw-hy2"}},
	}}
	if got := m.findGroup("sjw-hy2"); got != m.groups[1] {
		t.Fatalf("sjw-hy2 should match longer selector; got %p want %p", got, m.groups[1])
	}
	if got := m.findGroup("sjw-ss"); got != m.groups[0] {
		t.Fatalf("sjw-ss should match shorter selector; got %p want %p", got, m.groups[0])
	}
	if got := m.findGroup("bus-ntt"); got != nil {
		t.Fatalf("bus-ntt should match no group; got %v", got)
	}
}
