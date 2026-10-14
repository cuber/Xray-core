package anytls_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xtls/xray-core/infra/conf"
)

// Migration is an explicit fixture operation, not a runtime compatibility path.
func configureAnyTLSChain(t *testing.T, value map[string]any, mode, tag string) {
	t.Helper()
	stream := value["streamSettings"].(map[string]any)
	if mode == "migratedProxySettings" {
		value["proxySettings"] = map[string]any{"tag": tag}
		assertAnyTLSLegacyConfigRejected(t, value)
		delete(value, "proxySettings")
	} else if mode != "dialerProxy" {
		t.Fatalf("unknown chain mode: %q", mode)
	}
	sockopt, _ := stream["sockopt"].(map[string]any)
	if sockopt == nil {
		sockopt = make(map[string]any)
		stream["sockopt"] = sockopt
	}
	sockopt["dialerProxy"] = tag
}

func assertAnyTLSLegacyConfigRejected(t *testing.T, value map[string]any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var parsed conf.OutboundDetourConfig
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, err := parsed.Build(); err == nil || !strings.Contains(err.Error(), "proxySettings") || !strings.Contains(err.Error(), "dialerProxy") {
		t.Fatalf("legacy chain must be explicitly rejected with migration advice: %v", err)
	}
}

func assertAnyTLSLegacyChainRejected(t *testing.T, tag string) {
	t.Helper()
	assertAnyTLSLegacyConfigRejected(t, map[string]any{
		"protocol":      "anytls",
		"settings":      map[string]any{"address": "127.0.0.1", "port": 443, "password": "migration-test"},
		"proxySettings": map[string]any{"tag": tag},
		"streamSettings": map[string]any{"network": "raw", "security": "tls", "tlsSettings": map[string]any{},
			"sockopt": map[string]any{"dialerProxy": "alternate"}},
	})
}
