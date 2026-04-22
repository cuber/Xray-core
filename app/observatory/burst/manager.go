package burst

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/xtls/xray-core/features/routing"
)

// Manager orchestrates multiple HealthPing groups, each with its own
// subject-selector and fully independent probe settings (destination /
// interval / timeout / sampling / httpMethod).
//
// A tag must belong to at most one group. Overlap detection happens at
// config-load time (see ValidatePingGroups); at runtime Manager still
// longest-prefix-dispatches tags defensively.
type Manager struct {
	ctx    context.Context
	groups []*HealthPing
}

// NewManager builds a Manager from a list of HealthPingGroup protos. If
// `groups` is empty the returned Manager is a no-op on Start/Stop/Check.
func NewManager(ctx context.Context, dispatcher routing.Dispatcher, groups []*HealthPingGroup) *Manager {
	m := &Manager{ctx: ctx}
	for _, g := range groups {
		hp := NewHealthPing(ctx, dispatcher, g.GetPingConfig())
		hp.Selector = append([]string(nil), g.GetSubjectSelector()...)
		m.groups = append(m.groups, hp)
	}
	return m
}

// Groups returns the underlying HealthPing instances (read-only view).
func (m *Manager) Groups() []*HealthPing {
	return m.groups
}

// StartAll starts every group's scheduler. `resolve(selector)` expands a
// group's tag-prefix selector into concrete outbound tags at tick time.
func (m *Manager) StartAll(resolve func(selector []string) ([]string, error)) {
	for _, hp := range m.groups {
		sel := hp.Selector
		hp.StartScheduler(func() ([]string, error) { return resolve(sel) })
	}
}

// StopAll stops every group's scheduler.
func (m *Manager) StopAll() {
	for _, hp := range m.groups {
		hp.StopScheduler()
	}
}

// Check dispatches each tag to the group that owns it (longest-prefix
// match) and runs a one-shot probe on that group. Tags not covered by any
// group are dropped.
func (m *Manager) Check(tags []string) error {
	byGroup := make(map[*HealthPing][]string)
	for _, t := range tags {
		if hp := m.findGroup(t); hp != nil {
			byGroup[hp] = append(byGroup[hp], t)
		}
	}
	for hp, tgs := range byGroup {
		if err := hp.Check(tgs); err != nil {
			return err
		}
	}
	return nil
}

// findGroup returns the HealthPing whose selector has the longest prefix
// matching tag, or nil if none matches.
func (m *Manager) findGroup(tag string) *HealthPing {
	var best *HealthPing
	bestLen := -1
	for _, hp := range m.groups {
		for _, s := range hp.Selector {
			if strings.HasPrefix(tag, s) && len(s) > bestLen {
				bestLen = len(s)
				best = hp
			}
		}
	}
	return best
}

// WalkResults invokes fn(tag, stats) for every measurement across all
// groups. Stats are computed (and cached) under each group's lock, but fn
// runs *after* the lock is released — so a slow reader (e.g. gRPC
// GetObservation on a busy dispatcher) cannot block probe scheduling or
// result writes on the hot path.
//
// Rationale: `leastPing` strategy calls GetObservation per dispatched
// connection, which fans out to WalkResults. Previously fn ran inside
// hp.access and also called the O(sampling) `getStatistics` under the
// lock, serializing every PutResult / Cleanup / dispatcher goroutine
// behind stats computation. On a busy node that pegs CPU.
func (m *Manager) WalkResults(fn func(tag string, stats HealthPingStats)) {
	type entry struct {
		tag   string
		stats HealthPingStats
	}
	var snapshot []entry
	for _, hp := range m.groups {
		hp.access.Lock()
		if cap(snapshot) < len(snapshot)+len(hp.Results) {
			grown := make([]entry, len(snapshot), len(snapshot)+len(hp.Results))
			copy(grown, snapshot)
			snapshot = grown
		}
		for t, r := range hp.Results {
			snapshot = append(snapshot, entry{tag: t, stats: *r.GetWithCache()})
		}
		hp.access.Unlock()
	}
	for _, e := range snapshot {
		fn(e.tag, e.stats)
	}
}

// ValidatePingGroups checks that no two selectors across different groups
// overlap in prefix-coverage (a tag could match both). Returns a friendly
// error explaining which selectors collide and how to fix it. Safe to call
// at `xray run -test -config` time.
func ValidatePingGroups(groups []*HealthPingGroup) error {
	// normalize to (groupIdx, selector) pairs
	type sel struct {
		group int
		value string
	}
	var all []sel
	for i, g := range groups {
		sels := g.GetSubjectSelector()
		if len(sels) == 0 {
			return fmt.Errorf("burstObservatory.pingGroups[%d].subjectSelector: empty selector list; each group must select at least one tag prefix", i)
		}
		// dedup within a single group
		seen := map[string]bool{}
		for _, s := range sels {
			if seen[s] {
				return fmt.Errorf("burstObservatory.pingGroups[%d].subjectSelector: duplicate selector %q within the same group", i, s)
			}
			seen[s] = true
			all = append(all, sel{group: i, value: s})
		}
	}

	// sort by length descending so the *longer* selector is always reported
	// as the one "more specific than" the shorter — makes the error message
	// read naturally.
	sort.SliceStable(all, func(i, j int) bool { return len(all[i].value) > len(all[j].value) })

	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			a, b := all[i], all[j]
			if a.group == b.group {
				continue
			}
			if !strings.HasPrefix(a.value, b.value) && !strings.HasPrefix(b.value, a.value) {
				continue
			}
			// conflict
			long, short := a, b
			if len(b.value) > len(a.value) {
				long, short = b, a
			}
			return fmt.Errorf(
				"burstObservatory.pingGroups: selector %q (group #%d) conflicts with %q (group #%d) — every tag starting with %q also starts with %q, so a tag could match both probe groups. "+
					"Each outbound must belong to exactly one group. "+
					"Fix: narrow %q (e.g., use %q or a more specific prefix), or merge the groups.",
				long.value, long.group, short.value, short.group,
				long.value, short.value,
				short.value, long.value,
			)
		}
	}
	return nil
}
