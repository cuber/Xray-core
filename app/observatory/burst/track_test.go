package burst

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"google.golang.org/protobuf/proto"
)

func TestTrackFollowCopiesTheProbeStatus(t *testing.T) {
	index := newTrackIndex([]*TrackGroup{{Probe: "ecs", Outbounds: []string{"a", "b"}}})
	probe := &observatory.OutboundStatus{
		OutboundTag: "ecs", Alive: true, Delay: 30,
		HealthPing: &observatory.HealthPingMeasurementResult{All: 25, Fail: 1},
	}
	other := &observatory.OutboundStatus{OutboundTag: "fcc", Alive: true, Delay: 10}
	copies := index.follow([]*observatory.OutboundStatus{other, probe})
	if len(copies) != 2 {
		t.Fatalf("copies = %v", copies)
	}
	for i, tag := range []string{"a", "b"} {
		want := proto.Clone(probe).(*observatory.OutboundStatus)
		want.OutboundTag, want.TrackedFrom = tag, "ecs"
		if !proto.Equal(copies[i], want) {
			t.Fatalf("copy %d = %v, want %v", i, copies[i], want)
		}
	}
	copies[0].HealthPing.All = 0
	if probe.HealthPing.All != 25 {
		t.Fatal("a tracked copy shares its probe's measurement")
	}
	// No status before the probe's first result, the same as unobserved.
	if got := index.follow([]*observatory.OutboundStatus{other}); len(got) != 0 {
		t.Fatalf("copies without a probe result = %v", got)
	}
	if got := newTrackIndex(nil).follow([]*observatory.OutboundStatus{probe}); got != nil {
		t.Fatalf("copies without track = %v", got)
	}
}

func TestTrackChecksTheProbe(t *testing.T) {
	index := newTrackIndex([]*TrackGroup{{Probe: "ecs", Outbounds: []string{"a", "b"}}})
	if got := index.probes([]string{"a", "b", "ecs", "x"}); !reflect.DeepEqual(got, []string{"ecs", "x"}) {
		t.Fatalf("probes = %v", got)
	}
	if got := newTrackIndex(nil).probes([]string{"a"}); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("probes without track = %v", got)
	}
}

func TestTrackShape(t *testing.T) {
	if err := ValidateTrackShape([]*TrackGroup{{Probe: "p", Outbounds: []string{"a"}}, {Probe: "q", Outbounds: []string{"b"}}}); err != nil {
		t.Fatal(err)
	}
	for want, groups := range map[string][]*TrackGroup{
		"probe: empty":           {{Outbounds: []string{"a"}}},
		"outbounds: empty;":      {{Probe: "p"}},
		"empty tag":              {{Probe: "p", Outbounds: []string{""}}},
		`"a" is already tracked`: {{Probe: "p", Outbounds: []string{"a"}}, {Probe: "q", Outbounds: []string{"a"}}},
		`"a" is itself tracked`:  {{Probe: "p", Outbounds: []string{"a"}}, {Probe: "a", Outbounds: []string{"b"}}},
	} {
		if err := ValidateTrackShape(groups); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v", want, err)
		}
	}
}

func TestTrackAgainstOutbounds(t *testing.T) {
	config := func(track ...*TrackGroup) *Config {
		return &Config{PingGroups: []*HealthPingGroup{{SubjectSelector: []string{"ahk.", "fcc"}}}, Track: track}
	}
	tags := []string{"ahk.ecs-hy2", "ahk.alpine-hy2", "fcc-a", "tk.auto-hy2", "sg.auto-hy2"}
	if err := ValidateTrack(config(&TrackGroup{Probe: "ahk.ecs-hy2", Outbounds: []string{"tk.auto-hy2", "sg.auto-hy2"}}), tags); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTrack(config(), nil); err != nil {
		t.Fatalf("no track: %v", err)
	}
	legacy := &Config{SubjectSelector: []string{"ahk."}, Track: []*TrackGroup{{Probe: "ahk.ecs-hy2", Outbounds: []string{"tk.auto-hy2"}}}}
	if err := ValidateTrack(legacy, tags); err != nil {
		t.Fatalf("legacy selector: %v", err)
	}
	for want, group := range map[string]*TrackGroup{
		`no outbound "missing"`:       {Probe: "missing", Outbounds: []string{"tk.auto-hy2"}},
		`"tk.auto-hy2" is not probed`: {Probe: "tk.auto-hy2", Outbounds: []string{"sg.auto-hy2"}},
		`no outbound "gone"`:          {Probe: "ahk.ecs-hy2", Outbounds: []string{"gone"}},
		`"ahk.alpine-hy2" is probed itself (subjectSelector "ahk.")`: {Probe: "ahk.ecs-hy2", Outbounds: []string{"ahk.alpine-hy2"}},
	} {
		if err := ValidateTrack(config(group), tags); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v", want, err)
		}
	}
}

// The observer reports tracked copies after the probed statuses, and GetTrack the
// groups with their probe's status (Check's mapping is TestTrackChecksTheProbe).
func TestObserverReportsTrackedOutbounds(t *testing.T) {
	groups := []*HealthPingGroup{{SubjectSelector: []string{"ecs", "fcc"}, PingConfig: &HealthPingConfig{SamplingCount: 3}}}
	o := &Observer{
		hpm:   NewManager(context.Background(), nil, groups),
		track: newTrackIndex([]*TrackGroup{{Probe: "ecs", Outbounds: []string{"a", "b"}}, {Probe: "fcc", Outbounds: []string{"c"}}}),
	}
	hp := o.hpm.Groups()[0]
	hp.Results = map[string]*HealthPingRTTS{"ecs": NewHealthPingResult(3, time.Hour)}
	hp.Results["ecs"].Put(30 * time.Millisecond)

	result, err := o.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	statuses := result.(*observatory.ObservationResult).Status
	var tags []string
	for _, s := range statuses {
		tags = append(tags, s.OutboundTag+"<"+s.TrackedFrom)
	}
	if !reflect.DeepEqual(tags, []string{"ecs<", "a<ecs", "b<ecs"}) {
		t.Fatalf("statuses = %v", tags)
	}
	if statuses[1].Alive != statuses[0].Alive || statuses[1].Delay != statuses[0].Delay {
		t.Fatalf("tracked %v differs from its probe %v", statuses[1], statuses[0])
	}

	track, err := o.GetTrack(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(track) != 2 || track[0].Probe != "ecs" || !reflect.DeepEqual(track[0].Outbounds, []string{"a", "b"}) ||
		track[0].ProbeStatus.GetOutboundTag() != "ecs" || track[1].Probe != "fcc" || track[1].ProbeStatus != nil {
		t.Fatalf("track = %v", track)
	}
}
