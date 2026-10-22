package conf_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/infra/conf"
)

func buildReceiver(t *testing.T, raw string) (*proxyman.ReceiverConfig, error) {
	t.Helper()
	var c conf.InboundDetourConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	built, err := c.Build()
	if err != nil {
		return nil, err
	}
	receiver, err := built.ReceiverSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	return receiver.(*proxyman.ReceiverConfig), nil
}

const ssInbound = `{"protocol":"shadowsocks","port":512,"listen":"127.0.0.1",
	"settings":{"method":"2022-blake3-aes-128-gcm","password":"MDEyMzQ1Njc4OWFiY2RlZg==",
	"clients":[{"email":"u","password":"ZmVkY2JhOTg3NjU0MzIxMA=="}]}%s}`

func TestSingMuxConfig(t *testing.T) {
	for _, c := range []struct {
		name, field string
		want        *proxyman.SingMuxConfig
		err         string
	}{
		{name: "omitted"},
		{name: "disabled", field: `,"singMux":{"enabled":false,"maxStreams":8}`},
		{name: "default", field: `,"singMux":{"enabled":true}`, want: &proxyman.SingMuxConfig{Enabled: true}},
		{name: "bounded", field: `,"singMux":{"enabled":true,"maxStreams":64}`, want: &proxyman.SingMuxConfig{Enabled: true, MaxStreams: 64}},
		{name: "over limit", field: `,"singMux":{"enabled":true,"maxStreams":4097}`, err: "maxStreams"},
	} {
		t.Run(c.name, func(t *testing.T) {
			receiver, err := buildReceiver(t, strings.Replace(ssInbound, "%s", c.field, 1))
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("error %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := receiver.SingMux
			if (got == nil) != (c.want == nil) || (got != nil && (got.Enabled != c.want.Enabled || got.MaxStreams != c.want.MaxStreams)) {
				t.Fatalf("singMux %v, want %v", got, c.want)
			}
		})
	}
}

func TestSingMuxRejectedOnAnyTLS(t *testing.T) {
	_, err := buildReceiver(t, `{"protocol":"anytls","port":2083,"listen":"127.0.0.1","settings":{"clients":[]},
		"streamSettings":{"network":"raw","security":"tls"},"singMux":{"enabled":true}}`)
	if err == nil || !strings.Contains(err.Error(), "singMux is not supported on anytls inbounds") {
		t.Fatalf("error %v, want the AnyTLS exclusion", err)
	}
}
