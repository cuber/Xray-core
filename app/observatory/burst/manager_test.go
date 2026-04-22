package burst

import (
	"strings"
	"testing"
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
