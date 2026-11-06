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
