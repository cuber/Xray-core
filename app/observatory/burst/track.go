package burst

import (
	"fmt"
	"strings"

	"github.com/xtls/xray-core/app/observatory"
	"google.golang.org/protobuf/proto"
)

// Track (spec014): outbounds whose observation follows another outbound's probe.
// A tracked outbound is never probed; the observation result reports it with a
// copy of its probe's status, so balancers fail over on one probe per path.

// ValidateTrackShape checks a track list on its own: every group names a probe
// and at least one outbound, no tag is tracked twice and no probe is tracked.
func ValidateTrackShape(groups []*TrackGroup) error {
	tracked := map[string]int{}
	for i, g := range groups {
		if g.GetProbe() == "" {
			return fmt.Errorf("burstObservatory.track[%d].probe: empty", i)
		}
		if len(g.GetOutbounds()) == 0 {
			return fmt.Errorf("burstObservatory.track[%d].outbounds: empty; a group tracks at least one outbound", i)
		}
		for _, tag := range g.GetOutbounds() {
			if tag == "" {
				return fmt.Errorf("burstObservatory.track[%d].outbounds: empty tag", i)
			}
			if at, ok := tracked[tag]; ok {
				return fmt.Errorf("burstObservatory.track[%d].outbounds: %q is already tracked in group #%d", i, tag, at)
			}
			tracked[tag] = i
		}
	}
	for i, g := range groups {
		if at, ok := tracked[g.GetProbe()]; ok {
			return fmt.Errorf("burstObservatory.track[%d].probe: %q is itself tracked in group #%d; tracking does not chain", i, g.GetProbe(), at)
		}
	}
	return nil
}

// ValidateTrack checks a configuration's track list against its outbound tags:
// every tag exists, each probe is selected by a ping group, and no tracked tag
// is (selectors match by prefix, so one could probe it anyway).
func ValidateTrack(config *Config, outboundTags []string) error {
	groups := config.GetTrack()
	if len(groups) == 0 {
		return nil
	}
	if err := ValidateTrackShape(groups); err != nil {
		return err
	}
	known := make(map[string]bool, len(outboundTags))
	for _, tag := range outboundTags {
		known[tag] = true
	}
	var selectors []string
	for _, g := range resolvePingGroups(config) {
		selectors = append(selectors, g.GetSubjectSelector()...)
	}
	selected := func(tag string) string {
		for _, s := range selectors {
			if strings.HasPrefix(tag, s) {
				return s
			}
		}
		return ""
	}
	for i, g := range groups {
		if !known[g.GetProbe()] {
			return fmt.Errorf("burstObservatory.track[%d].probe: no outbound %q", i, g.GetProbe())
		}
		if selected(g.GetProbe()) == "" {
			return fmt.Errorf("burstObservatory.track[%d].probe: %q is not probed; no subjectSelector selects it", i, g.GetProbe())
		}
		for _, tag := range g.GetOutbounds() {
			if !known[tag] {
				return fmt.Errorf("burstObservatory.track[%d].outbounds: no outbound %q", i, tag)
			}
			if s := selected(tag); s != "" {
				return fmt.Errorf("burstObservatory.track[%d].outbounds: %q is probed itself (subjectSelector %q); a tracked outbound is not probed", i, tag, s)
			}
		}
	}
	return nil
}

// trackIndex is the loaded track list; it is immutable after New.
type trackIndex struct {
	groups  []*TrackGroup
	probeOf map[string]string
}

func newTrackIndex(groups []*TrackGroup) trackIndex {
	index := trackIndex{groups: groups, probeOf: map[string]string{}}
	for _, g := range groups {
		for _, tag := range g.GetOutbounds() {
			index.probeOf[tag] = g.GetProbe()
		}
	}
	return index
}

// follow returns, for every tracked outbound whose probe has a status in probed,
// a copy of that status under the tracked tag, marked by tracked_from.
func (t trackIndex) follow(probed []*observatory.OutboundStatus) []*observatory.OutboundStatus {
	if len(t.groups) == 0 {
		return nil
	}
	byTag := make(map[string]*observatory.OutboundStatus, len(probed))
	for _, s := range probed {
		byTag[s.OutboundTag] = s
	}
	var out []*observatory.OutboundStatus
	for _, g := range t.groups {
		probe := byTag[g.GetProbe()]
		if probe == nil {
			continue
		}
		for _, tag := range g.GetOutbounds() {
			copied := proto.Clone(probe).(*observatory.OutboundStatus)
			copied.OutboundTag = tag
			copied.TrackedFrom = g.GetProbe()
			out = append(out, copied)
		}
	}
	return out
}

// probes replaces tracked tags by their probes, keeping order and dropping duplicates.
func (t trackIndex) probes(tags []string) []string {
	if len(t.probeOf) == 0 {
		return tags
	}
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		if probe, ok := t.probeOf[tag]; ok {
			tag = probe
		}
		if !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out
}

// statuses reports the groups as loaded, each with its probe's status from probed.
func (t trackIndex) statuses(probed []*observatory.OutboundStatus) []*observatory.TrackStatus {
	byTag := make(map[string]*observatory.OutboundStatus, len(probed))
	for _, s := range probed {
		byTag[s.OutboundTag] = s
	}
	out := make([]*observatory.TrackStatus, 0, len(t.groups))
	for _, g := range t.groups {
		out = append(out, &observatory.TrackStatus{
			Probe:       g.GetProbe(),
			Outbounds:   append([]string(nil), g.GetOutbounds()...),
			ProbeStatus: byTag[g.GetProbe()],
		})
	}
	return out
}
