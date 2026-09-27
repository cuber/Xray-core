package anytls_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/anytls"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/reality"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// No parallel subtests: the fixture owns Core's process-global dialer state.
func TestAnyTLSOutboundNativeValidationMatrix(t *testing.T) {
	f, good := outboundFixture(t, "")
	if err := f.alter("alice", "validation-secret", false); err != nil {
		t.Fatal(err)
	}
	client := f.client(t, "validation-secret")
	checkBusiness := func(t *testing.T, payload string) {
		t.Helper()
		if got, err := exchange(client, "alpha.test", payload); err != nil || got != "A"+payload {
			t.Fatalf("original client lost business: got=%q err=%v", got, err)
		}
	}
	checkBusiness(t, "before-validation")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	baseline, err := f.api.ListOutbounds(ctx, &handler.ListOutboundsRequest{})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	original := make(map[string]*core.OutboundHandlerConfig, len(baseline.Outbounds))
	for _, out := range baseline.Outbounds {
		original[out.Tag] = out
	}
	if original["client"] == nil {
		t.Fatal("missing original client")
	}
	tests := []struct {
		name   string
		proxy  func(*anytls.ClientConfig)
		sender func(*proxyman.SenderConfig)
		config func(*core.OutboundHandlerConfig)
	}{
		{name: "missing-server", proxy: func(c *anytls.ClientConfig) { c.Server = nil }},
		{name: "missing-address", proxy: func(c *anytls.ClientConfig) { c.Server.Address = nil }},
		{name: "unset-address", proxy: func(c *anytls.ClientConfig) { c.Server.Address = &xnet.IPOrDomain{} }},
		{name: "invalid-ip", proxy: func(c *anytls.ClientConfig) {
			c.Server.Address = &xnet.IPOrDomain{Address: &xnet.IPOrDomain_Ip{Ip: []byte{1}}}
		}},
		{name: "empty-ip", proxy: func(c *anytls.ClientConfig) {
			c.Server.Address = &xnet.IPOrDomain{Address: &xnet.IPOrDomain_Ip{}}
		}},
		{name: "empty-domain", proxy: func(c *anytls.ClientConfig) { c.Server.Address = xnet.NewIPOrDomain(xnet.DomainAddress("")) }},
		{name: "blank-domain", proxy: func(c *anytls.ClientConfig) { c.Server.Address = xnet.NewIPOrDomain(xnet.DomainAddress(" \t")) }},
		{name: "leading-domain-space", proxy: func(c *anytls.ClientConfig) {
			c.Server.Address = xnet.NewIPOrDomain(xnet.DomainAddress(" server.test"))
		}},
		{name: "trailing-domain-space", proxy: func(c *anytls.ClientConfig) {
			c.Server.Address = xnet.NewIPOrDomain(xnet.DomainAddress("server.test "))
		}},
		{name: "zero-port", proxy: func(c *anytls.ClientConfig) { c.Server.Port = 0 }},
		{name: "overflow-port", proxy: func(c *anytls.ClientConfig) { c.Server.Port = 65536 }},
		{name: "max-uint-port", proxy: func(c *anytls.ClientConfig) { c.Server.Port = math.MaxUint32 }},
		{name: "missing-user", proxy: func(c *anytls.ClientConfig) { c.Server.User = nil }},
		{name: "missing-account", proxy: func(c *anytls.ClientConfig) { c.Server.User.Account = nil }},
		{name: "empty-password", proxy: func(c *anytls.ClientConfig) { c.Server.User.Account = serial.ToTypedMessage(&anytls.Account{}) }},
		{name: "wrong-account", proxy: func(c *anytls.ClientConfig) { c.Server.User.Account = serial.ToTypedMessage(&anytls.ServerConfig{}) }},
		{name: "unset-account-type", proxy: func(c *anytls.ClientConfig) { c.Server.User.Account = &serial.TypedMessage{} }},
		{name: "unknown-account-type", proxy: func(c *anytls.ClientConfig) {
			c.Server.User.Account = &serial.TypedMessage{Type: "address.test.UnknownAccount"}
		}},
		{name: "malformed-account", proxy: func(c *anytls.ClientConfig) {
			c.Server.User.Account = serial.ToTypedMessage(&anytls.Account{Password: "secret"})
			c.Server.User.Account.Value = []byte{0xff}
		}},
		{name: "min-idle-overflow", proxy: func(c *anytls.ClientConfig) { c.MinIdleSession = uint32(math.MaxInt32) + 1 }},
		{name: "min-idle-max-uint", proxy: func(c *anytls.ClientConfig) { c.MinIdleSession = math.MaxUint32 }},
		{name: "missing-sender", config: func(c *core.OutboundHandlerConfig) { c.SenderSettings = nil }},
		{name: "missing-stream", sender: func(s *proxyman.SenderConfig) { s.StreamSettings = nil }},
		{name: "empty-stream", sender: func(s *proxyman.SenderConfig) { s.StreamSettings = &internet.StreamConfig{} }},
		{name: "no-security", sender: func(s *proxyman.SenderConfig) {
			s.StreamSettings.SecurityType = ""
			s.StreamSettings.SecuritySettings = nil
		}},
		{name: "reality-security", sender: func(s *proxyman.SenderConfig) {
			reality := serial.ToTypedMessage(&reality.Config{})
			s.StreamSettings.SecurityType = reality.Type
			s.StreamSettings.SecuritySettings = []*serial.TypedMessage{reality}
		}},
		{name: "websocket-network", sender: func(s *proxyman.SenderConfig) { s.StreamSettings.ProtocolName = "websocket" }},
		{name: "grpc-network", sender: func(s *proxyman.SenderConfig) { s.StreamSettings.ProtocolName = "grpc" }},
		{name: "hysteria-network", sender: func(s *proxyman.SenderConfig) { s.StreamSettings.ProtocolName = "hysteria" }},
		{name: "unknown-network", sender: func(s *proxyman.SenderConfig) { s.StreamSettings.ProtocolName = "invalid-transport" }},
		{name: "outer-mux", sender: func(s *proxyman.SenderConfig) { s.MultiplexSettings = &proxyman.MultiplexingConfig{Enabled: true} }},
		{name: "dynamic-origin", sender: func(s *proxyman.SenderConfig) { s.Via = xnet.NewIPOrDomain(xnet.DomainAddress("origin")) }},
		{name: "dynamic-srcip", sender: func(s *proxyman.SenderConfig) { s.Via = xnet.NewIPOrDomain(xnet.DomainAddress("srcip")) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bad := proto.Clone(good).(*core.OutboundHandlerConfig)
			bad.Tag = "bad-validation-" + tc.name
			if tc.proxy != nil {
				value, err := bad.ProxySettings.GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				tc.proxy(value.(*anytls.ClientConfig))
				bad.ProxySettings = serial.ToTypedMessage(value)
			}
			if tc.sender != nil {
				value, err := bad.SenderSettings.GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				tc.sender(value.(*proxyman.SenderConfig))
				bad.SenderSettings = serial.ToTypedMessage(value)
			}
			if tc.config != nil {
				tc.config(bad)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: bad})
			if err == nil {
				t.Fatal("invalid native configuration accepted")
			}
			if status.Code(err) == codes.DeadlineExceeded || status.Code(err) == codes.Canceled || status.Code(err) == codes.Unavailable {
				t.Fatalf("RPC transport failure is not validation rejection: %v", err)
			}
			list, err := f.api.ListOutbounds(ctx, &handler.ListOutboundsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if len(list.Outbounds) != len(original) {
				t.Fatalf("failed add changed registry size: got=%d want=%d", len(list.Outbounds), len(original))
			}
			for _, out := range list.Outbounds {
				if out.Tag == bad.Tag || original[out.Tag] == nil || !proto.Equal(out, original[out.Tag]) {
					t.Fatalf("failed add published or changed outbound %q", out.Tag)
				}
			}
			checkBusiness(t, "after-"+tc.name)
		})
	}
}

