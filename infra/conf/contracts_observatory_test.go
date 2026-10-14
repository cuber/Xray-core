package conf_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/xtls/xray-core/app/observatory/burst"
	. "github.com/xtls/xray-core/infra/conf"
)

func TestContractBurstJSONGroups(t *testing.T) {
	for _, tc := range []struct{ name, input, err string }{
		{"legacyEmpty", `{"pingConfig":{"interval":"3s","timeout":"2s","sampling":20}}`, ""},
		{"nestedSameGroup", `{"pingGroups":[{"subjectSelector":["a","a-b"],"pingConfig":{}}]}`, ""},
		{"duplicateSameGroup", `{"pingGroups":[{"subjectSelector":["a","a"],"pingConfig":{}}]}`, "duplicate"},
		{"sameAcrossGroups", `{"pingGroups":[{"subjectSelector":["a"],"pingConfig":{}},{"subjectSelector":["a"],"pingConfig":{}}]}`, "conflicts"},
		{"nestedAcrossGroups", `{"pingGroups":[{"subjectSelector":["a"],"pingConfig":{}},{"subjectSelector":["a-b"],"pingConfig":{}}]}`, "conflicts"},
		{"nullGroup", `{"pingGroups":[null]}`, "null entry"},
		{"emptySelector", `{"pingGroups":[{"subjectSelector":[],"pingConfig":{}}]}`, "at least one"},
		{"missingPing", `{"pingGroups":[{"subjectSelector":["a"]}]}`, "pingConfig"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config BurstObservatoryConfig
			if err := json.Unmarshal([]byte(tc.input), &config); err != nil {
				t.Fatal(err)
			}
			result, err := config.Build()
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error=%v want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "legacyEmpty" {
				cfg := result.(*burst.Config)
				if len(cfg.SubjectSelector) != 0 {
					t.Fatalf("legacy selector=%v", cfg.SubjectSelector)
				}
				h := burst.NewHealthPing(context.Background(), nil, cfg.PingConfig)
				if h.Settings.Interval != 3*time.Second || h.Settings.Timeout != 2*time.Second || h.Settings.SamplingCount != 20 || h.Settings.KeepAlive || h.Settings.HTTP2 {
					t.Fatalf("3s/2s/20/default keepAlive not preserved: %+v", h.Settings)
				}
			}
		})
	}
}

func TestContractBurstJSONIndependentSettings(t *testing.T) {
	var config BurstObservatoryConfig
	input := `{"pingGroups":[{"subjectSelector":["a"],"pingConfig":{"destination":"http://a.example/a?ob=x","interval":"3s","timeout":"2s","sampling":20,"httpMethod":"GET","keepAlive":true,"http2":true}},{"subjectSelector":["b"],"pingConfig":{"destination":"http://b.example/b?ob=y","interval":"7s","timeout":"1s","sampling":2}}]}`
	if err := json.Unmarshal([]byte(input), &config); err != nil {
		t.Fatal(err)
	}
	result, err := config.Build()
	if err != nil {
		t.Fatal(err)
	}
	m := burst.NewManager(context.Background(), nil, result.(*burst.Config).PingGroups)
	a, b := m.Groups()[0].Settings, m.Groups()[1].Settings
	if a.Destination != "http://a.example/a?ob=x" || a.Interval != 3*time.Second || a.Timeout != 2*time.Second || a.SamplingCount != 20 || !a.KeepAlive || !a.HTTP2 || a.HttpMethod != "GET" {
		t.Fatalf("A=%+v", a)
	}
	if b.Destination != "http://b.example/b?ob=y" || b.Interval != 7*time.Second || b.Timeout != time.Second || b.SamplingCount != 2 || b.KeepAlive || b.HTTP2 || b.HttpMethod != "HEAD" {
		t.Fatalf("B=%+v", b)
	}
}

func TestContractBurstHTTP2Config(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			input := `{"subjectSelector":["probe"],"pingConfig":{"http2":` + fmt.Sprint(enabled) + `}}`
			if grouped {
				input = `{"pingGroups":[` + input + `]}`
			}
			var config BurstObservatoryConfig
			if err := json.Unmarshal([]byte(input), &config); err != nil {
				t.Fatal(err)
			}
			built, err := config.Build()
			if err != nil {
				t.Fatal(err)
			}
			wire, err := proto.Marshal(built)
			if err != nil {
				t.Fatal(err)
			}
			var decoded burst.Config
			if err := proto.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			ping := decoded.PingConfig
			if grouped {
				ping = decoded.PingGroups[0].PingConfig
			}
			settings := burst.NewHealthPing(context.Background(), nil, ping).Settings
			if ping.GetHttp2() != enabled || settings.HTTP2 != enabled || settings.KeepAlive {
				t.Fatalf("grouped=%v enabled=%v settings=%+v", grouped, enabled, settings)
			}
		}
	}
	var config BurstObservatoryConfig
	if err := json.Unmarshal([]byte(`{"pingConfig":{"http2":"true"}}`), &config); err == nil {
		t.Fatal("http2 must be a boolean")
	}
}

func TestContractBurstProbeOutboundConfig(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		for _, tag := range []string{"", "sidecar-uds", " sidecar-uds "} {
			input := `{"subjectSelector":["hkt-socks"],"pingConfig":{"probeOutboundTag":"` + tag + `"}}`
			if grouped {
				input = `{"pingGroups":[` + input + `]}`
			}
			var config BurstObservatoryConfig
			if err := json.Unmarshal([]byte(input), &config); err != nil {
				t.Fatal(err)
			}
			built, err := config.Build()
			if err != nil {
				t.Fatal(err)
			}
			wire, err := proto.Marshal(built)
			if err != nil {
				t.Fatal(err)
			}
			var decoded burst.Config
			if err := proto.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			ping := decoded.PingConfig
			if grouped {
				ping = decoded.PingGroups[0].PingConfig
			}
			settings := burst.NewHealthPing(context.Background(), nil, ping).Settings
			if ping.ProbeOutboundTag != strings.TrimSpace(tag) || settings.ProbeOutboundTag != strings.TrimSpace(tag) {
				t.Fatalf("grouped=%v tag=%q settings=%+v", grouped, tag, settings)
			}
		}
	}
	var config BurstObservatoryConfig
	if err := json.Unmarshal([]byte(`{"pingConfig":{"probeOutboundTag":true}}`), &config); err == nil {
		t.Fatal("probeOutboundTag must be a string")
	}
}
