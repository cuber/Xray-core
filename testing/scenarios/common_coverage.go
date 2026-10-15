//go:build coverage
// +build coverage

package scenarios

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/uuid"
)

func BuildXray() error {
	genTestBinaryPath()
	if _, err := os.Stat(testBinaryPath); err == nil {
		return nil
	}

	args := []string{"test", "-tags", "coverage coveragemain", "-coverpkg", "github.com/xtls/xray-core/...", "-c", "-o", testBinaryPath}
	if scenarioRaceEnabled {
		args = append(args, "-race")
	}
	args = append(args, GetSourcePath())
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func RunXrayProtobuf(config []byte) *exec.Cmd {
	genTestBinaryPath()

	covDir := os.Getenv("XRAY_COV")
	if covDir == "" {
		covDir = filepath.Join(filepath.Dir(testBinaryPath), "coverage")
	}
	common.Must(os.MkdirAll(covDir, 0o700))
	randomID := uuid.New()
	profile := randomID.String() + ".out"
	proc := exec.Command(testBinaryPath, "-test.run=^TestRunMainForCoverage$", "-test.coverprofile", profile, "-test.outputdir", covDir, "--", "run", "-config=stdin:", "-format=pb")
	proc.Stdin = bytes.NewBuffer(config)
	proc.Stderr = os.Stderr
	proc.Stdout = os.Stdout
	captureScenarioRace(proc)

	return proc
}
