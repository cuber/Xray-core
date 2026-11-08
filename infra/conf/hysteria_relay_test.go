package conf

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/hysteria/account"
	"github.com/xtls/xray-core/transport/internet/hysteria"

	hysteriaproxy "github.com/xtls/xray-core/proxy/hysteria"
)

const relayOwnerSession = `{"minConnections":1,"maxConnections":4,"streamsPerConnection":256,"scaleUpStreams":128}`

func relayOwner(tag, auth, session, quicParams string) string {
	hs := `{"version":2,"auth":"` + auth + `"`
	if session != "" {
		hs += `,"session":` + session
	}
	hs += `}`
	return `{"tag":"` + tag + `","protocol":"hysteria","settings":{"version":2,"address":"127.0.0.1","port":443},` +
		`"streamSettings":{"network":"hysteria","security":"tls","tlsSettings":{"serverName":"relay.invalid"},` +
		`"hysteriaSettings":` + hs + `,"finalmask":{"quicParams":` + quicParams + `}}}`
}

func relayOutbound(tag, session, auth string) string {
	return `{"tag":"` + tag + `","protocol":"hysteria","settings":{"version":2,"relay":{"session":"` + session + `","auth":"` + auth + `"}}}`
}

func buildRelayConfig(t *testing.T, outbounds ...string) (*core.Config, error) {
	t.Helper()
	var c Config
	if err := json.Unmarshal([]byte(`{"outbounds":[`+strings.Join(outbounds, ",")+`]}`), &c); err != nil {
		t.Fatal(err)
	}
	return c.Build()
}

func relayClient(t *testing.T, oc *core.OutboundHandlerConfig) *hysteriaproxy.ClientConfig {
	t.Helper()
	instance, err := oc.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	return instance.(*hysteriaproxy.ClientConfig)
}

func ownerSession(t *testing.T, oc *core.OutboundHandlerConfig) *hysteria.Session {
	t.Helper()
	instance, err := oc.SenderSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	return hysteriaTransport(instance.(*proxyman.SenderConfig).StreamSettings).GetSession()
}

const keepAlive = `{"congestion":"bbr","keepAlivePeriod":10}`

func TestHysteriaRelayLinksOutboundsToTheirOwner(t *testing.T) {
	config, err := buildRelayConfig(t,
		relayOwner("owner", "owner-auth", relayOwnerSession, keepAlive),
		relayOutbound("r1", "owner", "user-1"),
		`{"tag":"direct","protocol":"freedom"}`,
		relayOutbound("r2", "owner", "user-2"),
	)
	if err != nil {
		t.Fatal(err)
	}
	session := ownerSession(t, config.Outbound[0])
	if session.MinConnections != 1 || session.MaxConnections != 4 || session.StreamsPerConnection != 256 || session.ScaleUpStreams != 128 {
		t.Fatalf("session %+v", session)
	}
	if got := strings.Join(session.RelayAuth, ","); got != "user-1,user-2" {
		t.Fatalf("relay credentials in negotiation order: %s", got)
	}
	for i, tag := range []string{"r1", "r2"} {
		oc := config.Outbound[map[string]int{"r1": 1, "r2": 3}[tag]]
		relay := relayClient(t, oc).Relay
		if relay.Session != "owner" || relay.Index != uint32(i+1) || relay.Auth != fmt.Sprintf("user-%d", i+1) {
			t.Errorf("%s: %+v", tag, relay)
		}
		if relayClient(t, oc).Server != nil {
			t.Errorf("%s: a relay outbound has no server", tag)
		}
	}
}

