package burst

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/features/outbound"
	"google.golang.org/protobuf/proto"
)

// selector is an outbound manager that only selects, by prefix, among fixed tags.
type selector struct {
	outbound.Manager
	tags []string
}

func (s selector) Select(prefixes []string) []string {
	var out []string
	for _, tag := range s.tags {
		for _, prefix := range prefixes {
			if strings.HasPrefix(tag, prefix) {
				out = append(out, tag)
				break
			}
		}
	}
	return out
}

// forceObserver probes "ecs" (with a result) and "alpine" (none yet); "a" tracks "ecs"
// and "fcc" is an outbound nothing probes.
func forceObserver(t *testing.T) *Observer {
	t.Helper()
	groups := []*HealthPingGroup{{SubjectSelector: []string{"ecs", "alpine"}, PingConfig: &HealthPingConfig{SamplingCount: 3}}}
	o := &Observer{
		ohm:   selector{tags: []string{"ecs", "alpine", "a", "fcc"}},
		hpm:   NewManager(context.Background(), nil, groups),
		track: newTrackIndex([]*TrackGroup{{Probe: "ecs", Outbounds: []string{"a"}}}),
	}
	hp := o.hpm.Groups()[0]
	hp.Results = map[string]*HealthPingRTTS{"ecs": NewHealthPingResult(3, time.Hour)}
	hp.Results["ecs"].Put(30 * time.Millisecond)
	return o
}

func setClock(t *testing.T, unix int64) {
	t.Helper()
	previous := forceDownNow
	forceDownNow = func() time.Time { return time.Unix(unix, 0) }
	t.Cleanup(func() { forceDownNow = previous })
}

func selection(t *testing.T, o *Observer) map[string]*observatory.OutboundStatus {
	t.Helper()
	result, err := o.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byTag := map[string]*observatory.OutboundStatus{}
	for _, s := range result.(*observatory.ObservationResult).Status {
		byTag[s.OutboundTag] = s
	}
	return byTag
}

func TestForceDownRefusesTheWholeRequest(t *testing.T) {
	o := forceObserver(t)
	if _, err := o.SetForceDown([]ForceDown{{Tag: "ecs", Reason: "quota"}}); err != nil {
		t.Fatal(err)
	}
	for name, entries := range map[string][]ForceDown{
		"unknown":   {{Tag: "nope"}},
		"unprobed":  {{Tag: "fcc"}},
		"tracked":   {{Tag: "a"}},
		"empty":     {{Tag: ""}},
		"twice":     {{Tag: "alpine"}, {Tag: "alpine"}},
		"long":      {{Tag: "alpine", Reason: strings.Repeat("x", ForceDownReasonMax+1)}},
		"one wrong": {{Tag: "alpine"}, {Tag: "fcc"}},
	} {
		if _, err := o.SetForceDown(entries); err == nil {
			t.Fatalf("%s: accepted %v", name, entries)
		}
		if got := o.ForceDown(); len(got) != 1 || got[0].Tag != "ecs" {
			t.Fatalf("%s: set changed to %v", name, got)
		}
	}
}

func TestForceDownKeepsSince(t *testing.T) {
	o := forceObserver(t)
	setClock(t, 100)
	applied, err := o.SetForceDown([]ForceDown{{Tag: "ecs", Reason: "quota 190/190"}})
	if err != nil || !reflect.DeepEqual(applied, []ForceDown{{Tag: "ecs", Reason: "quota 190/190", Since: 100}}) {
		t.Fatalf("applied = %v, %v", applied, err)
	}
	setClock(t, 200)
	applied, err = o.SetForceDown([]ForceDown{{Tag: "alpine", Reason: "b"}, {Tag: "ecs", Reason: "quota 191/190", Since: 5}})
	want := []ForceDown{{Tag: "alpine", Reason: "b", Since: 200}, {Tag: "ecs", Reason: "quota 191/190", Since: 100}}
	if err != nil || !reflect.DeepEqual(applied, want) || !reflect.DeepEqual(o.ForceDown(), want) {
		t.Fatalf("applied = %v, %v; set = %v", applied, err, o.ForceDown())
	}
	// Released and forced again: a new Since.
	if _, err := o.SetForceDown(nil); err != nil || o.ForceDown() != nil {
		t.Fatalf("clear: %v, %v", o.ForceDown(), err)
	}
	setClock(t, 300)
	if applied, _ := o.SetForceDown([]ForceDown{{Tag: "ecs"}}); applied[0].Since != 300 {
		t.Fatalf("since after release = %v", applied)
	}
}

func TestForceDownViews(t *testing.T) {
	o := forceObserver(t)
	before := selection(t, o)
	if !before["ecs"].Alive || !before["a"].Alive || before["alpine"] != nil || before["ecs"].ForceDown {
		t.Fatalf("before = %v", before)
	}
	setClock(t, 100)
	if _, err := o.SetForceDown([]ForceDown{{Tag: "ecs", Reason: "cdt"}, {Tag: "alpine", Reason: "cdt"}}); err != nil {
		t.Fatal(err)
	}

	// Selection: dead, the reason in last_error_reason, the measurement kept; the tracked
	// copy inherits; alpine, which has no result yet, gets a dead status of its own.
	after := selection(t, o)
	ecs, a, alpine := after["ecs"], after["a"], after["alpine"]
	if ecs.Alive || !ecs.ForceDown || ecs.ForceDownReason != "cdt" || ecs.ForceDownSince != 100 ||
		ecs.LastErrorReason != "force down: cdt" || ecs.Delay != before["ecs"].Delay || ecs.HealthPing.GetAll() != 1 {
		t.Fatalf("ecs = %v", ecs)
	}
	if a.Alive || !a.ForceDown || a.TrackedFrom != "ecs" {
		t.Fatalf("a = %v", a)
	}
	if alpine == nil || alpine.Alive || !alpine.ForceDown || alpine.HealthPing != nil {
		t.Fatalf("alpine = %v", alpine)
	}

	// Measured: alive as probed, the force fields beside it, no tracked copy.
	measured := map[string]*observatory.OutboundStatus{}
	for _, s := range o.MeasuredObservation() {
		measured[s.OutboundTag] = s
	}
	if len(measured) != 2 || !measured["ecs"].Alive || !measured["ecs"].ForceDown || measured["ecs"].LastErrorReason != "" ||
		measured["alpine"].Alive || !measured["alpine"].ForceDown {
		t.Fatalf("measured = %v", measured)
	}
	track, _ := o.GetTrack(context.Background())
	if probe := track[0].ProbeStatus; !probe.Alive || !probe.ForceDown || probe.ForceDownSince != 100 {
		t.Fatalf("track probe = %v", probe)
	}

	// Released: the measurement at once.
	if _, err := o.SetForceDown(nil); err != nil {
		t.Fatal(err)
	}
	after = selection(t, o)
	if len(after) != len(before) {
		t.Fatalf("after release = %v, want %v", after, before)
	}
	for tag, status := range before {
		if !proto.Equal(after[tag], status) {
			t.Fatalf("after release %s = %v, want %v", tag, after[tag], status)
		}
	}
}

func TestForceDownNeedsASelector(t *testing.T) {
	o := forceObserver(t)
	o.ohm = nil
	if _, err := o.SetForceDown([]ForceDown{{Tag: "ecs"}}); err == nil {
		t.Fatal("accepted without an outbound manager")
	}
}
