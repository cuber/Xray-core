package conf

import (
	"strings"

	"github.com/xtls/xray-core/app/observatory/burst"
	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/infra/conf/cfgcommon/duration"
	"google.golang.org/protobuf/proto"
)

const (
	strategyRandom            string = "random"
	strategyLeastPing         string = "leastping"
	strategyRoundRobin        string = "roundrobin"
	strategyLeastLoad         string = "leastload"
	strategyWeightedLeastPing string = "weightedleastping"
)

var (
	strategyConfigLoader = NewJSONConfigLoader(ConfigCreatorCache{
		strategyRandom:            func() interface{} { return new(strategyEmptyConfig) },
		strategyLeastPing:         func() interface{} { return new(strategyEmptyConfig) },
		strategyRoundRobin:        func() interface{} { return new(strategyEmptyConfig) },
		strategyLeastLoad:         func() interface{} { return new(strategyLeastLoadConfig) },
		strategyWeightedLeastPing: func() interface{} { return new(strategyWeightedLeastPingConfig) },
	}, "type", "settings")
)

type strategyEmptyConfig struct {
}

func (v *strategyEmptyConfig) Build() (proto.Message, error) {
	return nil, nil
}

type strategyLeastLoadConfig struct {
	// weight settings
	Costs []*router.StrategyWeight `json:"costs,omitempty"`
	// ping rtt baselines
	Baselines []duration.Duration `json:"baselines,omitempty"`
	// expected nodes count to select
	Expected int32 `json:"expected,omitempty"`
	// max acceptable rtt, filter away high delay nodes. default 0
	MaxRTT duration.Duration `json:"maxRTT,omitempty"`
	// acceptable failure rate
	Tolerance float64 `json:"tolerance,omitempty"`
}

type strategyWeightedLeastPingConfig struct {
	Weights      []*router.StrategyWeight `json:"weights,omitempty"`
	MaxRTT       duration.Duration        `json:"maxRTT,omitempty"`
	RTTTolerance duration.Duration        `json:"rttTolerance,omitempty"`
	Tolerance    float64                  `json:"tolerance,omitempty"`
	MinSamples   int32                    `json:"minSamples,omitempty"`
}

// healthCheckSettings holds settings for health Checker
type healthCheckSettings struct {
	Destination          string            `json:"destination"`
	Connectivity         string            `json:"connectivity"`
	Interval             duration.Duration `json:"interval"`
	SamplingCount        int               `json:"sampling"`
	Timeout              duration.Duration `json:"timeout"`
	HttpMethod           string            `json:"httpMethod"`
	KeepAlive            bool              `json:"keepAlive"`
	DestinationsByPrefix map[string]string `json:"destinationsByPrefix,omitempty"`
}

func (h healthCheckSettings) Build() (proto.Message, error) {
	var httpMethod string
	if h.HttpMethod == "" {
		httpMethod = "HEAD"
	} else {
		httpMethod = strings.TrimSpace(h.HttpMethod)
	}
	return &burst.HealthPingConfig{
		Destination:          h.Destination,
		Connectivity:         h.Connectivity,
		Interval:             int64(h.Interval),
		Timeout:              int64(h.Timeout),
		SamplingCount:        int32(h.SamplingCount),
		HttpMethod:           httpMethod,
		KeepAlive:            h.KeepAlive,
		DestinationsByPrefix: h.DestinationsByPrefix,
	}, nil
}

// Build implements Buildable.
func (v *strategyLeastLoadConfig) Build() (proto.Message, error) {
	config := &router.StrategyLeastLoadConfig{}
	config.Costs = v.Costs
	config.Tolerance = float32(v.Tolerance)
	if config.Tolerance < 0 {
		config.Tolerance = 0
	}
	if config.Tolerance > 1 {
		config.Tolerance = 1
	}
	config.Expected = v.Expected
	if config.Expected < 0 {
		config.Expected = 0
	}
	config.MaxRTT = int64(v.MaxRTT)
	if config.MaxRTT < 0 {
		config.MaxRTT = 0
	}
	config.Baselines = make([]int64, 0)
	for _, b := range v.Baselines {
		if b <= 0 {
			continue
		}
		config.Baselines = append(config.Baselines, int64(b))
	}
	return config, nil
}

func (v *strategyWeightedLeastPingConfig) Build() (proto.Message, error) {
	for _, weight := range v.Weights {
		if weight == nil || strings.TrimSpace(weight.Match) == "" {
			return nil, errors.New("weightedLeastPing weight match must not be empty")
		}
		if weight.Value <= 0 {
			return nil, errors.New("weightedLeastPing weight value must be greater than zero")
		}
	}

	config := &router.StrategyWeightedLeastPingConfig{
		Weights:      v.Weights,
		MaxRTT:       int64(v.MaxRTT),
		RttTolerance: int64(v.RTTTolerance),
		Tolerance:    float32(v.Tolerance),
		MinSamples:   v.MinSamples,
	}
	if config.MaxRTT < 0 {
		config.MaxRTT = 0
	}
	if config.RttTolerance < 0 {
		config.RttTolerance = 0
	}
	if config.Tolerance < 0 {
		config.Tolerance = 0
	}
	if config.Tolerance > 1 {
		config.Tolerance = 1
	}
	if config.MinSamples < 0 {
		config.MinSamples = 0
	}
	return config, nil
}
