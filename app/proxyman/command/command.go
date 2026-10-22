package command

import (
	"context"
	"sort"

	"github.com/xtls/xray-core/app/commander"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/mux"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy"
	grpc "google.golang.org/grpc"
)

// InboundOperation is the interface for operations that applies to inbound handlers.
type InboundOperation interface {
	// ApplyInbound applies this operation to the given inbound handler.
	ApplyInbound(context.Context, inbound.Handler) error
}

// OutboundOperation is the interface for operations that applies to outbound handlers.
type OutboundOperation interface {
	// ApplyOutbound applies this operation to the given outbound handler.
	ApplyOutbound(context.Context, outbound.Handler) error
}

func getInbound(handler inbound.Handler) (proxy.Inbound, error) {
	gi, ok := handler.(proxy.GetInbound)
	if !ok {
		return nil, errors.New("can't get inbound proxy from handler.")
	}
	return gi.GetInbound(), nil
}

// ApplyInbound implements InboundOperation.
func (op *AddUserOperation) ApplyInbound(ctx context.Context, handler inbound.Handler) error {
	p, err := getInbound(handler)
	if err != nil {
		return err
	}
	um, ok := p.(proxy.UserManager)
	if !ok {
		return errors.New("proxy is not a UserManager")
	}
	mUser, err := op.User.ToMemoryUser()
	if err != nil {
		return errors.New("failed to parse user").Base(err)
	}
	return um.AddUser(ctx, mUser)
}

// ApplyInbound implements InboundOperation.
func (op *RemoveUserOperation) ApplyInbound(ctx context.Context, handler inbound.Handler) error {
	p, err := getInbound(handler)
	if err != nil {
		return err
	}
	um, ok := p.(proxy.UserManager)
	if !ok {
		return errors.New("proxy is not a UserManager")
	}
	return um.RemoveUser(ctx, op.Email)
}

type handlerServer struct {
	s   *core.Instance
	ihm inbound.Manager
	ohm outbound.Manager
}

func (s *handlerServer) AddInbound(ctx context.Context, request *AddInboundRequest) (*AddInboundResponse, error) {
	if err := core.AddInboundHandler(s.s, request.Inbound); err != nil {
		return nil, err
	}

	return &AddInboundResponse{}, nil
}

func (s *handlerServer) RemoveInbound(ctx context.Context, request *RemoveInboundRequest) (*RemoveInboundResponse, error) {
	return &RemoveInboundResponse{}, s.ihm.RemoveHandler(ctx, request.Tag)
}

func (s *handlerServer) AlterInbound(ctx context.Context, request *AlterInboundRequest) (*AlterInboundResponse, error) {
	rawOperation, err := request.Operation.GetInstance()
	if err != nil {
		return nil, errors.New("unknown operation").Base(err)
	}
	operation, ok := rawOperation.(InboundOperation)
	if !ok {
		return nil, errors.New("not an inbound operation")
	}

	handler, err := s.ihm.GetHandler(ctx, request.Tag)
	if err != nil {
		return nil, errors.New("failed to get handler: ", request.Tag).Base(err)
	}

	return &AlterInboundResponse{}, operation.ApplyInbound(ctx, handler)
}

func (s *handlerServer) ListInbounds(ctx context.Context, request *ListInboundsRequest) (*ListInboundsResponse, error) {
	handlers := s.ihm.ListHandlers(ctx)
	response := &ListInboundsResponse{}
	if request.GetIsOnlyTags() {
		for _, handler := range handlers {
			response.Inbounds = append(response.Inbounds, &core.InboundHandlerConfig{
				Tag: handler.Tag(),
			})
		}
	} else {
		for _, handler := range handlers {
			response.Inbounds = append(response.Inbounds, &core.InboundHandlerConfig{
				Tag:              handler.Tag(),
				ReceiverSettings: handler.ReceiverSettings(),
				ProxySettings:    handler.ProxySettings(),
			})
		}
	}

	return response, nil
}

func (s *handlerServer) GetInboundUsers(ctx context.Context, request *GetInboundUserRequest) (*GetInboundUserResponse, error) {
	handler, err := s.ihm.GetHandler(ctx, request.Tag)
	if err != nil {
		return nil, errors.New("failed to get handler: ", request.Tag).Base(err)
	}
	p, err := getInbound(handler)
	if err != nil {
		return nil, err
	}
	um, ok := p.(proxy.UserManager)
	if !ok {
		return nil, errors.New("proxy is not a UserManager")
	}
	if len(request.Email) > 0 {
		return &GetInboundUserResponse{Users: []*protocol.User{protocol.ToProtoUser(um.GetUser(ctx, request.Email))}}, nil
	}
	result := make([]*protocol.User, 0, 100)
	users := um.GetUsers(ctx)
	for _, u := range users {
		result = append(result, protocol.ToProtoUser(u))
	}
	return &GetInboundUserResponse{Users: result}, nil
}

