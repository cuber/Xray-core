package command

import (
	"context"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/app/observatory/burst"
	"github.com/xtls/xray-core/common"
	core "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type service struct {
	UnimplementedObservatoryServiceServer
	v *core.Instance

	observatory extension.Observatory
}

// forceDowner is an observatory that keeps a force-down set: the burst observatory
// (spec015). Its measured view is what GetOutboundStatus reports.
type forceDowner interface {
	MeasuredObservation() []*observatory.OutboundStatus
	ForceDown() []burst.ForceDown
	SetForceDown(entries []burst.ForceDown) ([]burst.ForceDown, error)
}

func (s *service) GetOutboundStatus(ctx context.Context, request *GetOutboundStatusRequest) (*GetOutboundStatusResponse, error) {
	if f, ok := s.observatory.(forceDowner); ok {
		return &GetOutboundStatusResponse{Status: &observatory.ObservationResult{Status: f.MeasuredObservation()}}, nil
	}
	resp, err := s.observatory.GetObservation(ctx)
	if err != nil {
		return nil, err
	}
	retdata := resp.(*observatory.ObservationResult)
	// Probed outbounds only: a tracked status copies its probe's (spec014), and
	// GetTrack reports those.
	probed := make([]*observatory.OutboundStatus, 0, len(retdata.Status))
	for _, status := range retdata.Status {
		if status.TrackedFrom == "" {
			probed = append(probed, status)
		}
	}
	return &GetOutboundStatusResponse{
		Status: &observatory.ObservationResult{Status: probed},
	}, nil
}

// tracker is an observatory that loads track groups: the burst observatory (spec014).
type tracker interface {
	GetTrack(ctx context.Context) ([]*observatory.TrackStatus, error)
}

func (s *service) GetTrack(ctx context.Context, request *GetTrackRequest) (*GetTrackResponse, error) {
	t, ok := s.observatory.(tracker)
	if !ok {
		return &GetTrackResponse{}, nil
	}
	groups, err := t.GetTrack(ctx)
	if err != nil {
		return nil, err
	}
	return &GetTrackResponse{Groups: groups}, nil
}

// GetForceDown reports the force-down set (spec015); empty for an observatory
// without one.
func (s *service) GetForceDown(ctx context.Context, request *GetForceDownRequest) (*GetForceDownResponse, error) {
	f, ok := s.observatory.(forceDowner)
	if !ok {
		return &GetForceDownResponse{}, nil
	}
	return &GetForceDownResponse{Entries: forceDownEntries(f.ForceDown())}, nil
}

// SetForceDown replaces the force-down set as a whole or not at all (spec015 F2/F3).
func (s *service) SetForceDown(ctx context.Context, request *SetForceDownRequest) (*SetForceDownResponse, error) {
	f, ok := s.observatory.(forceDowner)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "force down needs a burstObservatory")
	}
	entries := make([]burst.ForceDown, 0, len(request.GetEntries()))
	for _, entry := range request.GetEntries() {
		entries = append(entries, burst.ForceDown{Tag: entry.GetOutboundTag(), Reason: entry.GetReason()})
	}
	applied, err := f.SetForceDown(entries)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &SetForceDownResponse{Entries: forceDownEntries(applied)}, nil
}

func forceDownEntries(set []burst.ForceDown) []*ForceDownEntry {
	out := make([]*ForceDownEntry, 0, len(set))
	for _, entry := range set {
		out = append(out, &ForceDownEntry{OutboundTag: entry.Tag, Reason: entry.Reason, Since: entry.Since})
	}
	return out
}

func (s *service) Register(server *grpc.Server) {
	RegisterObservatoryServiceServer(server, s)
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, cfg interface{}) (interface{}, error) {
		s := core.MustFromContext(ctx)
		sv := &service{v: s}
		err := s.RequireFeatures(func(Observatory extension.Observatory) {
			sv.observatory = Observatory
		}, false)
		if err != nil {
			return nil, err
		}
		return sv, nil
	}))
}
