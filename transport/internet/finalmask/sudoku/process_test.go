package sudoku

import (
	"bytes"
	"debug/buildinfo"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type sudokuOutput struct {
	mu          sync.Mutex
	tail        []byte
	ready, race bool
	bytes       int
}

func (o *sudokuOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.bytes += len(p)
	o.tail = append(o.tail, p...)
	o.race = o.race || bytes.Contains(o.tail, []byte("WARNING: DATA RACE"))
	o.ready = o.ready || (bytes.Contains(o.tail, []byte("core: Xray ")) && bytes.Contains(o.tail, []byte(" started")))
	if len(o.tail) > 64*1024 {
		o.tail = append([]byte(nil), o.tail[len(o.tail)-64*1024:]...)
	}
	return len(p), nil
}

func (o *sudokuOutput) snapshot() (bool, bool, string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ready, o.race, fmt.Sprintf("output_bytes=%d tail=%q", o.bytes, o.tail)
}

type sudokuChild struct {
	cmd           *exec.Cmd
	label         string
	output        sudokuOutput
	done          chan struct{}
	err           error // Published by closing done, after Cmd.Wait joins output copying.
	started       time.Time
	peer          *sudokuChild
	readyDeadline time.Time
	stopOnce      sync.Once
	report        func(error)
}

func startSudokuChild(cmd *exec.Cmd, label string) (*sudokuChild, error) {
	c := &sudokuChild{cmd: cmd, label: label, done: make(chan struct{}), started: time.Now()}
	env := cmd.Environ()
	var raceOptions string
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, "GORACE=") {
			raceOptions = strings.TrimPrefix(entry, "GORACE=")
		} else {
			filtered = append(filtered, entry)
		}
	}
	// Never let an inherited log_path hide a zero-exit child race from the parent.
	cmd.Env = append(filtered, "GORACE="+raceOptions+" log_path=stderr")
	cmd.Stdout, cmd.Stderr = &c.output, &c.output
	// Bound output-pipe cleanup if a failed child leaves an inherited descriptor.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", label, err)
	}
	go func() { c.err = cmd.Wait(); close(c.done) }()
	return c, nil
}

func (c *sudokuChild) diagnostic() string {
	ready, race, output := c.output.snapshot()
	state := "running"
	select {
	case <-c.done:
		state = fmt.Sprintf("exited: %v (%v)", c.err, c.cmd.ProcessState)
	default:
	}
	return fmt.Sprintf("%s pid=%d elapsed=%s ready=%v race=%v state=%s %s", c.label, c.cmd.Process.Pid, time.Since(c.started), ready, race, state, output)
}

func (c *sudokuChild) failure() error {
	_, race, _ := c.output.snapshot()
	if race {
		return fmt.Errorf("child race: %s", c.diagnostic())
	}
	select {
	case <-c.done:
		return fmt.Errorf("child exited before teardown: %s", c.diagnostic())
	default:
		return nil
	}
}

func (c *sudokuChild) stop() error {
	early := c.failure()
	select {
	case <-c.done:
	default:
		_ = c.cmd.Process.Signal(syscall.SIGTERM)
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	forced := false
	select {
	case <-c.done:
	case <-timer.C:
		forced = true
		_ = c.cmd.Process.Kill()
		<-c.done
	}
	_, race, _ := c.output.snapshot()
	if early != nil || forced || race {
		return fmt.Errorf("child cleanup early=%v forced=%v: %s", early, forced, c.diagnostic())
	}
	if c.err != nil {
		status, ok := c.cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
			return fmt.Errorf("child abnormal exit: %s", c.diagnostic())
		}
	}
	return nil
}

func waitSudokuReady(deadline time.Time, port int, children ...*sudokuChild) (net.Conn, error) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for {
		ready := true
		for _, child := range children {
			if err := child.failure(); err != nil {
				return nil, err
			}
			r, _, _ := child.output.snapshot()
			ready = ready && r
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			var diagnostics []string
			for _, child := range children {
				diagnostics = append(diagnostics, child.diagnostic())
			}
			return nil, fmt.Errorf("startup deadline exceeded port=%d last_dial=%v: %s", port, last, strings.Join(diagnostics, "\n"))
		}
		if ready {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), min(remaining, 500*time.Millisecond))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		select {
		case <-ticker.C:
		case <-time.After(time.Until(deadline)):
		}
	}
}

