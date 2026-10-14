package scenarios

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"sync"
)

var scenarioRaceReports sync.Map

// Preserve stderr while remembering race diagnostics even if graceful shutdown
// masks the child's race-detector exit status. Keep only a marker-sized suffix.
type scenarioRaceReport struct {
	sync.Mutex
	output   io.Writer
	tail     []byte
	detected bool
}

func (r *scenarioRaceReport) Write(p []byte) (int, error) {
	r.Lock()
	defer r.Unlock()
	const marker = "WARNING: DATA RACE"
	joined := append(r.tail, p...)
	r.detected = r.detected || bytes.Contains(joined, []byte(marker))
	if len(joined) >= len(marker) {
		joined = joined[len(joined)-len(marker)+1:]
	}
	r.tail = append([]byte(nil), joined...)
	return r.output.Write(p)
}

func captureScenarioRace(proc *exec.Cmd) {
	if !scenarioRaceEnabled {
		return
	}
	r := &scenarioRaceReport{output: os.Stderr}
	proc.Stderr = r
	scenarioRaceReports.Store(proc, r)
}

func scenarioHadRace(proc *exec.Cmd) bool {
	value, ok := scenarioRaceReports.LoadAndDelete(proc)
	if !ok {
		return false
	}
	r := value.(*scenarioRaceReport)
	r.Lock()
	defer r.Unlock()
	return r.detected
}
