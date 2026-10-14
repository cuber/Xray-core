package conf

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory/burst"
	"google.golang.org/protobuf/proto"
)

func TestObservatoryRetryConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		bad         bool
		retries     int
	}{
		{"absent", `{"subjectSelector":["a"],"pingConfig":{}}`, false, 0},
		{"legacy", `{"subjectSelector":["a"],"pingConfig":{"retry":{"maxRetries":2}}}`, false, 2},
		{"groups", `{"pingGroups":[{"subjectSelector":["a"],"pingConfig":{"retry":{"maxRetries":2,"initialBackoff":"10ms","maxBackoff":"20ms"}}},{"subjectSelector":["b"],"pingConfig":{}}]}`, false, 2},
		{"negative", `{"subjectSelector":["a"],"pingConfig":{"retry":{"maxRetries":-1}}}`, true, 0},
		{"too_many", `{"subjectSelector":["a"],"pingConfig":{"retry":{"maxRetries":4}}}`, true, 0},
		{"negative_delay", `{"subjectSelector":["a"],"pingConfig":{"retry":{"initialBackoff":"-1s"}}}`, true, 0},
		{"inverted", `{"subjectSelector":["a"],"pingConfig":{"retry":{"initialBackoff":"1s","maxBackoff":"10ms"}}}`, true, 0},
		{"non_idempotent", `{"subjectSelector":["a"],"pingConfig":{"httpMethod":"POST","retry":{"maxRetries":2}}}`, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg BurstObservatoryConfig
			if err := json.Unmarshal([]byte(tc.input), &cfg); err != nil {
				t.Fatal(err)
			}
			built, err := cfg.Build()
			if (err != nil) != tc.bad {
				t.Fatalf("Build err=%v", err)
			}
			if err != nil {
				return
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
			if len(decoded.PingGroups) > 0 {
				ping = decoded.PingGroups[0].PingConfig
				if decoded.PingGroups[1].PingConfig.Retry != nil {
					t.Fatal("group settings leaked")
				}
			}
			h := burst.NewHealthPing(context.Background(), nil, ping)
			if h.Settings.Retry.MaxRetries != tc.retries {
				t.Fatalf("runtime retry=%+v", h.Settings.Retry)
			}
			if tc.name == "legacy" && (h.Settings.Retry.InitialBackoff != 100*time.Millisecond || h.Settings.Retry.MaxBackoff != 400*time.Millisecond) {
				t.Fatalf("defaults=%+v", h.Settings.Retry)
			}
		})
	}
	if err := burst.ValidatePingGroups([]*burst.HealthPingGroup{{SubjectSelector: []string{"a"}, PingConfig: &burst.HealthPingConfig{Retry: &burst.ProbeRetryConfig{MaxRetries: 4}}}}); err == nil {
		t.Fatal("invalid protobuf retry accepted")
	}
}
