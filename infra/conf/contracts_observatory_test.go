package conf_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
				if h.Settings.Interval != 3*time.Second || h.Settings.Timeout != 2*time.Second || h.Settings.SamplingCount != 20 || h.Settings.KeepAlive {
					t.Fatalf("3s/2s/20/default keepAlive not preserved: %+v", h.Settings)
				}
			}
		})
	}
}

func TestContractBurstJSONIndependentSettings(t *testing.T) {
	var config BurstObservatoryConfig
	input := `{"pingGroups":[{"subjectSelector":["a"],"pingConfig":{"destination":"http://a.example/a?ob=x","interval":"3s","timeout":"2s","sampling":20,"httpMethod":"GET","keepAlive":true}},{"subjectSelector":["b"],"pingConfig":{"destination":"http://b.example/b?ob=y","interval":"7s","timeout":"1s","sampling":2}}]}`
	if err := json.Unmarshal([]byte(input), &config); err != nil {
		t.Fatal(err)
	}
	result, err := config.Build()
	if err != nil {
		t.Fatal(err)
	}
	m := burst.NewManager(context.Background(), nil, result.(*burst.Config).PingGroups)
	a, b := m.Groups()[0].Settings, m.Groups()[1].Settings
	if a.Destination != "http://a.example/a?ob=x" || a.Interval != 3*time.Second || a.Timeout != 2*time.Second || a.SamplingCount != 20 || !a.KeepAlive || a.HttpMethod != "GET" {
		t.Fatalf("A=%+v", a)
	}
	if b.Destination != "http://b.example/b?ob=y" || b.Interval != 7*time.Second || b.Timeout != time.Second || b.SamplingCount != 2 || b.KeepAlive || b.HttpMethod != "HEAD" {
		t.Fatalf("B=%+v", b)
	}
}
