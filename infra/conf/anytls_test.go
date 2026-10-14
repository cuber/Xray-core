package conf_test

import (
	"encoding/json"
	"testing"

	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/anytls"
)

func TestAnyTLSJSON(t *testing.T) {
	for _, raw := range []string{
		`{"clients":[{"email":"alice","password":"secret","level":-1}]}`,
		`{"maxSessions":-1}`,
		`{"clients":[{"email":"alice","password":""}]}`,
	} {
		var c conf.AnyTLSServerConfig
		if err := json.Unmarshal([]byte(raw), &c); err == nil {
			if _, err := c.Build(); err == nil {
				t.Fatalf("accepted invalid JSON %s", raw)
			}
		}
	}
	c := &conf.AnyTLSServerConfig{Clients: []conf.AnyTLSUserConfig{{Email: "alice", Password: "secret", Level: 7}}}
	m, err := c.Build()
	if err != nil || m.(*anytls.ServerConfig).Users[0].Level != 7 {
		t.Fatal("native user fields not preserved", err)
	}
	for _, network := range []string{"raw", "tcp", "ws", "grpc"} {
		for _, security := range []string{"none", "tls", "reality"} {
			raw := `{"tag":"test","port":14433,"protocol":"anytls","settings":{"clients":[]},"streamSettings":{"network":"` + network + `","security":"` + security + `","tlsSettings":{}}}`
			var c conf.InboundDetourConfig
			if err := json.Unmarshal([]byte(raw), &c); err != nil {
				t.Fatal(err)
			}
			_, err := c.Build()
			valid := security == "tls" && (network == "raw" || network == "tcp")
			if (err == nil) != valid {
				t.Fatalf("network=%s security=%s error=%v", network, security, err)
			}
		}
	}
	var outbound conf.OutboundDetourConfig
	if err := json.Unmarshal([]byte(`{"protocol":"anytls","settings":{}}`), &outbound); err != nil {
		t.Fatal(err)
	}
	if _, err := outbound.Build(); err == nil {
		t.Fatal("empty AnyTLS outbound settings must be rejected")
	}
}

func TestAnyTLSOutboundPoolLimitsJSON(t *testing.T) {
	var config conf.AnyTLSClientConfig
	if err := json.Unmarshal([]byte(`{"address":"anytls.test","port":443,"password":"secret","maxSessions":32,"maxIdleSessions":8,"maxConcurrentDials":4}`), &config); err != nil {
		t.Fatal(err)
	}
	built, err := config.Build()
	if err != nil {
		t.Fatal(err)
	}
	c := built.(*anytls.ClientConfig)
	if c.MaxSessions != 32 || c.MaxIdleSessions != 8 || c.MaxConcurrentDials != 4 {
		t.Fatal("pool limits not preserved")
	}
	for _, raw := range []string{
		`{"address":"anytls.test","port":443,"password":"secret","maxSessions":-1}`,
		`{"address":"anytls.test","port":443,"password":"secret","maxSessions":4097}`,
		`{"address":"anytls.test","port":443,"password":"secret","maxSessions":2,"maxConcurrentDials":3}`,
		`{"address":"anytls.test","port":443,"password":"secret","maxIdleSessions":1,"minIdleSession":2}`,
	} {
		var bad conf.AnyTLSClientConfig
		if err := json.Unmarshal([]byte(raw), &bad); err == nil {
			if _, err := bad.Build(); err == nil {
				t.Fatal("invalid pool limits accepted", raw)
			}
		}
	}
}
