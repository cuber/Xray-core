package router

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
)

type weightedLeastPingNode struct {
	Tag        string
	AverageRTT time.Duration
	Weight     float64
}

// WeightedLeastPingStrategy rotates across healthy nodes according to their
// configured weights. RTT and failure rate only control pool membership.
type WeightedLeastPingStrategy struct {
	settings *StrategyWeightedLeastPingConfig
	weights  *WeightManager
	observer extension.Observatory
	ctx      context.Context

	mu      sync.Mutex
	current map[string]float64
}

func NewWeightedLeastPingStrategy(settings *StrategyWeightedLeastPingConfig) *WeightedLeastPingStrategy {
	return &WeightedLeastPingStrategy{
		settings: settings,
		weights: NewWeightManager(settings.Weights, 1, func(value, weight float64) float64 {
			return value * weight
		}),
		current: make(map[string]float64),
	}
}

func (s *WeightedLeastPingStrategy) InjectContext(ctx context.Context) {
	s.ctx = ctx
	common.Must(core.RequireFeatures(s.ctx, func(observer extension.Observatory) error {
		s.observer = observer
		return nil
	}))
}

func (s *WeightedLeastPingStrategy) GetPrincipleTarget(candidates []string) []string {
	nodes := s.getNodes(candidates)
	tags := make([]string, 0, len(nodes))
	for _, node := range nodes {
		tags = append(tags, node.Tag)
	}
	return tags
}

func (s *WeightedLeastPingStrategy) PickOutbound(candidates []string) string {
	return s.pickWeighted(s.getNodes(candidates))
}

func (s *WeightedLeastPingStrategy) getNodes(candidates []string) []*weightedLeastPingNode {
	if s.observer == nil {
		errors.LogError(s.ctx, "observer is nil")
		return s.filterNodes(candidates, nil)
	}
	result, err := s.observer.GetObservation(s.ctx)
	if err != nil {
		errors.LogInfoInner(s.ctx, err, "cannot get observation")
		return s.filterNodes(candidates, nil)
	}
	observation, ok := result.(*observatory.ObservationResult)
	if !ok {
		errors.LogError(s.ctx, "unexpected observation result type")
		return s.filterNodes(candidates, nil)
	}
	return s.filterNodes(candidates, observation.Status)
}

func (s *WeightedLeastPingStrategy) filterNodes(candidates []string, statuses []*observatory.OutboundStatus) []*weightedLeastPingNode {
	statusByTag := make(map[string]*observatory.OutboundStatus, len(statuses))
	for _, status := range statuses {
		if status != nil {
			statusByTag[status.OutboundTag] = status
		}
	}

	maxRTT := time.Duration(s.settings.MaxRTT)
	minSamples := int64(s.settings.MinSamples)
	bestRTT := time.Duration(1<<63 - 1)
	allNodes := make([]*weightedLeastPingNode, 0, len(candidates))
	healthyNodes := make([]*weightedLeastPingNode, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		weight := s.weights.Get(candidate)
		if weight <= 0 {
			continue
		}
		node := &weightedLeastPingNode{Tag: candidate, Weight: weight}
		allNodes = append(allNodes, node)

		status, ok := statusByTag[candidate]
		if !ok || !status.Alive {
			continue
		}
		averageRTT := time.Duration(status.Delay) * time.Millisecond
		all := int64(1)
		fail := int64(0)
		if status.HealthPing != nil {
			averageRTT = time.Duration(status.HealthPing.Average)
			all = status.HealthPing.All
			fail = status.HealthPing.Fail
		}
		if minSamples > 0 && all < minSamples {
			continue
		}
		if s.settings.Tolerance > 0 && all > 0 && float64(fail)/float64(all) > float64(s.settings.Tolerance) {
			continue
		}
		if maxRTT > 0 && averageRTT >= maxRTT {
			continue
		}

		node.AverageRTT = averageRTT
		healthyNodes = append(healthyNodes, node)
		if averageRTT < bestRTT {
			bestRTT = averageRTT
		}
	}

	rttTolerance := time.Duration(s.settings.RttTolerance)
	if rttTolerance > 0 && len(healthyNodes) > 0 {
		limit := bestRTT + rttTolerance
		filtered := healthyNodes[:0]
		for _, node := range healthyNodes {
			if node.AverageRTT <= limit {
				filtered = append(filtered, node)
			}
		}
		healthyNodes = filtered
	}
	if len(healthyNodes) == 0 {
		healthyNodes = allNodes
	}
	sort.Slice(healthyNodes, func(i, j int) bool {
		return healthyNodes[i].Tag < healthyNodes[j].Tag
	})
	return healthyNodes
}

func (s *WeightedLeastPingStrategy) pickWeighted(nodes []*weightedLeastPingNode) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	active := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		active[node.Tag] = struct{}{}
	}
	for tag := range s.current {
		if _, ok := active[tag]; !ok {
			delete(s.current, tag)
		}
	}
	if len(nodes) == 0 {
		return ""
	}

	totalWeight := float64(0)
	selected := nodes[0]
	selectedCurrent := float64(0)
	for i, node := range nodes {
		s.current[node.Tag] += node.Weight
		totalWeight += node.Weight
		if i == 0 || s.current[node.Tag] > selectedCurrent {
			selected = node
			selectedCurrent = s.current[node.Tag]
		}
	}
	s.current[selected.Tag] -= totalWeight
	return selected.Tag
}
