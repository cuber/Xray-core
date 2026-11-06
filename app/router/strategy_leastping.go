package router

import (
	"context"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
)

type LeastPingStrategy struct {
	ctx         context.Context
	observatory extension.Observatory
	memberWrap
}

func (l *LeastPingStrategy) GetPrincipleTarget(strings []string) []string {
	return []string{l.PickOutbound(strings)}
}

func (l *LeastPingStrategy) InjectContext(ctx context.Context) {
	l.ctx = ctx
	common.Must(core.RequireFeatures(l.ctx, func(observatory extension.Observatory) error {
		l.observatory = l.wrapped(observatory)
		return nil
	}))
}

// FallbackActive implements BalancingFallbackState (spec017): the same decision as PickOutbound, which
// changes no state.
func (l *LeastPingStrategy) FallbackActive(candidates []string) bool {
	return l.PickOutbound(candidates) == ""
}

// PickShares implements BalancingShares: every new pick goes to the one PickOutbound picks now.
func (l *LeastPingStrategy) PickShares(candidates []string) (map[string]float64, map[string]float64) {
	if pick := l.PickOutbound(candidates); pick != "" {
		return map[string]float64{pick: 1}, nil
	}
	return nil, nil
}

func (l *LeastPingStrategy) PickOutbound(strings []string) string {
	if l.observatory == nil {
		errors.LogError(l.ctx, "observer is nil")
		return ""
	}
	observeReport, err := l.observatory.GetObservation(l.ctx)
	if err != nil {
		errors.LogInfoInner(l.ctx, err, "cannot get observer report")
		return ""
	}
	outboundsList := outboundList(strings)
	if result, ok := observeReport.(*observatory.ObservationResult); ok {
		status := result.Status
		leastPing := int64(99999999)
		selectedOutboundName := ""
		for _, v := range status {
			if outboundsList.contains(v.OutboundTag) && v.Alive && v.Delay < leastPing {
				selectedOutboundName = v.OutboundTag
				leastPing = v.Delay
			}
		}
		return selectedOutboundName
	}

	// No way to understand observeReport
	return ""
}

type outboundList []string

func (o outboundList) contains(name string) bool {
	for _, v := range o {
		if v == name {
			return true
		}
	}
	return false
}
