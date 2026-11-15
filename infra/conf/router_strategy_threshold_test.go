package conf

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/router"
)

// buildThreshold builds one balancer from JSON with the given strategy type and settings.
func buildThreshold(t *testing.T, strategyType, settings string) (*router.BalancingRule, error) {
	t.Helper()
	raw := `{"tag": "t", "selector": ["a-"], "strategy": {"type": "` + strategyType + `"`
	if settings != "" {
		raw += `, "settings": ` + settings
	}
	raw += `}, "fallbackTag": "f"}`
	var rule BalancingRule
	if err := json.Unmarshal([]byte(raw), &rule); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return rule.Build()
}

func loadedThreshold(t *testing.T, rule *router.BalancingRule) *router.StrategyThresholdLeastPingConfig {
	t.Helper()
	instance, err := rule.StrategySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	config, ok := instance.(*router.StrategyThresholdLeastPingConfig)
	if !ok {
		t.Fatalf("settings are %T", instance)
	}
	return config
}

// Core spec027 C01, C02, C04: the type in any case; exact durations; recoverRTT defaults to maxRTT.
func TestThresholdLeastPingSettingsLoad(t *testing.T) {
	for _, strategyType := range []string{"thresholdLeastPing", "thresholdleastping", "ThresholdLeastPing"} {
		rule, err := buildThreshold(t, strategyType, `{"maxRTT": "50ms"}`)
		if err != nil {
			t.Fatalf("%s: %v", strategyType, err)
		}
		if rule.Strategy != "thresholdleastping" {
			t.Fatalf("%s: strategy %q", strategyType, rule.Strategy)
		}
	}
	for _, c := range []struct {
		settings           string
		maxRTT, recoverRTT time.Duration
	}{
		{`{"maxRTT": "50ms"}`, 50 * time.Millisecond, 50 * time.Millisecond},
		{`{"maxRTT": "1s"}`, time.Second, time.Second},
		{`{"maxRTT": "500us"}`, 500 * time.Microsecond, 500 * time.Microsecond},
		{`{"maxRTT": "50.000001ms"}`, 50*time.Millisecond + time.Nanosecond, 50*time.Millisecond + time.Nanosecond},
		{`{"maxRTT": "50ms", "recoverRTT": "40ms"}`, 50 * time.Millisecond, 40 * time.Millisecond},
		{`{"maxRTT": "50ms", "recoverRTT": "50ms"}`, 50 * time.Millisecond, 50 * time.Millisecond},
	} {
		rule, err := buildThreshold(t, "thresholdLeastPing", c.settings)
		if err != nil {
			t.Fatalf("%s: %v", c.settings, err)
		}
		got := loadedThreshold(t, rule)
		if time.Duration(got.MaxRTT) != c.maxRTT || time.Duration(got.RecoverRTT) != c.recoverRTT {
			t.Fatalf("%s: loaded %v/%v, want %v/%v", c.settings,
				time.Duration(got.MaxRTT), time.Duration(got.RecoverRTT), c.maxRTT, c.recoverRTT)
		}
	}
}

// Core spec027 C03, C05: each invalid value is an error naming its field.
func TestThresholdLeastPingSettingsErrors(t *testing.T) {
	for _, c := range []struct{ settings, want string }{
		{"", "needs maxRTT"},
		{`{}`, "needs maxRTT"},
		{`{"recoverRTT": "40ms"}`, "needs maxRTT"},
		{`{"maxRTT": "0s"}`, "maxRTT must be positive"},
		{`{"maxRTT": "-1ms"}`, "maxRTT must be positive"},
		{`{"maxRTT": 50}`, "thresholdLeastPing maxRTT"},
		{`{"maxRTT": true}`, "thresholdLeastPing maxRTT"},
		{`{"maxRTT": null}`, "thresholdLeastPing maxRTT"},
		{`{"maxRTT": "50"}`, "thresholdLeastPing maxRTT"},
		{`{"maxRTT": "fast"}`, "thresholdLeastPing maxRTT"},
		{`{"maxRTT": "9999999999h"}`, "thresholdLeastPing maxRTT"},
		{`{"maxRTT": "50ms", "recoverRTT": "60ms"}`, "recoverRTT 60ms is above maxRTT 50ms"},
		{`{"maxRTT": "50ms", "recoverRTT": "0s"}`, "recoverRTT must be positive"},
		{`{"maxRTT": "50ms", "recoverRTT": "-1ms"}`, "recoverRTT must be positive"},
		{`{"maxRTT": "50ms", "recoverRTT": 40}`, "thresholdLeastPing recoverRTT"},
		{`{"maxRTT": "50ms", "recoverRTT": null}`, "thresholdLeastPing recoverRTT"},
	} {
		_, err := buildThreshold(t, "thresholdLeastPing", c.settings)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("settings %s: error %v, want %q", c.settings, err, c.want)
		}
	}
}

// Core spec027 C11 (TL-9): leastPing refuses thresholdLeastPing's settings, in any key case and even
// as null; every other setting stays ignored as before.
func TestLeastPingRefusesThresholdSettings(t *testing.T) {
	for _, settings := range []string{
		`{"maxRTT": "50ms"}`, `{"recoverRTT": "40ms"}`, `{"MAXRTT": "50ms"}`, `{"maxRTT": null}`,
		`{"maxRTT": "50ms", "recoverRTT": "40ms"}`,
	} {
		_, err := buildThreshold(t, "leastPing", settings)
		if err == nil || !strings.Contains(err.Error(), "use the thresholdLeastPing strategy") {
			t.Fatalf("leastPing settings %s: error %v", settings, err)
		}
	}
	_, err := buildThreshold(t, "leastPing", `{"maxRTT": "50ms", "recoverRTT": "40ms"}`)
	if err == nil || !strings.Contains(err.Error(), "leastPing has no maxRTT") {
		t.Fatalf("both fields: error %v, want maxRTT named first", err)
	}
	for _, settings := range []string{"", `{}`, `{"tolerance": 0.5}`, `{"expected": 2}`} {
		rule, err := buildThreshold(t, "leastPing", settings)
		if err != nil {
			t.Fatalf("leastPing settings %q: %v", settings, err)
		}
		if rule.Strategy != "leastping" || rule.StrategySettings != nil {
			t.Fatalf("leastPing settings %q: rule %v", settings, rule)
		}
	}
}
