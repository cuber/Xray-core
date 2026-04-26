package conf_test

import (
	"strings"
	"testing"

	"github.com/xtls/xray-core/app/observatory/burst"
	. "github.com/xtls/xray-core/infra/conf"
)

func TestBurstObservatoryConfigPingGroupsBuild(t *testing.T) {
	creator := func() Buildable {
		return new(BurstObservatoryConfig)
	}

	runMultiTestCase(t, []TestCase{
		{
			Input: `{
				"pingGroups": [
					{
						"subjectSelector": ["us-"],
						"pingConfig": {
							"destination": "http://us.example/generate_204",
							"sampling": 3,
							"httpMethod": "GET",
							"destinationsByPrefix": {
								"us-la-": "http://la.example/generate_204"
							}
						}
					},
					{
						"subjectSelector": ["jp-"],
						"pingConfig": {
							"destination": "http://jp.example/generate_204"
						}
					}
				]
			}`,
			Parser: loadJSON(creator),
			Output: &burst.Config{
				PingGroups: []*burst.HealthPingGroup{
					{
						SubjectSelector: []string{"us-"},
						PingConfig: &burst.HealthPingConfig{
							Destination:   "http://us.example/generate_204",
							SamplingCount: 3,
							HttpMethod:    "GET",
							DestinationsByPrefix: map[string]string{
								"us-la-": "http://la.example/generate_204",
							},
						},
					},
					{
						SubjectSelector: []string{"jp-"},
						PingConfig: &burst.HealthPingConfig{
							Destination: "http://jp.example/generate_204",
							HttpMethod:  "HEAD",
						},
					},
				},
			},
		},
	})
}

func TestBurstObservatoryConfigPingGroupsRejectsLegacyMix(t *testing.T) {
	_, err := loadJSON(func() Buildable { return new(BurstObservatoryConfig) })(`{
		"subjectSelector": ["legacy-"],
		"pingConfig": {"destination": "http://legacy.example/generate_204"},
		"pingGroups": [{
			"subjectSelector": ["us-"],
			"pingConfig": {"destination": "http://us.example/generate_204"}
		}]
	}`)
	if err == nil {
		t.Fatal("expected pingGroups plus legacy fields to fail")
	}
	if !strings.Contains(err.Error(), "would be ignored") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBurstObservatoryConfigPingGroupsRejectsOverlap(t *testing.T) {
	_, err := loadJSON(func() Buildable { return new(BurstObservatoryConfig) })(`{
		"pingGroups": [
			{
				"subjectSelector": ["us-"],
				"pingConfig": {"destination": "http://us.example/generate_204"}
			},
			{
				"subjectSelector": ["us-la-"],
				"pingConfig": {"destination": "http://la.example/generate_204"}
			}
		]
	}`)
	if err == nil {
		t.Fatal("expected overlapping pingGroups to fail")
	}
	if !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("unexpected error: %v", err)
	}
}
