package command

import (
	"context"
	"time"

	routerConfig "github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/features/stats"
	"google.golang.org/grpc"
)

// routingServer is an implementation of RoutingService.
type routingServer struct {
	router       routing.Router
	routingStats stats.Channel
}

func (s *routingServer) GetBalancerInfo(ctx context.Context, request *GetBalancerInfoRequest) (*GetBalancerInfoResponse, error) {
	// spec017 L-E: one balancer map for every field, the whole fallback chain included.
	var balancers any = s.router
	if bl, ok := s.router.(routing.BalancerLister); ok {
		balancers = bl.Balancers()
	}
	msg, err := balancerMsg(ctx, balancers, request.GetTag())
	if err != nil {
		return nil, err
	}
	return &GetBalancerInfoResponse{Balancer: msg}, nil
}

// balancerMsg reads one balancer through whichever balancer interfaces r implements: a router, or
// one balancer map of it (spec017).
func balancerMsg(ctx context.Context, r any, tag string) (*BalancerMsg, error) {
	msg := &BalancerMsg{}
	if bo, ok := r.(interface {
		GetOverrideTarget(tag string) (string, error)
	}); ok {
		res, err := bo.GetOverrideTarget(tag)
		if err != nil {
			return nil, err
		}
		msg.Override = &OverrideInfo{
			Target: res,
		}
	}

	if pt, ok := r.(routing.BalancerPrincipleTarget); ok {
		res, err := pt.GetPrincipleTarget(tag)
		if err != nil {
			errors.LogInfoInner(ctx, err, "unable to obtain principle target")
		} else {
			msg.PrincipleTarget = &PrincipleTargetInfo{Tag: res}
		}
	}

	if bm, ok := r.(routing.BalancerMembers); ok { // Core spec019 M10
		if members, err := bm.GetMembers(tag); err != nil {
			errors.LogInfoInner(ctx, err, "unable to obtain members")
		} else {
			for _, member := range members {
				msg.Members = append(msg.Members, &BalancerMember{
					Tag: member.Tag, Balancer: member.Balancer, Weight: member.Weight, Share: member.Share, // spec020
					Status: &BalancerStatus{Known: member.Status.Known, Alive: member.Status.Alive, Delay: member.Status.Delay},
				})
			}
		}
		if status, err := bm.GetStatus(tag); err == nil {
			msg.Status = &BalancerStatus{Known: status.Known, Alive: status.Alive, Delay: status.Delay}
		}
		if strategy, err := bm.GetStrategy(tag); err == nil {
			msg.Strategy = strategy // spec020 P3
		}
	}

	if bf, ok := r.(routing.BalancerFallback); ok { // spec016
		fallbackTag, active, reported, err := bf.GetFallback(tag)
		if err != nil {
			errors.LogInfoInner(ctx, err, "unable to obtain fallback")
			return msg, nil
		}
		msg.Fallback = &FallbackInfo{Tag: fallbackTag, Active: active, Reported: reported}
		if fc, ok := r.(routing.BalancerFallbackChain); ok { // spec017
			chain, err := fc.GetFallbackChain(tag)
			if err != nil {
				errors.LogInfoInner(ctx, err, "unable to obtain fallback chain")
				return msg, nil
			}
			for _, hop := range chain {
				msg.Fallback.Chain = append(msg.Fallback.Chain, &FallbackHop{
					Tag: hop.Tag, Balancer: hop.Balancer, Active: hop.Active, Reported: hop.Reported,
				})
			}
			msg.Fallback.Balancer = len(chain) > 0 && chain[0].Balancer
		}
	}
	return msg, nil
}

// ListBalancers returns every balancer of one balancer map (spec017).
func (s *routingServer) ListBalancers(ctx context.Context, request *ListBalancersRequest) (*ListBalancersResponse, error) {
	bl, ok := s.router.(routing.BalancerLister)
	if !ok {
		return nil, errors.New("unsupported router implementation")
	}
	balancers := bl.Balancers()
	response := &ListBalancersResponse{}
	for _, tag := range balancers.Tags() {
		msg, err := balancerMsg(ctx, balancers, tag)
		if err != nil {
			return nil, err
		}
		response.Balancers = append(response.Balancers, &BalancerItem{Tag: tag, Balancer: msg})
	}
	return response, nil
}

// GetLoopGuards returns the loop guard counters (spec017 L8).
func (s *routingServer) GetLoopGuards(ctx context.Context, request *GetLoopGuardsRequest) (*GetLoopGuardsResponse, error) {
	g, ok := s.router.(routing.LoopGuard)
	if !ok {
		return nil, errors.New("unsupported router implementation")
	}
	fallbackChain, loopbackReentry := g.LoopGuards()
	return &GetLoopGuardsResponse{FallbackChain: fallbackChain, LoopbackReentry: loopbackReentry}, nil
}