func (s *handlerServer) GetInboundUsersCount(ctx context.Context, request *GetInboundUserRequest) (*GetInboundUsersCountResponse, error) {
	handler, err := s.ihm.GetHandler(ctx, request.Tag)
	if err != nil {
		return nil, errors.New("failed to get handler: ", request.Tag).Base(err)
	}
	p, err := getInbound(handler)
	if err != nil {
		return nil, err
	}
	um, ok := p.(proxy.UserManager)
	if !ok {
		return nil, errors.New("proxy is not a UserManager")
	}
	return &GetInboundUsersCountResponse{Count: um.GetUsersCount(ctx)}, nil
}

type muxStatsProvider interface {
	MuxStats() mux.Stats
}

// GetInboundMuxStats reports the loaded sing-mux setting and the sing-mux and
// Mux.Cool carriers of one inbound, or of every inbound when no tag is given.
func (s *handlerServer) GetInboundMuxStats(ctx context.Context, request *GetInboundMuxStatsRequest) (*GetInboundMuxStatsResponse, error) {
	var handlers []inbound.Handler
	if request.Tag != "" {
		handler, err := s.ihm.GetHandler(ctx, request.Tag)
		if err != nil {
			return nil, errors.New("failed to get handler: ", request.Tag).Base(err)
		}
		if _, ok := handler.(muxStatsProvider); !ok {
			return nil, errors.New("inbound has no multiplexing server: ", request.Tag)
		}
		handlers = []inbound.Handler{handler}
	} else {
		handlers = s.ihm.ListHandlers(ctx)
	}
	response := &GetInboundMuxStatsResponse{}
	for _, handler := range handlers {
		provider, ok := handler.(muxStatsProvider)
		if !ok {
			continue
		}
		stats := provider.MuxStats()
		sing := stats.SingMux
		response.Inbounds = append(response.Inbounds, &InboundMuxStats{
			Tag: handler.Tag(),
			SingMux: &SingMuxStats{
				Enabled:              sing.Enabled,
				MaxStreams:           sing.MaxStreams,
				ActiveCarriers:       sing.ActiveCarriers,
				ActivePaddedCarriers: sing.ActivePadded,
				ActiveTcpStreams:     sing.ActiveTCPStreams,
				ActiveUdpStreams:     sing.ActiveUDPStreams,
				TotalCarriers:        sing.TotalCarriers,
				TotalStreams:         sing.TotalStreams,
				Rejected:             sing.Rejected,
			},
			MuxCool: &MuxCoolStats{
				ActiveCarriers: stats.MuxCool.ActiveCarriers,
				ActiveStreams:  stats.MuxCool.ActiveStreams,
				TotalCarriers:  stats.MuxCool.TotalCarriers,
			},
		})
	}
	sort.Slice(response.Inbounds, func(i, j int) bool { return response.Inbounds[i].Tag < response.Inbounds[j].Tag })
	return response, nil
}

func (s *handlerServer) AddOutbound(ctx context.Context, request *AddOutboundRequest) (*AddOutboundResponse, error) {
	if err := core.AddOutboundHandler(s.s, request.Outbound); err != nil {
		return nil, err
	}
	return &AddOutboundResponse{}, nil
}

func (s *handlerServer) RemoveOutbound(ctx context.Context, request *RemoveOutboundRequest) (*RemoveOutboundResponse, error) {
	return &RemoveOutboundResponse{}, s.ohm.RemoveHandler(ctx, request.Tag)
}

func (s *handlerServer) AlterOutbound(ctx context.Context, request *AlterOutboundRequest) (*AlterOutboundResponse, error) {
	rawOperation, err := request.Operation.GetInstance()
	if err != nil {
		return nil, errors.New("unknown operation").Base(err)
	}
	operation, ok := rawOperation.(OutboundOperation)
	if !ok {
		return nil, errors.New("not an outbound operation")
	}

	handler := s.ohm.GetHandler(request.Tag)
	return &AlterOutboundResponse{}, operation.ApplyOutbound(ctx, handler)
}

func (s *handlerServer) ListOutbounds(ctx context.Context, request *ListOutboundsRequest) (*ListOutboundsResponse, error) {
	handlers := s.ohm.ListHandlers(ctx)
	response := &ListOutboundsResponse{}
	for _, handler := range handlers {
		// Ignore gRPC outbound
		if _, ok := handler.(*commander.Outbound); ok {
			continue
		}
		response.Outbounds = append(response.Outbounds, &core.OutboundHandlerConfig{
			Tag:            handler.Tag(),
			SenderSettings: handler.SenderSettings(),
			ProxySettings:  handler.ProxySettings(),
		})
	}
	return response, nil
}

func (s *handlerServer) mustEmbedUnimplementedHandlerServiceServer() {}

type service struct {
	v *core.Instance
}

func (s *service) Register(server *grpc.Server) {
	hs := &handlerServer{
		s: s.v,
	}
	common.Must(s.v.RequireFeatures(func(im inbound.Manager, om outbound.Manager) {
		hs.ihm = im
		hs.ohm = om
	}, false))
	RegisterHandlerServiceServer(server, hs)

	// For compatibility purposes
	vCoreDesc := HandlerService_ServiceDesc
	vCoreDesc.ServiceName = "v2ray.core.app.proxyman.command.HandlerService"
	server.RegisterService(&vCoreDesc, hs)
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, cfg interface{}) (interface{}, error) {
		s := core.MustFromContext(ctx)
		return &service{v: s}, nil
	}))
}
