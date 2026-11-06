package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Core spec019 FR-003, FR-004: `xray run -test` refuses a member balancer without a fallbackTag, a
// member cycle and an unobserved outbound under a member, with exit 23; it accepts the shape of
// xray-config spec021 with a blackhole fallback, which needs no observation.
func TestRunTestChecksBalancerMembers(t *testing.T) {
	if config := os.Getenv("XRAY_SPEC019_RUN_TEST"); config != "" {
		os.Args = []string{"xray", "run", "-test", "-config", config}
		main()
		return
	}
	shape := func(observed, routeFallback, memberFallback, extra string) string {
		return `{
			"outbounds": [{"tag": "direct", "protocol": "freedom"}, {"tag": "block", "protocol": "blackhole"},
				{"tag": "a-hy2", "protocol": "freedom"}, {"tag": "a-anytls", "protocol": "freedom"},
				{"tag": "x", "protocol": "freedom"}],
			"routing": {"balancers": [
				{"tag": "route", "selector": ["x"], "balancers": ["a"], "strategy": {"type": "leastping"},
					"fallbackTag": "` + routeFallback + `"},
				{"tag": "a", "selector": ["a-hy2", "a-anytls"], "strategy": {"type": "leastping"}` + memberFallback + `}` + extra + `
			]},
			"burstObservatory": {"subjectSelector": ` + observed + `,
				"pingConfig": {"destination": "http://127.0.0.1:9/", "interval": "1h"}}
		}`
	}
	all := `["a-hy2", "a-anytls", "x"]`
	cases := []struct {
		name, config string
		exit         int
		want         string
	}{
		{"accepted", shape(all, "block", `, "fallbackTag": "a-hy2"`, ""), 0, "Configuration OK"},
		{"a member without a fallback", shape(all, "block", "", ""), 23, "balancer a is a member and has no fallbackTag"},
		{
			"an unobserved candidate", shape(`["a-hy2", "x"]`, "block", `, "fallbackTag": "a-hy2"`, ""), 23,
			"balancer a: outbound a-anytls is not observed (balancer members need every outbound observed)",
		},
		{
			"an unobserved fallback", shape(all, "direct", `, "fallbackTag": "a-hy2"`, ""), 23,
			"balancer route: outbound direct is not observed",
		},
		{
			"a member cycle", shape(all, "block", `, "fallbackTag": "route"`, ""), 23,
			"balancer cycle: a -[fallback]-> route -[member]-> a",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(config, []byte(c.config), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestRunTestChecksBalancerMembers$")
			cmd.Env = append(os.Environ(), "XRAY_SPEC019_RUN_TEST="+config)
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
