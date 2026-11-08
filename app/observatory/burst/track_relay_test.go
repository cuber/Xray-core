package burst

import (
	"context"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
)

// Core spec024 FR-007: a Hy2 relay outbound the server refused or cannot relay for is dead whatever its probe,
// with the reason in the observation and in GetTrack; bound and not yet connected relays follow the probe.

type relayProxy struct{ status string }

func (p relayProxy) Process(context.Context, *transport.Link, internet.Dialer) error { return nil }
func (p relayProxy) RelayStatus() (status, user string, ok bool) {
	return p.status, "", p.status != ""
}

type relayHandler struct {
	outbound.Handler
	proxy proxy.Outbound
}

func (h relayHandler) GetOutbound() proxy.Outbound { return h.proxy }

type relayManager struct {
	outbound.Manager
	handlers map[string]outbound.Handler
}

func (m relayManager) GetHandler(tag string) outbound.Handler { return m.handlers[tag] }

func TestTrackMarksRefusedRelaysDead(t *testing.T) {
	statuses := map[string]string{
		"bound": "bound", "pending": "no_connection", "unknown": "unknown",
		"denied": "denied", "unsupported": "unsupported", "plain": "",
	}
	handlers := make(map[string]outbound.Handler)
	var tracked []string
	for tag, status := range statuses {
		handlers[tag] = relayHandler{proxy: relayProxy{status: status}}
		tracked = append(tracked, tag)
	}
	groups := []*HealthPingGroup{{SubjectSelector: []string{"owner"}, PingConfig: &HealthPingConfig{SamplingCount: 3}}}
	o := &Observer{
		hpm:   NewManager(context.Background(), nil, groups),
		ohm:   relayManager{handlers: handlers},
		track: newTrackIndex([]*TrackGroup{{Probe: "owner", Outbounds: tracked}}),
	}
	hp := o.hpm.Groups()[0]
	hp.Results = map[string]*HealthPingRTTS{"owner": NewHealthPingResult(3, time.Hour)}
	hp.Results["owner"].Put(20 * time.Millisecond)

	result, err := o.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byTag := make(map[string]*observatory.OutboundStatus)
	for _, s := range result.(*observatory.ObservationResult).Status {
		byTag[s.OutboundTag] = s
	}
	if !byTag["owner"].Alive {
		t.Fatal("the probe is alive")
	}
	for tag, status := range statuses {
		refused := status == "unknown" || status == "denied" || status == "unsupported"
		s := byTag[tag]
		if s.Alive == refused {
			t.Errorf("%s: alive %v", tag, s.Alive)
		}
		if refused && s.LastErrorReason != "hysteria relay "+status {
			t.Errorf("%s: reason %q", tag, s.LastErrorReason)
		}
	}

	track, err := o.GetTrack(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := track[0].Refused; len(got) != 3 || got["denied"] != "hysteria relay denied" || got["bound"] != "" {
		t.Errorf("GetTrack refused = %v", got)
	}
}
