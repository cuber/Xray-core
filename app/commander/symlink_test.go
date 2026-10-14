package commander

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectAPIUnixRejectsSymlinkWithoutChangingTarget(t *testing.T) {
	for _, kind := range []string{"file", "live-socket", "stale-socket", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			path := testUnixPath(t)
			target := filepath.Join(filepath.Dir(path), "target")
			switch kind {
			case "file":
				if err := os.WriteFile(target, []byte("unrelated target"), 0600); err != nil {
					t.Fatal(err)
				}
			case "live-socket":
				owner := testAPI(t, target)
				if err := owner.Start(); err != nil {
					t.Fatal(err)
				}
			case "stale-socket":
				listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: target, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				listener.SetUnlinkOnClose(false)
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before, beforeErr := os.Lstat(target)
			if kind != "dangling" && beforeErr != nil {
				t.Fatal(beforeErr)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			candidate := testAPI(t, path)
			if err := candidate.Start(); err == nil {
				t.Fatal("API accepted symlink")
			}
			if err := candidate.Close(); err != nil {
				t.Fatal(err)
			}
			link, err := os.Readlink(path)
			if err != nil || link != target {
				t.Fatalf("symlink changed: %q %v", link, err)
			}
			after, err := os.Lstat(target)
			if kind == "dangling" {
				if !os.IsNotExist(err) {
					t.Fatal("dangling target created", err)
				}
			} else if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("unrelated target replaced or changed", err)
			}
			if kind == "file" {
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "unrelated target" {
					t.Fatal("unrelated file contents changed", err)
				}
			}
			if kind == "live-socket" {
				testHealth(t, "unix://"+target)
			}
			if _, err := os.Lstat(path + ".lock"); !os.IsNotExist(err) {
				t.Fatal("failed start retained its lock", err)
			}
		})
	}
}
