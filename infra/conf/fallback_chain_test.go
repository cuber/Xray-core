package conf_test

import (
	"encoding/json"
	"strings"
	"testing"

	. "github.com/xtls/xray-core/infra/conf"
)

// Core spec017 L9 (FR-008): a fallbackTag that names both an outbound and a balancer is an error;
// one that names a balancer or an outbound alone builds.
func TestFallbackTagCollision(t *testing.T) {
	build := func(fallback string) error {
		input := `{
			"outbounds": [{"tag": "x", "protocol": "freedom"}, {"tag": "out", "protocol": "freedom"}],
			"routing": {"balancers": [
				{"tag": "x", "selector": ["out"], "fallbackTag": "out"},
				{"tag": "y", "selector": ["out"], "fallbackTag": "out"},
				{"tag": "b", "selector": ["out"], "strategy": {"type": "roundrobin"}, "fallbackTag": "` + fallback + `"}
			]}
		}`
		var cfg Config
		if err := json.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatal(err)
		}
		_, err := cfg.Build()
		return err
	}
	if err := build("x"); err == nil || !strings.Contains(err.Error(), "fallbackTag x names both an outbound and a balancer") {
		t.Fatalf("collision = %v", err)
	}
	for _, fallback := range []string{"out", "y", ""} {
		if err := build(fallback); err != nil {
			t.Fatalf("fallbackTag %q: %v", fallback, err)
		}
	}
}

// Core spec019 M1, M2 (FR-001): `balancers` builds into the rule, alone or beside a selector; an
// empty or repeated member, and a member sharing its tag with an outbound, are errors.
func TestBalancerMembersJSON(t *testing.T) {
	build := func(balancers string) (*Config, error) {
		input := `{
			"outbounds": [{"tag": "out", "protocol": "freedom"}, {"tag": "a-hy2", "protocol": "freedom"}],
			"routing": {"balancers": [
				{"tag": "route", "balancers": ` + balancers + `, "strategy": {"type": "leastping"}, "fallbackTag": "out"},
				{"tag": "a", "selector": ["a-hy2"], "strategy": {"type": "leastping"}, "fallbackTag": "a-hy2"},
				{"tag": "b", "selector": ["a-hy2"], "strategy": {"type": "leastping"}, "fallbackTag": "a-hy2"}
			]}
		}`
		var cfg Config
		if err := json.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatal(err)
		}
		_, err := cfg.Build()
		return &cfg, err
	}
	cfg, err := build(`["a", "b"]`)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := cfg.RouterConfig.Balancers[0].Build()
	if err != nil || strings.Join(rule.Balancers, ",") != "a,b" || len(rule.OutboundSelector) != 0 {
		t.Fatalf("route = %v, %v", rule, err)
	}
	for balancers, want := range map[string]string{
		`["a", "a"]`: `member "a" is empty or listed twice`,
		`[""]`:       `member "" is empty or listed twice`,
		`[]`:         "empty selector list",
	} {
		if _, err := build(balancers); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("balancers %s: %v, want %q", balancers, err, want)
		}
	}
	var collision Config
	if err := json.Unmarshal([]byte(`{
		"outbounds": [{"tag": "out", "protocol": "freedom"}, {"tag": "a", "protocol": "freedom"}],
		"routing": {"balancers": [
			{"tag": "route", "balancers": ["a"], "fallbackTag": "out"},
			{"tag": "a", "selector": ["out"], "fallbackTag": "out"}
		]}
	}`), &collision); err != nil {
		t.Fatal(err)
	}
	if _, err := collision.Build(); err == nil || !strings.Contains(err.Error(), "balancer member a names both an outbound and a balancer") {
		t.Fatalf("collision = %v", err)
	}
}
