package conf_test

import (
	"testing"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/freedom"
)

func TestFreedomUnixRedirectConfig(t *testing.T) {
	c := &conf.FreedomConfig{
		Redirect:   "unix:/run/xray-sidecar/probe.sock",
		FinalRules: []*conf.FreedomFinalRuleConfig{{Action: "allow"}},
	}
	built, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	f := built.(*freedom.Config)
	dest := f.DestinationOverride
	if dest.Network != net.Network_UNIX || dest.Server.Address.AsAddress().String() != "/run/xray-sidecar/probe.sock" || dest.Server.Port != 0 {
		t.Fatalf("unexpected Unix override: %v", dest)
	}
	if len(f.FinalRules) != 1 || f.FinalRules[0].Action != freedom.RuleAction_Allow {
		t.Fatalf("upstream final rules lost: %v", f.FinalRules)
	}
}
