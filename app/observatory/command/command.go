package command

import (
	"context"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common"
	core "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"google.golang.org/grpc"
)

type service struct {
	UnimplementedObservatoryServiceServer
	v *core.Instance

	observatory extension.Observatory
}

func (s *service) GetOutboundStatus(ctx context.Context, request *GetOutboundStatusRequest) (*GetOutboundStatusResponse, error) {
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