func (s *routingServer) OverrideBalancerTarget(ctx context.Context, request *OverrideBalancerTargetRequest) (*OverrideBalancerTargetResponse, error) {
	if bo, ok := s.router.(routing.BalancerOverrider); ok {
		return &OverrideBalancerTargetResponse{}, bo.SetOverrideTarget(request.BalancerTag, request.Target)
	}
	return nil, errors.New("unsupported router implementation")
}

func (s *routingServer) AddRule(ctx context.Context, request *AddRuleRequest) (*AddRuleResponse, error) {
	if bo, ok := s.router.(routing.Router); ok {
		return &AddRuleResponse{}, bo.AddRule(request.Config, request.ShouldAppend)
	}
	return nil, errors.New("unsupported router implementation")
}

func (s *routingServer) RemoveRule(ctx context.Context, request *RemoveRuleRequest) (*RemoveRuleResponse, error) {
	if bo, ok := s.router.(routing.Router); ok {
		return &RemoveRuleResponse{}, bo.RemoveRule(request.RuleTag)
	}
	return nil, errors.New("unsupported router implementation")
}

func (s *routingServer) ListRule(ctx context.Context, request *ListRuleRequest) (*ListRuleResponse, error) {
	if bo, ok := s.router.(routing.Router); ok {
		response := &ListRuleResponse{}
		for _, v := range bo.ListRule() {
			item := &ListRuleItem{
				Tag:     v.GetOutboundTag(),
				RuleTag: v.GetRuleTag(),
			}
			if detailed, ok := v.(interface {
				GetRule() *routerConfig.RoutingRule
			}); ok {
				item.Rule = detailed.GetRule()
			}
			response.Rules = append(response.Rules, item)
		}
		return response, nil
	}
	return nil, errors.New("unsupported router implementation")
}

// NewRoutingServer creates a statistics service with statistics manager.
func NewRoutingServer(router routing.Router, routingStats stats.Channel) RoutingServiceServer {
	return &routingServer{
		router:       router,
		routingStats: routingStats,
	}
}

func (s *routingServer) TestRoute(ctx context.Context, request *TestRouteRequest) (*RoutingContext, error) {
	if request.RoutingContext == nil {
		return nil, errors.New("Invalid routing request.")
	}
	route, err := s.router.PickRoute(AsRoutingContext(request.RoutingContext))
	if err != nil {
		return nil, err
	}
	if request.PublishResult && s.routingStats != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		// Publish queues ctx for asynchronous delivery; release it at its
		// deadline rather than canceling delivery when the RPC returns.
		context.AfterFunc(ctx, cancel)
		s.routingStats.Publish(ctx, route)
	}
	return AsProtobufMessage(request.FieldSelectors)(route), nil
}

func (s *routingServer) SubscribeRoutingStats(request *SubscribeRoutingStatsRequest, stream RoutingService_SubscribeRoutingStatsServer) error {
	if s.routingStats == nil {
		return errors.New("Routing statistics not enabled.")
	}
	genMessage := AsProtobufMessage(request.FieldSelectors)
	subscriber, err := stats.SubscribeRunnableChannel(s.routingStats)
	if err != nil {
		return err
	}
	defer stats.UnsubscribeClosableChannel(s.routingStats, subscriber)
	for {
		select {
		case value, ok := <-subscriber:
			if !ok {
				return errors.New("Upstream closed the subscriber channel.")
			}
			route, ok := value.(routing.Route)
			if !ok {
				return errors.New("Upstream sent malformed statistics.")
			}
			err := stream.Send(genMessage(route))
			if err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *routingServer) mustEmbedUnimplementedRoutingServiceServer() {}

type service struct {
	v *core.Instance
}

func (s *service) Register(server *grpc.Server) {
	common.Must(s.v.RequireFeatures(func(router routing.Router, stats stats.Manager) {
		rs := NewRoutingServer(router, nil)
		RegisterRoutingServiceServer(server, rs)

		// For compatibility purposes
		vCoreDesc := RoutingService_ServiceDesc
		vCoreDesc.ServiceName = "v2ray.core.app.router.command.RoutingService"
		server.RegisterService(&vCoreDesc, rs)
	}, false))
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, cfg interface{}) (interface{}, error) {
		s := core.MustFromContext(ctx)
		return &service{v: s}, nil
	}))
}
