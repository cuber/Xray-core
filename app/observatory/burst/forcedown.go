package burst

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/features/outbound"
)

// Force down (spec015): a runtime set of probes, owned by the control plane, that
// the selection view (GetObservation) reports dead. Probing goes on, and the
// measured view keeps the measurement with the force fields beside it.

// ForceDownReasonMax bounds an entry's reason in bytes (spec015 F7).
const ForceDownReasonMax = 256

// ForceDown is one entry of the set.
type ForceDown struct {
	Tag    string
	Reason string
	Since  int64 // Unix seconds the tag entered the set
}

// forceDownNow is the clock that stamps Since; tests replace it.
var forceDownNow = time.Now

// forceSet is an immutable snapshot: readers load it without a lock.
type forceSet struct {
	byTag   map[string]ForceDown
	ordered []ForceDown // by tag
}

type forceDownState struct {
	write sync.Mutex // serialises SetForceDown; readers only load
	set   atomic.Pointer[forceSet]
}

func (s *forceDownState) load() *forceSet {
	return s.set.Load()
}

// entries returns the set ordered by tag; nil when empty.
func (f *forceSet) entries() []ForceDown {
	if f == nil {
		return nil
	}
	return append([]ForceDown(nil), f.ordered...)
}

// apply marks the forced probes in a fresh probe result. The selection view also
// reports them dead; a forced probe without a result yet gets a status of its own,
// because a balancer counts an unobserved candidate as alive.
func (f *forceSet) apply(probed []*observatory.OutboundStatus, selection bool) []*observatory.OutboundStatus {
	if f == nil || len(f.ordered) == 0 {
		return probed
	}
	seen := make(map[string]bool, len(f.ordered))
	for _, status := range probed {
		if entry, ok := f.byTag[status.OutboundTag]; ok {
			seen[entry.Tag] = true
			markForced(status, entry, selection)
		}
	}
	for _, entry := range f.ordered {
		if !seen[entry.Tag] {
			status := &observatory.OutboundStatus{OutboundTag: entry.Tag}
			markForced(status, entry, selection)
			probed = append(probed, status)
		}
	}
	return probed
}

func markForced(status *observatory.OutboundStatus, entry ForceDown, selection bool) {
	status.ForceDown = true
	status.ForceDownReason = entry.Reason
	status.ForceDownSince = entry.Since
	if selection {
		status.Alive = false
		status.LastErrorReason = "force down: " + entry.Reason
	}
}

// ForceDown returns the set ordered by tag.
func (o *Observer) ForceDown() []ForceDown {
	return o.force.load().entries()
}

// SetForceDown replaces the set with entries (spec015 F2/F3): every tag must be a
// probe of this observer, at most once, with a bounded reason. An invalid entry
// refuses the whole request and leaves the set unchanged. A tag already in the set
// keeps its Since. It returns the set as applied.
func (o *Observer) SetForceDown(entries []ForceDown) ([]ForceDown, error) {
	o.force.write.Lock()
	defer o.force.write.Unlock()
	probes, err := o.probeTags()
	if err != nil {
		return nil, err
	}
	previous := o.force.load()
	next := &forceSet{byTag: make(map[string]ForceDown, len(entries))}
	now := forceDownNow().Unix()
	for i, entry := range entries {
		switch {
		case entry.Tag == "":
			return nil, fmt.Errorf("force down entry #%d: empty outbound tag", i)
		case len(entry.Reason) > ForceDownReasonMax:
			return nil, fmt.Errorf("force down %q: reason is %d bytes, at most %d", entry.Tag, len(entry.Reason), ForceDownReasonMax)
		case next.byTag[entry.Tag].Tag != "":
			return nil, fmt.Errorf("force down %q: listed twice", entry.Tag)
		}
		if probe, ok := o.track.probeOf[entry.Tag]; ok {
			return nil, fmt.Errorf("force down %q: tracked from %q (spec014); force the probe", entry.Tag, probe)
		}
		if !probes[entry.Tag] {
			return nil, fmt.Errorf("force down %q: not a probed outbound; no subjectSelector selects it", entry.Tag)
		}
		entry.Since = now
		if previous != nil {
			if kept, ok := previous.byTag[entry.Tag]; ok {
				entry.Since = kept.Since
			}
		}
		next.byTag[entry.Tag] = entry
		next.ordered = append(next.ordered, entry)
	}
	sort.Slice(next.ordered, func(i, j int) bool { return next.ordered[i].Tag < next.ordered[j].Tag })
	if len(next.ordered) == 0 {
		next = nil
	}
	o.force.set.Store(next)
	return next.entries(), nil
}

// probeTags is every outbound a ping group selects now: the configured subjects,
// probed already or not yet.
func (o *Observer) probeTags() (map[string]bool, error) {
	hs, ok := o.ohm.(outbound.HandlerSelector)
	if !ok {
		return nil, errors.New("outbound.Manager is not a HandlerSelector")
	}
	tags := map[string]bool{}
	for _, group := range o.hpm.Groups() {
		for _, tag := range hs.Select(group.Selector) {
			tags[tag] = true
		}
	}
	return tags, nil
}
