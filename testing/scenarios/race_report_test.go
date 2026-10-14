package scenarios

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestScenarioRaceReport(t *testing.T) {
	for _, chunks := range [][]string{{"normal output\n"}, {"WARNING: DATA RACE\n"}, {"WAR", "NING: DATA ", "RACE\n"}} {
		var out bytes.Buffer
		r := &scenarioRaceReport{output: &out}
		for _, chunk := range chunks {
			if n, err := r.Write([]byte(chunk)); err != nil || n != len(chunk) {
				t.Fatalf("write: %d %v", n, err)
			}
		}
		joined := strings.Join(chunks, "")
		if out.String() != joined || r.detected != strings.Contains(joined, "WARNING: DATA RACE") {
			t.Fatalf("lost output or detection: %q %v", out.String(), r.detected)
		}
		if len(r.tail) >= len("WARNING: DATA RACE") {
			t.Fatal("unbounded diagnostic suffix")
		}
		proc := new(exec.Cmd)
		scenarioRaceReports.Store(proc, r)
		if got := scenarioHadRace(proc); got != r.detected {
			t.Fatalf("reported race %v, want %v", got, r.detected)
		}
		if scenarioHadRace(proc) {
			t.Fatal("completed diagnostic retained")
		}
	}
}

func TestScenarioRaceChildFixture(t *testing.T) {
	if os.Getenv("XRAY_SCENARIO_RACE_FIXTURE") != "1" {
		return
	}
	fmt.Fprintln(os.Stderr, "WARNING: DATA RACE")
	os.Exit(0)
}

func TestScenarioRaceFailsDespiteCleanChildExit(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprint(all), func(t *testing.T) {
			proc := exec.Command(os.Args[0], "-test.run=^TestScenarioRaceChildFixture$")
			proc.Env = append(os.Environ(), "XRAY_SCENARIO_RACE_FIXTURE=1")
			r := &scenarioRaceReport{output: io.Discard}
			proc.Stderr = r
			scenarioRaceReports.Store(proc, r)
			defer scenarioRaceReports.Delete(proc)
			if err := proc.Run(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if recover() == nil {
					t.Error("clean process exit masked child race")
				}
			}()
			if all {
				CloseAllServers([]*exec.Cmd{proc})
			} else {
				CloseServer(proc)
			}
		})
	}
}
