package conf

import (
	"google.golang.org/protobuf/proto"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/app/observatory/burst"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/infra/conf/cfgcommon/duration"
)

type ObservatoryConfig struct {
	SubjectSelector   []string          `json:"subjectSelector"`
	ProbeURL          string            `json:"probeURL"`
	ProbeInterval     duration.Duration `json:"probeInterval"`
	EnableConcurrency bool              `json:"enableConcurrency"`
}

func (o *ObservatoryConfig) Build() (proto.Message, error) {
	return &observatory.Config{SubjectSelector: o.SubjectSelector, ProbeUrl: o.ProbeURL, ProbeInterval: int64(o.ProbeInterval), EnableConcurrency: o.EnableConcurrency}, nil
}

type BurstObservatoryConfig struct {
	// Legacy single-group. When `PingGroups` is non-empty, these are ignored.
	SubjectSelector []string             `json:"subjectSelector"`
	HealthCheck     *healthCheckSettings `json:"pingConfig,omitempty"`
	// Multi-group: each entry has its own selector + full HealthPingConfig.
	// Selectors across groups must NOT overlap (a tag must map to at most
	// one group). Overlap is reported at config-load / `xray -test` time.
	PingGroups []*pingGroupSettings `json:"pingGroups,omitempty"`
}

// pingGroupSettings mirrors burst.HealthPingGroup in JSON.
type pingGroupSettings struct {
	SubjectSelector []string             `json:"subjectSelector"`
	HealthCheck     *healthCheckSettings `json:"pingConfig,omitempty"`
}

func (g *pingGroupSettings) Build(groupIdx int) (*burst.HealthPingGroup, error) {
	if g == nil {
		return nil, errors.New("burstObservatory.pingGroups[", groupIdx, "]: null entry (remove it, or provide a valid {subjectSelector, pingConfig} object)")
	}
	if len(g.SubjectSelector) == 0 {
		return nil, errors.New("burstObservatory.pingGroups[", groupIdx, "].subjectSelector: each group must list at least one tag prefix")
	}
	if g.HealthCheck == nil {
		return nil, errors.New("burstObservatory.pingGroups[", groupIdx, "].pingConfig: each group must have a pingConfig block")
	}
	m, err := g.HealthCheck.Build()
	if err != nil {
		return nil, errors.New("burstObservatory.pingGroups[", groupIdx, "].pingConfig: ").Base(err)
	}
	return &burst.HealthPingGroup{
		SubjectSelector: g.SubjectSelector,
		PingConfig:      m.(*burst.HealthPingConfig),
	}, nil
}

func (b BurstObservatoryConfig) Build() (proto.Message, error) {
	cfg := &burst.Config{}

	if len(b.PingGroups) > 0 {
		// New-style: pingGroups overrides legacy fields. Accept but warn if
		// user also set legacy subjectSelector/pingConfig (would be silently
		// dropped by the runtime).
		if len(b.SubjectSelector) > 0 || b.HealthCheck != nil {
			return nil, errors.New("burstObservatory: `pingGroups` is set, so the top-level `subjectSelector` / `pingConfig` would be ignored. Remove them to avoid surprises.")
		}
		for i, g := range b.PingGroups {
			pb, err := g.Build(i)
			if err != nil {
				return nil, err
			}
			cfg.PingGroups = append(cfg.PingGroups, pb)
		}
		if err := burst.ValidatePingGroups(cfg.PingGroups); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	// Legacy single-group path.
	if b.HealthCheck == nil {
		return nil, errors.New("burstObservatory: requires a valid `pingConfig` (or use `pingGroups` for per-prefix configuration)")
	}
	m, err := b.HealthCheck.Build()
	if err != nil {
		return nil, err
	}
	cfg.SubjectSelector = b.SubjectSelector
	cfg.PingConfig = m.(*burst.HealthPingConfig)
	return cfg, nil
}
