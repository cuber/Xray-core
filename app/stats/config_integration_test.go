package stats_test

import (
	"context"
	"encoding/json"
	"testing"

	stats "github.com/xtls/xray-core/app/stats"
	"github.com/xtls/xray-core/infra/conf"
)

func TestDomainTrafficJSONConfigIntegration(t *testing.T) {
	for _, tc := range []struct {
		json    string
		enabled bool
		invalid bool
	}{
		{`{}`, false, false},
		{`{"domainTraffic":{"enabled":false}}`, false, false},
		{`{"domainTraffic":{"enabled":true}}`, true, false},
		{`{"domainTraffic":{"enabled":true,"bucketIntervalSeconds":7,"retentionSeconds":70,"maxDomains":3}}`, true, false},
		{`{"domainTraffic":{"enabled":true,"maxDomains":4097}}`, false, true},
		{`{"domainTraffic":{"enabled":true,"bucketIntervalSeconds":10,"retentionSeconds":5}}`, false, true},
	} {
		t.Run(tc.json, func(t *testing.T) {
			var input conf.StatsConfig
			if err := json.Unmarshal([]byte(tc.json), &input); err != nil {
				t.Fatal(err)
			}
			built, err := input.Build()
			if err != nil {
				t.Fatal(err)
			}
			if input.DomainTraffic != nil {
				source := input.DomainTraffic
				got := built.DomainTraffic
				if got == nil || got.Enabled != source.Enabled || got.BucketIntervalSeconds != source.BucketIntervalSeconds || got.RetentionSeconds != source.RetentionSeconds || got.MaxDomains != source.MaxDomains {
					t.Fatalf("JSON fields lost: %v", got)
				}
			}
			m, err := stats.NewManager(context.Background(), built)
			if tc.invalid {
				if err == nil {
					m.Close()
					t.Fatal("invalid config accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if m.DomainTrafficEnabled() != tc.enabled {
				t.Fatal("domain traffic enablement")
			}
			c, err := m.GetOrRegisterCounter("ordinary")
			if err != nil {
				t.Fatal(err)
			}
			if c.Add(123) != 123 {
				t.Fatal("ordinary counter disabled")
			}
		})
	}
}