func TestAnyTLSOutboundJSONIdleIntervalBoundaries(t *testing.T) {
	for _, field := range []string{"idleSessionCheckInterval", "idleSessionTimeout"} {
		for _, tc := range []struct {
			name  string
			raw   string
			want  uint32
			valid bool
		}{
			{"omitted", "", 0, true},
			{"zero", "0", 0, true},
			{"one", "1", 1, true},
			{"five", "5", 5, true},
			{"six", "6", 6, true},
			{"thirty", "30", 30, true},
			{"max-uint", "4294967295", math.MaxUint32, true},
			{"negative", "-1", 0, false},
			{"overflow", "4294967296", 0, false},
			{"fraction", "1.5", 0, false},
			{"string", `"6"`, 0, false},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				settings := map[string]any{"address": "server.test", "port": 443, "password": "validation-secret"}
				if tc.raw != "" {
					settings[field] = json.RawMessage(tc.raw)
				}
				raw, err := json.Marshal(map[string]any{
					"protocol": "anytls", "settings": settings,
					"streamSettings": map[string]any{"network": "raw", "security": "tls", "tlsSettings": map[string]any{}},
				})
				if err != nil {
					t.Fatal(err)
				}
				var config conf.OutboundDetourConfig
				if err := json.Unmarshal(raw, &config); err != nil {
					t.Fatal(err)
				}
				built, err := config.Build()
				if !tc.valid {
					if err == nil {
						t.Fatalf("accepted invalid %s=%s", field, tc.raw)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				value, err := built.ProxySettings.GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				c := value.(*anytls.ClientConfig)
				got := c.IdleSessionCheckInterval
				other := c.IdleSessionTimeout
				if field == "idleSessionTimeout" {
					got, other = other, got
				}
				if got != tc.want || other != 0 {
					t.Fatalf("%s lost value or changed other timer: got=%d other=%d want=%d", field, got, other, tc.want)
				}
			})
		}
	}
}
