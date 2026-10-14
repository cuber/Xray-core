package conf

import "github.com/xtls/xray-core/app/stats"

type StatsConfig struct {
	DomainTraffic *DomainTrafficConfig `json:"domainTraffic"`
}

type DomainTrafficConfig struct {
	Enabled               bool   `json:"enabled"`
	BucketIntervalSeconds uint32 `json:"bucketIntervalSeconds"`
	RetentionSeconds      uint32 `json:"retentionSeconds"`
	MaxDomains            uint32 `json:"maxDomains"`
}

// Build implements Buildable.
func (c *StatsConfig) Build() (*stats.Config, error) {
	config := &stats.Config{}
	if c != nil && c.DomainTraffic != nil {
		config.DomainTraffic = &stats.DomainTrafficConfig{
			Enabled:               c.DomainTraffic.Enabled,
			BucketIntervalSeconds: c.DomainTraffic.BucketIntervalSeconds,
			RetentionSeconds:      c.DomainTraffic.RetentionSeconds,
			MaxDomains:            c.DomainTraffic.MaxDomains,
		}
	}
	return config, nil
}
