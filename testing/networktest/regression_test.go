package networktest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"go/parser"
	"go/token"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestBadProxyNoDirectFallback(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := target.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			c.Close()
		}
	}()
	defer func() { target.Close(); <-done }()
	// Occupy the proxy port, accept, and reject the SOCKS handshake.
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyDone := make(chan struct{})
	go func() {
		defer close(proxyDone)
		for {
			c, err := proxy.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	defer func() { proxy.Close(); <-proxyDone }()
	t.Setenv("XRAY_TEST_NETWORK", "1")
	t.Setenv("XRAY_TEST_PROXY", "socks5://"+proxy.Addr().String())
	t.Setenv("XRAY_TEST_UDP_PROXY", "")
	p := Enable(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := p.DialContext(ctx, "tcp", target.Addr().String())
	if c != nil {
		c.Close()
	}
	if err == nil {
		t.Fatal("bad proxy unexpectedly succeeded")
	}
	target.Close()
	<-done
	if accepted.Load() != 0 {
		t.Fatalf("direct destination accepted %d connections", accepted.Load())
	}
}

func TestFormatGateRejectsWithoutModification(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return output
	}
	// Fetch only the tip and stable fork point into a separate object store.
	// Intermediate pre-reconstruction commits must not be needed by the gate.
	run("clone", "--no-checkout", "--depth=1", "file://"+root, dir)
	run("fetch", "--depth=1", "origin", "b4f08981becb71eaa995fa98ed2098ade92566bb")
	oldBaseline := exec.Command("git", "cat-file", "-e", "1e0c6a30f2a33080b4ce55121e2e414a09f99bec^{commit}")
	oldBaseline.Dir = dir
	if err := oldBaseline.Run(); err == nil {
		t.Fatal("fixture unexpectedly contains the obsolete format baseline")
	}
	run("read-tree", "HEAD")
	path := filepath.Join(dir, "common", "fmt_negative.go")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	source := []byte("package common\nfunc regressionFmt( ) {println( \"fixture\" )}\n")
	if _, err := parser.ParseFile(token.NewFileSet(), path, source, parser.AllErrors); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "common/fmt_negative.go")
	if got := run("ls-files", "--error-unmatch", "common/fmt_negative.go"); !bytes.Contains(got, []byte("fmt_negative.go")) {
		t.Fatal("fixture is not tracked")
	}
	before := sha256.Sum256(source)
	cmd := exec.Command("bash", filepath.Join(root, ".github/check-gofmt.sh"))
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("common/fmt_negative.go")) {
		t.Fatalf("gate = %v\n%s", err, output)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(after) != before {
		t.Fatal("format gate modified source")
	}
	t.Logf("tracked negative fixture rejected; SHA256 unchanged: %x", before)
}
