package conf_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/xtls/xray-core/app/router"
	. "github.com/xtls/xray-core/infra/conf"
)

func TestContractWeightedJSONLiteralWeights(t *testing.T) {
	for _, value := range []float64{-1, 0, 0.5, 1, 1.5} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			input := fmt.Sprintf(`{"balancers":[{"tag":"weighted","selector":["a"],"strategy":{"type":"weightedLeastPing","settings":{"weights":[{"match":"a","value":%g}]}}}]}`, value)
			var cfg RouterConfig
			if err := json.Unmarshal([]byte(input), &cfg); err != nil {
				t.Fatal(err)
			}
			built, err := cfg.Build()
			if value <= 0 {
				if err == nil {
					t.Fatal("nonpositive JSON weight accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			settings, err := built.BalancingRule[0].StrategySettings.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			if got := settings.(*router.StrategyWeightedLeastPingConfig).Weights[0].Value; float64(got) != value {
				t.Fatalf("weight=%v want %v", got, value)
			}
		})
	}
}
