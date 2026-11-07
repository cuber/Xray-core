package conf

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xtls/xray-core/transport/internet"
)

// spec021 FR-005: the loss recovery and copy fields from JSON to proto, with their ranges.

func buildQuicParams(t *testing.T, quicParams string) (*internet.QuicParams, error) {
	t.Helper()
	var c StreamConfig
	if err := json.Unmarshal([]byte(`{"network":"hysteria","finalmask":{"quicParams":`+quicParams+`}}`), &c); err != nil {
		return nil, err
	}
	built, err := c.Build()
	if err != nil {
		return nil, err
	}
	return built.QuicParams, nil
}

func TestQuicParamsLossRecoveryUnset(t *testing.T) {
	p, err := buildQuicParams(t, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxAckDelayMs != 0 || p.AckElicitingThreshold != 0 || p.InitialRttMs != 0 || p.PtoBackoffLimit != 0 ||
		p.LossTimeThreshold != 0 || p.LossPacketThreshold != 0 || p.DuplicateMaxSize != 0 || p.DuplicateTail || p.DuplicateDelayMs != 0 {
		t.Fatalf("unset fields must stay zero: %+v", p)
	}
}

func TestQuicParamsLossRecoveryFields(t *testing.T) {
	p, err := buildQuicParams(t, `{"maxAckDelayMs":2,"ackElicitingThreshold":1,"initialRttMs":15,"ptoBackoffLimit":1,
		"lossTimeThreshold":1.25,"lossPacketThreshold":4,"duplicateMaxSize":300,"duplicateTail":true,"duplicateDelayMs":3}`)
	if err != nil {
		t.Fatal(err)
	}
	want := &internet.QuicParams{
		BbrProfile:            "standard",
		MaxAckDelayMs:         2,
		AckElicitingThreshold: 1,
		InitialRttMs:          15,
		PtoBackoffLimit:       1,
		LossTimeThreshold:     1.25,
		LossPacketThreshold:   4,
		DuplicateMaxSize:      300,
		DuplicateTail:         true,
		DuplicateDelayMs:      3,
	}
	if p.String() != want.String() {
		t.Fatalf("got %v, want %v", p, want)
	}
}

func TestQuicParamsLossRecoveryRanges(t *testing.T) {
	for _, valid := range []string{
		`{"maxAckDelayMs":1}`, `{"maxAckDelayMs":16382}`, `{"ackElicitingThreshold":64}`, `{"initialRttMs":10000}`,
		`{"ptoBackoffLimit":16}`, `{"lossTimeThreshold":1}`, `{"lossTimeThreshold":4}`, `{"lossPacketThreshold":64}`,
		`{"duplicateMaxSize":65535}`, `{"duplicateTail":true,"duplicateDelayMs":1000}`,
	} {
		if _, err := buildQuicParams(t, valid); err != nil {
			t.Errorf("%s: %v", valid, err)
		}
	}
	for invalid, message := range map[string]string{
		`{"maxAckDelayMs":16383}`:                        "maxAckDelayMs",
		`{"ackElicitingThreshold":65}`:                   "ackElicitingThreshold",
		`{"initialRttMs":10001}`:                         "initialRttMs",
		`{"ptoBackoffLimit":17}`:                         "ptoBackoffLimit",
		`{"lossTimeThreshold":0.5}`:                      "lossTimeThreshold",
		`{"lossTimeThreshold":4.5}`:                      "lossTimeThreshold",
		`{"lossPacketThreshold":65}`:                     "lossPacketThreshold",
		`{"duplicateMaxSize":65536}`:                     "duplicateMaxSize",
		`{"duplicateDelayMs":5}`:                         "requires duplicateMaxSize or duplicateTail",
		`{"duplicateTail":true,"duplicateDelayMs":1001}`: "duplicateDelayMs",
		`{"maxAckDelayMs":-1}`:                           "",
	} {
		_, err := buildQuicParams(t, invalid)
		if err == nil || !strings.Contains(err.Error(), message) {
			t.Errorf("%s: got %v, want an error about %q", invalid, err, message)
		}
	}
}

// spec021 R1 (user, 2026-11-07: "需要 check fail 出来，显示disable，不要做隐式操作"): maxAckDelayMs on a
// Chrome-parroting QUIC dialer fails, and the outbound turns parroting off explicitly; inbounds do not parrot.
func TestQuicParamsMaxAckDelayNeedsExplicitParrotOff(t *testing.T) {
	outbound := func(stream string) error {
		var c OutboundDetourConfig
		if err := json.Unmarshal([]byte(`{"protocol":"freedom","streamSettings":`+stream+`}`), &c); err != nil {
			t.Fatal(err)
		}
		_, err := c.Build()
		return err
	}
	quic := func(network, security, quicParams string) string {
		return `{"network":"` + network + `",` + security + `"finalmask":{"quicParams":` + quicParams + `}}`
	}
	const h3 = `"security":"tls","tlsSettings":{"alpn":["h3"]},`
	const h2 = `"security":"tls","tlsSettings":{"alpn":["h2"]},`
	for _, tc := range []struct {
		name, stream string
		fails        bool
	}{
		{"hysteria", quic("hysteria", "", `{"maxAckDelayMs":2}`), true},
		{"hysteria, parroting off", quic("hysteria", "", `{"maxAckDelayMs":2,"disableChromeParrot":true}`), false},
		{"hysteria, other fields", quic("hysteria", "", `{"ptoBackoffLimit":1,"duplicateTail":true}`), false},
		{"xhttp over HTTP/3", quic("xhttp", h3, `{"maxAckDelayMs":2}`), true},
		{"xhttp over HTTP/3, parroting off", quic("xhttp", h3, `{"maxAckDelayMs":2,"disableChromeParrot":true}`), false},
		{"xhttp over HTTP/2 has no QUIC", quic("xhttp", h2, `{"maxAckDelayMs":2}`), false},
		{"xhttp without TLS has no QUIC", quic("xhttp", "", `{"maxAckDelayMs":2}`), false},
		{"raw", quic("raw", "", `{"maxAckDelayMs":2}`), false},
		{"xhttp downloadSettings over HTTP/3", `{"network":"xhttp",` + h2 + `"xhttpSettings":{"mode":"packet-up","downloadSettings":` +
			quic("xhttp", h3, `{"maxAckDelayMs":2}`) + `}}`, true},
		{"xhttp downloadSettings, parroting off", `{"network":"xhttp",` + h2 + `"xhttpSettings":{"mode":"packet-up","downloadSettings":` +
			quic("xhttp", h3, `{"maxAckDelayMs":2,"disableChromeParrot":true}`) + `}}`, false},
	} {
		err := outbound(tc.stream)
		if tc.fails != (err != nil) || (err != nil && !strings.Contains(err.Error(), "requires disableChromeParrot: true")) {
			t.Errorf("%s: got %v", tc.name, err)
		}
	}
	if _, err := buildQuicParams(t, `{"maxAckDelayMs":2}`); err != nil {
		t.Errorf("stream settings alone, as an inbound builds them: %v", err)
	}
}
