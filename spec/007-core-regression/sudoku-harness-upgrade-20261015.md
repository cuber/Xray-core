# Sudoku Harness Regression Evidence

Date: 2026-10-15. Scope: test-only changes in
`transport/internet/finalmask/sudoku/{sudoku,process,race_enabled,race_disabled}_test.go`.
No runtime changes, shared ledger edits, commits, pushes or deployment were made
for this task. Main-agent review and commit remain pending.

## Initial Failure and Diagnosis

The wholecore final-shutdown-race log
`/tmp/core-upgrade-final-shutdown-race.jsonl` records
`TestSudokuE2ETemp/vless-reality/prefer_ascii` failing after 12.08s with
`dial tcp 127.0.0.1:58400: connect: connection refused`. The first pair of child
processes produced no recorded output; subsequent cases passed. Child compilation
had already completed before the failing subtest. Compilation is not an explanation.

The historical root cause remains undetermined. The old harness did not observe
child exit immediately or retain exit diagnostics: it used fixed 500ms/1500ms
startup sleeps, then a 10s dial loop, and discarded `Process.Wait` results during
cleanup. These are confirmed diagnostic/lifecycle gaps, not proof of what prevented
the first child pair from listening. Separately reported scenario EADDRNOTAVAIL
failures do not establish that this connection-refused failure had the same cause.

## Test Harness Changes

- Start both child processes without fixed startup sleeps. Within one original
  10s budget measured before spawning, check both startup log markers, process
  liveness and the client TCP listener. The successful connection is reused for
  the existing payload oracle, not discarded as an extra probe.
- Observe each child using `Cmd.Wait` immediately after Start. Capture bounded
  combined output (64 KiB tail), PID, elapsed time, startup/race flags, exit status
  and last dial error for failure diagnostics. Join output copying before judging
  cleanup. Register cleanup immediately, including partial pair-start failure.
- Make cleanup idempotent and report early exit, abnormal exit, child race or
  forced kill to the parent test. Retain the existing 3s shutdown grace period;
  bound inherited output-pipe cleanup using `Cmd.WaitDelay` of 1s. Remove the
  test package's process-wide signal handler that called `os.Exit(130)`.
- Propagate parent race instrumentation into `go build -race` for child Core and
  verify the child binary's Go build metadata. Force child `GORACE` log output to
  captured stderr so inherited `log_path` cannot hide a report. Detect race markers
  across writes and retain the flag even after output-tail truncation.

The existing payload and traffic-shape assertions remain in place for VLESS
Reality, Hysteria2, VLESS encryption and VLESS XHTTP, each in ASCII and entropy
modes. No startup timeout widening, failure retry or runtime security relaxation
was introduced.

## Independent UDP Port Failure

The first full race count=3 run passed 23/24 E2E cases but failed the third
Hysteria2/ASCII case before spawning child Core:
`listen udp 127.0.0.1:54080: bind: address already in use`.
Evidence is retained in `/tmp/core-sudoku-full-race.log` (package failed, 97.863s).
This is EADDRINUSE, not EADDRNOTAVAIL, and is not attributed to the original
Reality failure.

The test had selected UDP server/relay ports using TCP `PickPort`, which does not
establish UDP availability. It now uses the existing UDP picker for the server;
the relay binds UDP port zero directly and advertises its actual bound port.
There is no retry on bind failure. The server picker still releases its socket
before child Core binds, so the general pick-then-bind race is not claimed solved.

## Negative Controls

`process_test.go` covers split/truncated output-marker retention, a silent child
exiting with status 7, and a silent live child exceeding a short supplied deadline
without extending that deadline. `TestSudokuChildRaceZeroExitRejected` runs an
actual data race in a subprocess with `GORACE=exitcode=0`, verifies a zero exit,
then checks that the production test-cleanup path reports the race exactly once
even when called twice. It does not merely print a synthetic race warning.
That negative control requires race instrumentation and explicitly skips in
ordinary mode; it ran and passed in every final race repetition.

## Commands and Results

Executed from `/Volumes/Linux/opensource/cuber/xray-core-rebase-20261014` on
Darwin/arm64 with Go 1.27.0. Test runs used the dirty candidate worktree based on
`be9f5afd9324c4d5c4de68ac2e97ba4a95895afa`, including concurrent agent changes;
they are not clean release-artifact acceptance. Each invocation builds its child
Core once and reuses it across repetitions. Later worktree edits are not covered
by an already-built child binary.

Final commands after the UDP test correction:

```sh
go test -race -count=3 -timeout=5m -v ./transport/internet/finalmask/sudoku > /tmp/core-sudoku-full-race-final.log 2>&1
go test -count=1 -timeout=2m -v ./transport/internet/finalmask/sudoku > /tmp/core-sudoku-full-normal-final.log 2>&1
go vet ./transport/internet/finalmask/sudoku
git diff --check -- transport/internet/finalmask/sudoku
```

- Race: PASS, 101.066s; 24/24 E2E cases, zero skips, all harness controls passed
  in all three repetitions. Child metadata explicitly reported `race=true`.
- Ordinary: PASS, 16.908s; 8/8 E2E cases, one expected skip for the race-only
  negative control. Child metadata explicitly reported `race=false`.
- Vet and scoped diff check: exit zero.

An earlier focused harness run also passed (2.015s), before the final cleanup
reporter/UDP refinements; final full-package results above supersede it:

```sh
go test -race -count=3 -timeout=2m -v ./transport/internet/finalmask/sudoku -run '^TestSudoku(Child|Ready)' > /tmp/core-sudoku-harness-race.log 2>&1
```

Final test source SHA-256 values at verification:

```text
f1c050df039d4798309caa7eef527966f8e08fdde4d1de902572eb2eb665960e  sudoku_test.go
8ce934cac457207080a2781f3b3e2f6f5975af836fc503a8ba32112947c57a7e  process_test.go
f25a9fab365ffe7025136430e2bf2c7237ab6f3174604993fb695f76b802fa38  race_enabled_test.go
abea2eeacb713b666fe45204960997f08940196d5333e4c5547e6144c72b460e  race_disabled_test.go
```

## Acceptance Boundary

This establishes scoped Darwin harness and protocol-matrix regression results,
not the cause of the historical startup failure, Linux parity, wholecore race
acceptance, AnyTLS acceptance, or final official binary acceptance. Local `/tmp`
logs are diagnostic artifacts, not committed release inputs. All task-owned test
processes have finished. Sudoku changes are frozen for main-agent review; further
testing is paused pending coordinated low-conflict final acceptance scheduling.
