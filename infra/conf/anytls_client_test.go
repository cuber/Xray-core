package conf_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/anytls"
)

func TestAnyTLSClientJSON(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"address":"server.test","port":443}`,
		`{"address":"server.test","port":65536,"password":"secret"}`,
		`{"address":"server.test","port":-1,"password":"secret"}`,
		`{"address":"server.test","port":443,"password":"secret","level":-1}`,
		`{"address":"server.test","port":443,"password":"secret","idleSessionTimeout":-1}`,
		`{"address":"server.test","port":443,"password":"secret","idleSessionTimeout":4294967296}`,
		`{"address":"server.test","port":443,"password":"secret","minIdleSession":2147483648}`,
	} {
		var c conf.AnyTLSClientConfig
		if err := json.Unmarshal([]byte(raw), &c); err == nil {
			if _, err := c.Build(); err == nil {
				t.Fatalf("accepted invalid JSON: %s", raw)
			}
		}
	}
	var c conf.AnyTLSClientConfig
	if err := json.Unmarshal([]byte(`{"address":"server.test","port":2083,"password":"opaque:password","email":"label","level":7,"idleSessionTimeout":60,"minIdleSession":2}`), &c); err != nil {
		t.Fatal(err)
	}
	m, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	config := m.(*anytls.ClientConfig)
	if config.Server.Port != 2083 || config.Server.User.Level != 7 || config.Server.User.Email != "label" || config.IdleSessionTimeout != 60 || config.MinIdleSession != 2 {
		t.Fatal("outbound fields lost", config)
	}
	account, err := config.Server.User.Account.GetInstance()
	if err != nil || account.(*anytls.Account).Password != "opaque:password" {
		t.Fatal("opaque password changed", err)
	}
}

func TestAnyTLSOutboundSenderJSON(t *testing.T) {
	for _, network := range []string{"raw", "tcp", "ws", "grpc"} {
		for _, security := range []string{"none", "tls", "reality"} {
			for _, mux := range []bool{false, true} {
				raw := fmt.Sprintf(`{"protocol":"anytls","settings":{"address":"server.test","port":443,"password":"secret"},"streamSettings":{"network":%q,"security":%q,"tlsSettings":{}},"mux":{"enabled":%t}}`, network, security, mux)
				var c conf.OutboundDetourConfig
				if err := json.Unmarshal([]byte(raw), &c); err != nil {
					t.Fatal(err)
				}
				_, err := c.Build()
				valid := (network == "raw" || network == "tcp") && security == "tls" && !mux
				if (err == nil) != valid {
					t.Fatalf("network=%s security=%s mux=%v: %v", network, security, mux, err)
				}
			}
		}
	}
}

func TestAnyTLSOutboundRejectsDynamicSourceJSON(t *testing.T) {
	for _, source := range []string{"origin", "srcip"} {
		raw := fmt.Sprintf(`{"protocol":"anytls","sendThrough":%q,"settings":{"address":"server.test","port":443,"password":"secret"},"streamSettings":{"network":"raw","security":"tls","tlsSettings":{}}}`, source)
		var config conf.OutboundDetourConfig
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Build(); err == nil || !strings.Contains(err.Error(), "fixed sendThrough") {
			t.Fatalf("dynamic source %s rejection: %v", source, err)
		}
	}
}
