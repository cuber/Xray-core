package burst

import (
	"context"
	"sync"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/signal/done"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	"google.golang.org/protobuf/proto"
)

type Observer struct {
	config *Config
	ctx    context.Context

	statusLock sync.Mutex
	hpm        *Manager

	finished *done.Instance

	ohm outbound.Manager

	track trackIndex
}

func (o *Observer) GetObservation(ctx context.Context) (proto.Message, error) {
	return &observatory.ObservationResult{Status: o.createResult()}, nil
}

// GetTrack reports the track groups as loaded, each with its probe's current
// status (spec014).
func (o *Observer) GetTrack(ctx context.Context) ([]*observatory.TrackStatus, error) {
	return o.track.statuses(o.probeResult()), nil
}

func (o *Observer) Check(tag []string) {
	_ = o.hpm.Check(o.track.probes(tag))
}

// createResult is the probed statuses followed by the tracked copies.
func (o *Observer) createResult() []*observatory.OutboundStatus {
	result := o.probeResult()
	return append(result, o.track.follow(result)...)
}

func (o *Observer) probeResult() []*observatory.OutboundStatus {
	var result []*observatory.OutboundStatus
	o.hpm.WalkResults(func(name string, stats HealthPingStats) {
		status := observatory.OutboundStatus{
			Alive:           stats.Alive,
			Delay:           stats.Average.Milliseconds(),
			LastErrorReason: "",
			OutboundTag:     name,
			LastSeenTime:    0,
			LastTryTime:     0,
			HealthPing: &observatory.HealthPingMeasurementResult{
				All:       int64(stats.All),
				Fail:      int64(stats.Fail),
				Deviation: int64(stats.Deviation),
				Average:   int64(stats.Average),
				Max:       int64(stats.Max),
				Min:       int64(stats.Min),
			},
		}
		result = append(result, &status)
	})
	return result
}

func (o *Observer) Type() interface{} {
	return extension.ObservatoryType()
}

func (o *Observer) Start() error {
	if len(o.hpm.Groups()) == 0 {
		return nil
	}
	o.finished = done.New()
	o.hpm.StartAll(func(selector []string) ([]string, error) {
		hs, ok := o.ohm.(outbound.HandlerSelector)
		if !ok {
			return nil, errors.New("outbound.Manager is not a HandlerSelector")
		}
		return hs.Select(selector), nil
	})
	return nil
}

func (o *Observer) Close() error {
	if o.finished != nil {
		o.hpm.StopAll()
		return o.finished.Close()
	}
	return nil
}

// resolvePingGroups returns the effective HealthPingGroup list: new-style
// `ping_groups` wins; otherwise fall back to legacy `subject_selector` +
// `ping_config` as a single group.
//
// Legacy no-op behavior: if the legacy config has an empty subject_selector
// (historically used to disable burst probing without removing the block),
// we return nil so Start() skips scheduler setup and nothing is probed,
// matching the pre-multi-group behavior. This avoids rejecting existing
// deployments at load time.
func resolvePingGroups(config *Config) []*HealthPingGroup {
	if len(config.GetPingGroups()) > 0 {
		return config.GetPingGroups()
	}
	if len(config.GetSubjectSelector()) == 0 {
		return nil
	}
	return []*HealthPingGroup{{
		SubjectSelector: config.GetSubjectSelector(),
		PingConfig:      config.GetPingConfig(),
	}}
}

func New(ctx context.Context, config *Config) (*Observer, error) {
	var outboundManager outbound.Manager
	var dispatcher routing.Dispatcher
	err := core.RequireFeatures(ctx, func(om outbound.Manager, rd routing.Dispatcher) {
		outboundManager = om
		dispatcher = rd
	})
	if err != nil {
		return nil, errors.New("Cannot get depended features").Base(err)
	}
	groups := resolvePingGroups(config)
	if err := ValidatePingGroups(groups); err != nil {
		return nil, err
	}
	if err := ValidateTrackShape(config.GetTrack()); err != nil {
		return nil, err
	}
	return &Observer{
		config: config,
		ctx:    ctx,
		ohm:    outboundManager,
		hpm:    NewManager(ctx, dispatcher, groups),
		track:  newTrackIndex(config.GetTrack()),
	}, nil
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return New(ctx, config.(*Config))
	}))
}
