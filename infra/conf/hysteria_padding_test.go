package conf

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/transport/internet/hysteria"
)

// Core spec025 FR-002: hysteriaSettings.disablePadding from JSON to the transport's Config.
func TestHysteriaDisablePaddingParses(t *testing.T) {
	for raw, want := range map[string]bool{
		`{"version":2}`:                        false,
		`{"version":2,"disablePadding":false}`: false,
		`{"version":2,"disablePadding":true}`:  true,
	} {
		var c HysteriaConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		built, err := c.Build()
		if err != nil {
			t.Fatal(err)
		}
		if got := built.(*hysteria.Config).DisablePadding; got != want {
			t.Errorf("%s: disable_padding %v", raw, got)
		}
	}
	for _, raw := range []string{`{"version":2,"disablePadding":"yes"}`, `{"version":2,"disablePadding":1}`} {
		var c HysteriaConfig
		if err := json.Unmarshal([]byte(raw), &c); err == nil {
			t.Errorf("%s parsed", raw)
		}
	}
}

// The field reaches an inbound's and an outbound's stream settings; a relay outbound still cannot carry stream
// settings, so it cannot set the field (spec024 R1, spec025 R3).
func TestHysteriaDisablePaddingOnHandlers(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{
		"inbounds":[{"tag":"in","protocol":"hysteria","port":443,"settings":{"version":2,"clients":[{"auth":"a","email":"a@padding.test"}]},
			"streamSettings":{"network":"hysteria","hysteriaSettings":{"version":2,"disablePadding":true}}}],
		"outbounds":[`+relayOwner("out", "a", "", `{"keepAlivePeriod":10}`)+`]}`), &c); err != nil {
		t.Fatal(err)
	}
	built, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := built.Inbound[0].ReceiverSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	if !hysteriaTransport(receiver.(*proxyman.ReceiverConfig).StreamSettings).GetDisablePadding() {
		t.Error("the inbound's disablePadding is lost")
	}
	sender, err := built.Outbound[0].SenderSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	if hysteriaTransport(sender.(*proxyman.SenderConfig).StreamSettings).GetDisablePadding() {
		t.Error("an outbound without the field does not disable padding")
	}

	_, err = buildRelayConfig(t, relayOwner("owner", "o", relayOwnerSession, `{"keepAlivePeriod":10}`),
		`{"tag":"r","protocol":"hysteria","settings":{"version":2,"relay":{"session":"owner","auth":"u"}},`+
			`"streamSettings":{"network":"hysteria","hysteriaSettings":{"version":2,"disablePadding":true}}}`)
	if err == nil || !strings.Contains(err.Error(), "has streamSettings") {
		t.Errorf("a relay outbound with disablePadding: %v", err)
	}
}
