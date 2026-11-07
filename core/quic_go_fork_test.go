package core_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The QUIC library is our fork (spec022). An upstream merge that rewrites go.mod
// must not silently drop the replace and build against upstream's module.
func TestQuicGoIsReplacedByTheFork(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	var target string
	for _, line := range strings.Split(string(data), "\n") {
		// Both "replace m => r v" and "replace m v => r v", alone or inside a replace block.
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "replace "))
		if len(fields) < 3 || fields[0] != "github.com/apernet/quic-go" {
			continue
		}
		for i, field := range fields[:len(fields)-1] {
			if field == "=>" {
				target = fields[i+1]
			}
		}
	}
	if target != "github.com/cuber/quic-go" {
		t.Fatalf("go.mod must replace github.com/apernet/quic-go with github.com/cuber/quic-go, got %q", target)
	}
}
