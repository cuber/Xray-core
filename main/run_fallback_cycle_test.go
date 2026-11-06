package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Core spec017 FR-004 (L3): `xray run -test` on a configuration with a balancer fallback cycle
// prints the cycle and exits 23, the configuration-error status that systemd does not restart.
func TestRunTestRejectsAFallbackCycle(t *testing.T) {
	if config := os.Getenv("XRAY_SPEC017_RUN_TEST"); config != "" {
		os.Args = []string{"xray", "run", "-test", "-config", config}
		main()
		return
	}
	config := filepath.Join(t.TempDir(), "cycle.json")
	if err := os.WriteFile(config, []byte(`{
		"outbounds": [{"tag": "direct", "protocol": "freedom"}],
		"routing": {"balancers": [
			{"tag": "a", "selector": ["direct"], "fallbackTag": "b"},
			{"tag": "b", "selector": ["direct"], "fallbackTag": "a"}
		]},
		"observatory": {"subjectSelector": ["direct"]}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunTestRejectsAFallbackCycle$")
	cmd.Env = append(os.Environ(), "XRAY_SPEC017_RUN_TEST="+config)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("run -test = %v, want exit 23\n%s", err, output)
	}
	if !strings.Contains(string(output), "Failed to start") || !strings.Contains(string(output), "balancer fallback cycle: a -> b -> a") {
		t.Fatalf("run -test output:\n%s", output)
	}
}