func verifySudokuBinary(t *testing.T, path string) {
	t.Helper()
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	race := false
	for _, setting := range info.Settings {
		if setting.Key == "-race" && setting.Value == "true" {
			race = true
		}
	}
	if race != sudokuRaceEnabled {
		t.Fatalf("child race metadata=%v parent=%v", race, sudokuRaceEnabled)
	}
	t.Logf("child build verified Go=%s race=%v path=%s", info.GoVersion, race, path)
}

func TestSudokuChildOutputMarkers(t *testing.T) {
	var output sudokuOutput
	for _, part := range []string{"core: Xray 26", ".10 started\nWAR", "NING: DATA ", "RACE\n"} {
		_, _ = output.Write([]byte(part))
	}
	_, _ = output.Write(bytes.Repeat([]byte("x"), 128*1024))
	ready, race, _ := output.snapshot()
	if !ready || !race || len(output.tail) != 64*1024 {
		t.Fatal("lost split marker or exceeded bounded output")
	}
}

func sudokuHelper(t *testing.T, mode string) *sudokuChild {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestSudokuChildHelper$")
	cmd.Env = append(os.Environ(), "XRAY_SUDOKU_CHILD_HELPER="+mode, "GORACE=exitcode=0 atexit_sleep_ms=0")
	child, err := startSudokuChild(cmd, "negative-control")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-child.done:
		default:
			_ = child.cmd.Process.Kill()
			<-child.done
		}
	})
	return child
}

func TestSudokuChildHelper(t *testing.T) {
	switch os.Getenv("XRAY_SUDOKU_CHILD_HELPER") {
	case "exit":
		os.Exit(7)
	case "race-zero-exit":
		var value int
		start := make(chan struct{})
		var wg sync.WaitGroup
		for n := 0; n < 2; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := 0; i < 1000; i++ {
					value++
				}
			}()
		}
		close(start)
		wg.Wait()
		fmt.Fprintln(os.Stdout, value)
		os.Exit(0)
	case "quiet":
		<-time.After(time.Minute)
		os.Exit(0)
	}
}

func TestSudokuReadyRejectsEarlyExit(t *testing.T) {
	child := sudokuHelper(t, "exit")
	<-child.done
	_, err := waitSudokuReady(time.Now().Add(time.Second), 0, child)
	if err == nil || !strings.Contains(err.Error(), "exit status 7") || !strings.Contains(err.Error(), "output_bytes=0") {
		t.Fatalf("missing early-exit diagnostic: %v", err)
	}
}

func TestSudokuReadyUsesOriginalDeadline(t *testing.T) {
	child := sudokuHelper(t, "quiet")
	start := time.Now()
	_, err := waitSudokuReady(start.Add(100*time.Millisecond), 0, child)
	if err == nil || !strings.Contains(err.Error(), "startup deadline exceeded") || !strings.Contains(err.Error(), "ready=false") {
		t.Fatalf("missing startup diagnostic: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("ready gate extended its deadline")
	}
}

func TestSudokuChildRaceZeroExitRejected(t *testing.T) {
	if !sudokuRaceEnabled {
		t.Skip("negative control requires a race-instrumented child")
	}
	child := sudokuHelper(t, "race-zero-exit")
	<-child.done
	if child.err != nil {
		t.Fatalf("negative control must exit zero: %v", child.err)
	}
	var reported []error
	child.report = func(err error) { reported = append(reported, err) }
	stopCmd(child)
	stopCmd(child)
	if len(reported) != 1 || !strings.Contains(reported[0].Error(), "child race") {
		t.Fatalf("zero-exit child race was not reported exactly once: %v", reported)
	}
}
