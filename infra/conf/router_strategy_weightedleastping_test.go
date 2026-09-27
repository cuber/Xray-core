package conf_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/router"
	. "github.com/xtls/xray-core/infra/conf"
	"google.golang.org/protobuf/proto"
)

func TestWeightedLeastPingConfig(t *testing.T) {
	input := `{
		"balancers": [{
			"tag": "weighted",
			"selector": ["route-"],
			"strategy": {
				"type": "weightedLeastPing",
				"settings": {
					"weights": [
						{"match": "node-a", "value": 5},
						{"regexp": true, "match": "node-[bc]", "value": 2}
					],
					"maxRTT": "600ms",
					"rttTolerance": "120ms",
					"tolerance": 0.2,
					"minSamples": 5
				}
			}
		}]
	}`
	config := new(RouterConfig)
	if err := json.Unmarshal([]byte(input), config); err != nil {
		t.Fatal(err)
	}
	built, err := config.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(built.BalancingRule) != 1 {
		t.Fatalf("got %d balancing rules, want 1", len(built.BalancingRule))
	}
	rule := built.BalancingRule[0]
	if rule.Strategy != "weightedleastping" {
		t.Fatalf("strategy = %q, want weightedleastping", rule.Strategy)
	}
	instance, err := rule.StrategySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	want := &router.StrategyWeightedLeastPingConfig{
		Weights: []*router.StrategyWeight{
			{Match: "node-a", Value: 5},
			{Regexp: true, Match: "node-[bc]", Value: 2},
		},
		MaxRTT:       int64(600 * time.Millisecond),
		RttTolerance: int64(120 * time.Millisecond),
		Tolerance:    0.2,
		MinSamples:   5,
	}
	if !proto.Equal(instance.(proto.Message), want) {
		t.Fatalf("strategy settings = %v, want %v", instance, want)
	}
}

func TestWeightedLeastPingRejectsInvalidWeight(t *testing.T) {
	input := `{
		"balancers": [{
			"tag": "weighted",
			"selector": ["route-"],
			"strategy": {
				"type": "weightedLeastPing",
				"settings": {
					"weights": [{"match": "node-a", "value": 0}]
				}
			}
		}]
	}`
	config := new(RouterConfig)
	if err := json.Unmarshal([]byte(input), config); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Build(); err == nil {
		t.Fatal("expected invalid zero weight to fail")
	}
}
