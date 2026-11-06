package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Core spec018 FR-004 (S3.1): `xray run -test` refuses a dialerProxy cycle and a loop through a
// loopback with exit 23 and the S4 text, and accepts the shapes S2 lets through.
func TestRunTestRefusesLoops(t *testing.T) {
	if config := os.Getenv("XRAY_SPEC018_RUN_TEST"); config != "" {
		os.Args = []string{"xray", "run", "-test", "-config", config}
		main()
		return
	}
	cases := []struct {
		name, config string
		exit         int
		want         string
	}{
		{
			"a dialerProxy cycle", `{"outbounds": [
			{"tag": "a", "protocol": "freedom", "streamSettings": {"sockopt": {"dialerProxy": "b"}}},
			{"tag": "b", "protocol": "freedom", "streamSettings": {"sockopt": {"dialerProxy": "a"}}}]}`,
			23, "dialerProxy cycle: a -> b -> a",
		},
		{
			"a loop through a loopback", `{
			"inbounds": [{"tag": "in", "listen": "127.0.0.1", "port": 0, "protocol": "socks"}],
			"outbounds": [{"tag": "lo", "protocol": "loopback", "settings": {"inboundTag": "in"}}],
			"routing": {"rules": [{"type": "field", "inboundTag": ["in"], "outboundTag": "lo"}]}}`,
			23, "loopback loop: in -> [rule #1] lo -> in",
		},
		{
			"a loopback default without routing", `{
			"outbounds": [{"tag": "lo", "protocol": "loopback", "settings": {"inboundTag": "in"}}]}`,
			23, "loopback loop: in -> [default] lo -> in",
		},
		{
			"a loopback whose inbound leads elsewhere first", `{
			"outbounds": [{"tag": "direct", "protocol": "freedom"},
				{"tag": "lo", "protocol": "loopback", "settings": {"inboundTag": "in"}}],
			"routing": {"rules": [{"type": "field", "inboundTag": ["in"], "outboundTag": "direct"},
				{"type": "field", "inboundTag": ["in"], "outboundTag": "lo"}]}}`,
			0, "Configuration OK",
		},
		{
			"a dialerProxy naming no outbound", `{"outbounds": [
			{"tag": "a", "protocol": "freedom", "streamSettings": {"sockopt": {"dialerProxy": "gone"}}}]}`,
			0, "Configuration OK",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(config, []byte(c.config), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestRunTestRefusesLoops$")
			cmd.Env = append(os.Environ(), "XRAY_SPEC018_RUN_TEST="+config)
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
