package conf_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xtls/xray-core/app/observatory/burst"
	. "github.com/xtls/xray-core/infra/conf"
	"google.golang.org/protobuf/proto"
)

// trackConfig is a whole configuration: two probed AHK paths, two routes and a track (spec014).
func trackConfig(track string) string {
	return `{
		"outbounds": [
			{"tag": "ahk.ecs-hy2", "protocol": "freedom"},
			{"tag": "ahk.alpine.1st-hy2", "protocol": "freedom"},
			{"tag": "tk.auto-hy2", "protocol": "freedom"},
			{"tag": "sg.auto-hy2", "protocol": "freedom"}
		],
		"burstObservatory": {
			"pingGroups": [{"subjectSelector": ["ahk."], "pingConfig": {"destination": "http://127.0.0.1/generate_204"}}],
			"track": ` + track + `
		}
	}`
}

func buildTrackConfig(t *testing.T, track string) (*burst.Config, error) {
	t.Helper()
	var config Config
	if err := json.Unmarshal([]byte(trackConfig(track)), &config); err != nil {
		t.Fatal(err)
	}
	built, err := config.Build()
	if err != nil {
		return nil, err
	}
	for _, app := range built.App {
		if message, err := app.GetInstance(); err == nil {
			if cfg, ok := message.(*burst.Config); ok {
				return cfg, nil
			}
		}
	}
	t.Fatal("no burst observatory in the built config")
	return nil, nil
}

func TestBurstObservatoryTrackBuild(t *testing.T) {
	cfg, err := buildTrackConfig(t, `[{"probe": "ahk.ecs-hy2", "outbounds": ["tk.auto-hy2", "sg.auto-hy2"]}]`)
	if err != nil {
		t.Fatal(err)
	}
	want := []*burst.TrackGroup{{Probe: "ahk.ecs-hy2", Outbounds: []string{"tk.auto-hy2", "sg.auto-hy2"}}}
	if len(cfg.Track) != 1 || !proto.Equal(cfg.Track[0], want[0]) {
		t.Fatalf("track = %v", cfg.Track)
	}
}

func TestBurstObservatoryTrackRefusals(t *testing.T) {
	for want, track := range map[string]string{
		"track[0]: null entry":                         `[null]`,
		"track[0].outbounds: empty":                    `[{"probe": "ahk.ecs-hy2", "outbounds": []}]`,
		`"tk.auto-hy2" is already tracked in group #0`: `[{"probe": "ahk.ecs-hy2", "outbounds": ["tk.auto-hy2"]}, {"probe": "ahk.alpine.1st-hy2", "outbounds": ["tk.auto-hy2"]}]`,
		`"sg.auto-hy2" is itself tracked`:              `[{"probe": "ahk.ecs-hy2", "outbounds": ["sg.auto-hy2"]}, {"probe": "sg.auto-hy2", "outbounds": ["tk.auto-hy2"]}]`,
		`no outbound "ahk.gone-hy2"`:                   `[{"probe": "ahk.gone-hy2", "outbounds": ["tk.auto-hy2"]}]`,
		`"tk.auto-hy2" is not probed`:                  `[{"probe": "tk.auto-hy2", "outbounds": ["sg.auto-hy2"]}]`,
		`no outbound "jp.auto-hy2"`:                    `[{"probe": "ahk.ecs-hy2", "outbounds": ["jp.auto-hy2"]}]`,
		`"ahk.alpine.1st-hy2" is probed itself`:        `[{"probe": "ahk.ecs-hy2", "outbounds": ["ahk.alpine.1st-hy2"]}]`,
	} {
		if _, err := buildTrackConfig(t, track); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v", want, err)
		}
	}
}
