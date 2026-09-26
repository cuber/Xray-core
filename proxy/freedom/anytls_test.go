package freedom

import (
	"context"
	"testing"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
)

func TestAnyTLSDefaultPrivateAddressPolicy(t *testing.T) {
	h := &Handler{config: &Config{}}
	matcher := h.getBlockedIPMatcher(context.Background(), &session.Inbound{Name: "anytls"})
	if !isBlockedAddress(matcher, net.ParseAddress("127.0.0.1")) || !isBlockedAddress(matcher, net.ParseAddress("10.0.0.1")) {
		t.Fatal("AnyTLS bypassed the default private-address policy")
	}
	h.config.IpsBlocked = &IPRules{}
	if h.getBlockedIPMatcher(context.Background(), &session.Inbound{Name: "anytls"}) != nil {
		t.Fatal("explicit ipsBlocked override ignored")
	}
}
