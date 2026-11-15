package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Core spec027 TL-1, TL-7, TL-9 through `xray run -test`: the thresholdLeastPing shapes that load,
// and exit 23 with a focused message for a missing maxRTT, leastPing with maxRTT, a member without a
// fallback, and every unobserved outbound a thresholdLeastPing balancer can choose. leastPing keeps
// its unobserved fallback (O07).
func TestRunTestChecksThresholdLeastPing(t *testing.T) {
	if config := os.Getenv("XRAY_SPEC027_RUN_TEST"); config != "" {
		os.Args = []string{"xray", "run", "-test", "-config", config}
		main()
		return
	}
	// shape: outbounds direct and block are freedom, sink is a blackhole; observed is the ping group's
	// subjectSelector (empty: no observatory); track an optional track group.
	shape := func(balancers, observed, track string) string {
		config := `{
			"outbounds": [{"tag": "direct", "protocol": "freedom"}, {"tag": "sink", "protocol": "blackhole"},
				{"tag": "block", "protocol": "freedom"}, {"tag": "a-hy2", "protocol": "freedom"},
				{"tag": "a-vless", "protocol": "freedom"}, {"tag": "f-backup", "protocol": "freedom"},
				{"tag": "f-track", "protocol": "freedom"}],
			"routing": {"balancers": [` + balancers + `]}`
		if observed != "" {
			config += `,
			"burstObservatory": {` + track + `"pingGroups": [{"subjectSelector": ` + observed + `,
				"pingConfig": {"destination": "http://127.0.0.1:9/", "interval": "1h"}}]}`
		}
		return config + "}"
	}
	threshold := func(tag, selector, fallback string) string {
		b := `{"tag": "` + tag + `", "selector": ["` + selector + `"], "strategy": {"type": "thresholdLeastPing",
			"settings": {"maxRTT": "50ms", "recoverRTT": "40ms"}}`
		if fallback != "" {
			b += `, "fallbackTag": "` + fallback + `"`
		}
		return b + "}"
	}
	all := `["a-", "f-"]`
	notObserved := " is not observed (thresholdLeastPing needs every outbound it can choose observed)"
	cases := []struct {
		name, config string
		exit         int
		want         string
	}{
		{"an observed outbound fallback", shape(threshold("t", "a-", "f-backup"), all, ""), 0, "Configuration OK"},
		{"no fallback", shape(threshold("t", "a-", ""), `["a-"]`, ""), 0, "Configuration OK"},
		{"an unobserved blackhole fallback", shape(threshold("t", "a-", "sink"), `["a-"]`, ""), 0, "Configuration OK"},
		{"a balancer fallback", shape(threshold("t", "a-", "u")+", "+threshold("u", "f-", "sink"), all, ""), 0, "Configuration OK"},
		{
			"a fallback observed through track",
			shape(threshold("t", "a-", "f-track"), `["a-", "f-backup"]`, `"track": [{"probe": "f-backup", "outbounds": ["f-track"]}], `),
			0, "Configuration OK",
		},
		{
			"leastPing keeps an unobserved fallback",
			shape(`{"tag": "l", "selector": ["a-"], "strategy": {"type": "leastPing"}, "fallbackTag": "direct"}`, `["a-"]`, ""),
			0, "Configuration OK",
		},
		{
			"no maxRTT",
			shape(`{"tag": "t", "selector": ["a-"], "strategy": {"type": "thresholdLeastPing"}, "fallbackTag": "f-backup"}`, all, ""),
			23, "thresholdLeastPing needs maxRTT",
		},
		{
			"leastPing with maxRTT",
			shape(`{"tag": "l", "selector": ["a-"], "strategy": {"type": "leastPing", "settings": {"maxRTT": "50ms"}}, "fallbackTag": "f-backup"}`, all, ""),
			23, "leastPing has no maxRTT; use the thresholdLeastPing strategy",
		},
		{
			"a thresholdLeastPing member without a fallback",
			shape(`{"tag": "p", "selector": ["f-"], "balancers": ["t"], "strategy": {"type": "leastPing"}, "fallbackTag": "sink"}, `+
				threshold("t", "a-", ""), all, ""),
			23, "balancer t is a member and has no fallbackTag",
		},
		{"an unobserved outbound fallback", shape(threshold("t", "a-", "direct"), all, ""), 23, "balancer t: outbound direct" + notObserved},
		{"an unobserved candidate", shape(threshold("t", "a-", "f-backup"), `["a-hy2", "f-"]`, ""), 23, "balancer t: outbound a-vless" + notObserved},
		{"a non-blackhole named block", shape(threshold("t", "a-", "block"), all, ""), 23, "balancer t: outbound block" + notObserved},
		{
			"an unobserved fallback one balancer down",
			shape(threshold("t", "a-", "u")+", "+threshold("u", "f-", "direct"), all, ""),
			23, "balancer u: outbound direct" + notObserved,
		},
		{
			"a fallbackTag naming nothing", shape(threshold("t", "a-", "nothing"), all, ""),
			23, "balancer t: fallbackTag nothing names no outbound and no balancer",
		},
		{"no observatory", shape(threshold("t", "a-", "sink"), "", ""), 23, "balancer t: outbound a-hy2" + notObserved},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(config, []byte(c.config), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestRunTestChecksThresholdLeastPing$")
			cmd.Env = append(os.Environ(), "XRAY_SPEC027_RUN_TEST="+config)
			output, err := cmd.CombinedOutput()
			exit := 0
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exit = exitErr.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if exit != c.exit || !strings.Contains(string(output), c.want) {
				t.Fatalf("run -test exit %d, want %d and %q:\n%s", exit, c.exit, c.want, output)
			}
		})
	}
}