func TestHysteriaSessionWithoutRelaysAndPlainOutboundsStayAsTheyWere(t *testing.T) {
	config, err := buildRelayConfig(t,
		relayOwner("owner", "owner-auth", relayOwnerSession, keepAlive),
		relayOwner("plain", "plain-auth", "", `{"congestion":"bbr"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := ownerSession(t, config.Outbound[0]).RelayAuth; len(got) != 0 {
		t.Errorf("an owner without relay outbounds negotiates nothing: %v", got)
	}
	if ownerSession(t, config.Outbound[1]) != nil {
		t.Error("a Hy2 outbound without session has no session")
	}
}

func TestHysteriaRelayRefusals(t *testing.T) {
	owner := relayOwner("owner", "owner-auth", relayOwnerSession, keepAlive)
	many := []string{owner}
	for i := 0; i < 256; i++ {
		many = append(many, relayOutbound(fmt.Sprintf("r%d", i), "owner", fmt.Sprintf("user-%d", i)))
	}
	for _, tc := range []struct {
		name      string
		outbounds []string
		want      string
	}{
		{"relay with streamSettings", []string{owner, `{"tag":"r","protocol":"hysteria","settings":{"version":2,"relay":{"session":"owner","auth":"u"}},` +
			`"streamSettings":{"network":"hysteria","hysteriaSettings":{"version":2}}}`}, "has streamSettings"},
		{"relay with an address", []string{owner, `{"tag":"r","protocol":"hysteria","settings":{"version":2,"address":"127.0.0.1","port":443,` +
			`"relay":{"session":"owner","auth":"u"}}}`}, "no address or port"},
		{"relay without auth", []string{owner, relayOutbound("r", "owner", "")}, "relay.auth"},
		{"relay without session", []string{owner, relayOutbound("r", "", "u")}, "relay.session"},
		{"unknown session", []string{owner, relayOutbound("r", "nobody", "u")}, "not a Hy2 outbound with hysteriaSettings.session"},
		{
			"session without pool",
			[]string{relayOwner("owner", "owner-auth", "", keepAlive), relayOutbound("r", "owner", "u")},
			"not a Hy2 outbound with hysteriaSettings.session",
		},
		{
			"session on a freedom outbound",
			[]string{`{"tag":"owner","protocol":"freedom"}`, relayOutbound("r", "owner", "u")},
			"not a Hy2 outbound with hysteriaSettings.session",
		},
		{"relays do not chain", []string{owner, relayOutbound("r1", "owner", "u1"), relayOutbound("r2", "r1", "u2")}, "do not chain"},
		{"the owner's credential", []string{owner, relayOutbound("r", "owner", "owner-auth")}, "its session owner's credential"},
		{"a credential twice", []string{owner, relayOutbound("r1", "owner", "u"), relayOutbound("r2", "owner", "u")}, "same credential"},
		{"more than 255 relays", many, "more than 255 relay outbounds"},
		{"no keepalive", []string{relayOwner("owner", "a", relayOwnerSession, `{"congestion":"bbr"}`)}, "keepAlivePeriod"},
		{"keepalive not below the idle timeout", []string{relayOwner("owner", "a", relayOwnerSession,
			`{"congestion":"bbr","keepAlivePeriod":20,"maxIdleTimeout":20}`)}, "keepAlivePeriod"},
		{
			"minConnections 0",
			[]string{relayOwner("owner", "a", `{"minConnections":0,"maxConnections":4,"streamsPerConnection":256,"scaleUpStreams":128}`, keepAlive)},
			"minConnections",
		},
		{
			"maxConnections below min",
			[]string{relayOwner("owner", "a", `{"minConnections":2,"maxConnections":1,"streamsPerConnection":256,"scaleUpStreams":128}`, keepAlive)},
			"maxConnections",
		},
		{
			"maxConnections above 16",
			[]string{relayOwner("owner", "a", `{"minConnections":1,"maxConnections":17,"streamsPerConnection":256,"scaleUpStreams":128}`, keepAlive)},
			"maxConnections",
		},
		{
			"streamsPerConnection below 16",
			[]string{relayOwner("owner", "a", `{"minConnections":1,"maxConnections":4,"streamsPerConnection":8,"scaleUpStreams":4}`, keepAlive)},
			"streamsPerConnection",
		},
		{
			"scaleUpStreams above the target",
			[]string{relayOwner("owner", "a", `{"minConnections":1,"maxConnections":4,"streamsPerConnection":256,"scaleUpStreams":300}`, keepAlive)},
			"scaleUpStreams",
		},
	} {
		_, err := buildRelayConfig(t, tc.outbounds...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error with %q", tc.name, err, tc.want)
		}
	}
}

func TestHysteriaSessionIsNoInboundSetting(t *testing.T) {
	var c InboundDetourConfig
	if err := json.Unmarshal([]byte(`{"protocol":"hysteria","port":443,"settings":{"version":2,"clients":[{"auth":"a","email":"a"}]},`+
		`"streamSettings":{"network":"hysteria","hysteriaSettings":{"version":2,"session":`+relayOwnerSession+`},`+
		`"finalmask":{"quicParams":`+keepAlive+`}}}`), &c); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Build(); err == nil || !strings.Contains(err.Error(), "not to an inbound") {
		t.Fatalf("got %v", err)
	}
}

func TestHysteriaServerRelayLists(t *testing.T) {
	build := func(clients string) (*hysteriaproxy.ServerConfig, error) {
		var c HysteriaServerConfig
		if err := json.Unmarshal([]byte(`{"version":2,"clients":`+clients+`}`), &c); err != nil {
			t.Fatal(err)
		}
		built, err := c.Build()
		if err != nil {
			return nil, err
		}
		return built.(*hysteriaproxy.ServerConfig), nil
	}
	config, err := build(`[{"auth":"t","email":"trunk","relay":["a","b"]},{"auth":"a","email":"a"},{"auth":"b","email":"b"}]`)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := config.Users[0].Account.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(instance.(*account.Account).Relay, ","); got != "a,b" {
		t.Errorf("relay list %s", got)
	}
	for _, tc := range []struct{ clients, want string }{
		{`[{"auth":"t","email":"trunk","relay":["x"]},{"auth":"a","email":"a"}]`, "not a client of this inbound"},
		{`[{"auth":"t","email":"trunk","relay":["trunk"]}]`, "lists itself"},
		{`[{"auth":"t","email":"trunk","relay":["a","a"]},{"auth":"a","email":"a"}]`, "twice"},
		{`[{"auth":"t","relay":["a"]},{"auth":"a","email":"a"}]`, "needs an email"},
	} {
		if _, err := build(tc.clients); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.clients, err, tc.want)
		}
	}
}
