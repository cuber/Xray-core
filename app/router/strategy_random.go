package router

import (
	"context"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/dice"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
)

// RandomStrategy represents a random balancing strategy
type RandomStrategy struct {
	FallbackTag string
	memberWrap

	ctx         context.Context
	observatory extension.Observatory
}

func (s *RandomStrategy) InjectContext(ctx context.Context) {
	s.ctx = ctx
	if len(s.FallbackTag) > 0 || s.observes() {
		common.Must(core.RequireFeatures(s.ctx, func(observatory extension.Observatory) error {
			s.observatory = s.wrapped(observatory)
			return nil
		}))
	}
}

func (s *RandomStrategy) GetPrincipleTarget(strings []string) []string {
	return strings
}

// FallbackActive implements BalancingFallbackState (spec017): no candidate is alive. It never rolls
// a die.
func (s *RandomStrategy) FallbackActive(candidates []string) bool {
	return len(aliveCandidates(s.ctx, s.observatory, candidates)) == 0
}

func (s *RandomStrategy) PickOutbound(candidates []string) string {
	candidates = aliveCandidates(s.ctx, s.observatory, candidates)

	count := len(candidates)
	if count == 0 {
		// goes to fallbackTag
		return ""
	}
	return candidates[dice.Roll(count)]
}
