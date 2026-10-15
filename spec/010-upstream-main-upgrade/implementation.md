# Implementation Ledger

## Boundaries

User authorization: 2026-10-14, publish existing draft audit then create a rebase
branch and start implementation. The published audit is develop `1fd51943`.
Candidate worktree: `../xray-core-rebase-20261014`; branch:
`rebase/main-20261014`; exact upstream base:
`0888beb38a9ba45440557175e71ce21cf8ba3b13`.

This is a contract-led rebase: start at upstream, import source specifications,
then port cohesive changes. Do not run a blind rebase that resurrects removed
singbridge or legacy proxySettings implementation. Each adaptation records its
original commit and why an upstream equivalent replaces part of it.

Source specs and evidence are imported from develop, then draft002 is moved to
spec010. References inside immutable dated evidence remain historical. The
canonical develop checkout and approved build/pin/runtime remain untouched.

## Phases

- [x] P01 Publish draft audit; lock source/upstream, create isolated branch,
  promote spec010; Go 1.27.0 is available on Darwin/arm64.
- [x] P02 Establish targeted upstream baseline; port spec002 dispatch lifecycle
  and independent tests without restoring removed SS bridge/legacy proxy path.
- [x] P03 Port shared spec007 readv/regression changes and later spec002
  retirement/validation capabilities. Windows runtime validation is a separately
  deferred TODO, not a passed test; see the platform boundary below.
- [x] P04 Port spec003 multi-listener, SOCKS/Unix redirect contracts and tests.
- [x] P05 Port spec004 OB and prerequisites, preserving 3s and full cancellation.
- [x] P06 Port spec005 routing metadata/weighted selection and spec006 domain
  traffic against atomic upstream APIs; regenerate reviewed protobuf.
- [x] P07 Port spec009 API UDS ownership beyond upstream's basic UDS support.
- [x] P08 Port spec008 AnyTLS, required Sing dependencies and new chain matrix;
  serial normal/race suites and strict ten-minute soaks pass, with platform and
  diagnostic skips recorded in the owning AnyTLS upgrade evidence.
- [x] P09 Resolve native SS gates U-001/002/003 and shared U-004..012;
  no source-level concern is declared fixed without a regression reproducer.
- [x] P10 Integrate isolated Sidecar/config migrations; normal/race source tests
  and migration staging pass. Final exact artifact/pin checks belong to P11;
  no production pin change or staged-config application is implied.
- [x] P11 Partitioned uncached/race/interop/chain tests, final changed-leaf reruns,
  format/vet and official Darwin/Linux Core builds
  through an explicitly candidate-aware wrapper; do not weaken normal develop
  release protections. Record every failure and skip.
- [ ] P12 Review/adopt candidate and release pins only on explicit approval;
  production rollout is a separate operation.

## Verification

Current partitioned results and explicit platform/network limits are summarized
in [local verification](local-verification-20261015.md). Dated failures below
remain historical evidence, not the latest result.

Each phase runs its owning spec tests; the additional U-* matrix in tests.md
applies throughout. Partial phase success is not full upgrade acceptance.
Never copy a prior fork's green report into this ledger as candidate evidence.

Completed port phases are backed by owning `upgrade-20261014.md` reports and
the resumed candidate package race log, not historical fork results. P05 has
106 candidate burst test/subtest passes; P06 has dispatcher/stats/stats-command
26/30/7 passes plus the router evidence directory. Final cross-component and
platform gates remain P09-P11. P03's Windows readiness is cross-compiled only;
no Windows execution is claimed. On 2026-10-15 the user explicitly deferred
Windows runtime acceptance because there is no Windows deployment scenario.
The follow-up remains in [spec007 tasks](../007-core-regression/tasks.md#deferred-platform-todo);
it does not block the current Linux/macOS candidate acceptance. Introducing
Windows later requires actual readv readiness/EOF/reset/cancellation and socket
retirement tests on Windows, not just cross-compilation.

Go source changes require the official dual-platform build workflow before
release; local verification does not make this branch an approved release.
No candidate artifact may overwrite xray-config/build approved release artifacts
or restart the local Xray service during integration.

### 2026-10-14: initial lifecycle checkpoint

Ported from develop `1fd51943`: dispatch pipe ownership, reader interrupt
forwarding, redirect owner cancellation, sender validation and optional outbound
retirement. Preserved upstream core status locking and FinalRules test fixtures;
did not restore singbridge or removed proxySettings behavior. Hy2 lifecycle
adaptation remains open, so P02/P03 are not marked complete.

Pristine upstream race baseline failed in outbound TestTagsCache: the cache
pointer was read outside the registry lock; its test also raced on stop flags.
The restored fork manager reads the cache under RLock and test flags are atomic.
Broader candidate tests exposed the existing UDP fixture accepting-flag race;
ported the spec007 close-driven fixture rather than suppressing the race.

Passed with Go 1.27.0 on Darwin/arm64:

```sh
go test -race -count=1 -timeout=5m ./common/buf ./core ./app/proxyman/outbound ./transport ./transport/internet ./transport/internet/hysteria
go vet ./common/buf ./core ./app/proxyman/outbound ./transport ./transport/internet ./transport/internet/hysteria
bash .github/check-gofmt.sh
git diff --check
```

Core tests took 48.546s, outbound 11.225s; other packages completed in under 3s.
These are package regressions, not the full acceptance or interop matrix.
Config wrapper now accepts a Git worktree root without weakening its dirty-tree
or develop tracking guards; four build wrapper tests pass. Candidate build
output is isolated in `xray-config/build/rebase-20261014`.

Dual-platform wrapper build passed at candidate `7dfa0637`; Darwin `version`
executed successfully (26.10.10, Go 1.27.0). Artifacts are not deployed:

| Artifact | SHA-256 |
| --- | --- |
| darwin/arm64 | `45d145538de651fbaff073f9f22223cf1254d696caba3f7f7caf365f67027354` |
| linux/amd64 | `779768d06d7e037df93d6b3139f7250cd3a150293cac2b074674bd96e4a80829` |

### 2026-10-14: integration in progress

Core, Sidecar and configuration now have isolated worktrees on the same branch,
`rebase/main-20261014`. The canonical Core develop, Sidecar master and config
master remain unchanged by the candidate runtime/config migrations. Config master
`5696ce0` only adds worktree-aware build-wrapper support.

The current dirty integration tree includes spec002-009 adaptations. Their presence
is not acceptance: AnyTLS interoperability, native SS resource gates, configuration
migration and complete regression/stability runs are still being verified. Do not
use this tree as a production replacement or copy candidate pins into master.

Additional completed checks (dirty candidate, Go 1.27.0):

- `go test ./... -run '^$' -timeout=5m`: all packages compile; **no functional
  tests are run by this command**.
- Darwin `go test -race ./testing/scenarios -run
  '^(TestCommander|TestDomainSniffing)' -count=1 -timeout=5m`: passed, 22.698s.
  Domain sniffing uses a local trusted TLS target and an exact domain route.
- Darwin `go test -race -count=5 ./proxy/blackhole ./common/task ./proxy/masque`:
  passed (11.484s / 62.930s / 1.698s).
- Linux/arm64 Docker, Go 1.27.0, `go test -race ./transport
  ./transport/internet ./app/commander ./common/buf -count=1 -timeout=5m`:
  passed (1.468s / 1.043s / 1.043s / 1.223s). These execute in Linux, not merely
  cross-compile; they do not substitute for Linux/amd64 production validation.
- Targeted DNS/TLS, OB, router, listeners and domain-statistics results are being
  recorded in their owning spec evidence. Full-suite acceptance remains open.

**Blocking REALITY/Vision regression:** the TLS-only GC matrix passes, but the
REALITY matrix reports races and fatal heap corruption. The selected REALITY
dependency changed `rawInput` from an inline `bytes.Buffer` to a pooled
`*bytes.Buffer`; VLESS's existing unsafe field extraction assumes the former
layout. Investigation/fix and a full rerun are required. An interim broad race
run explicitly excludes the three REALITY/Vision scenario entries while this is
being repaired; it must not be described as a whole-tree green run.

The acceptance ledger will be finalized against committed candidate revisions,
including artifact hashes, skips, resource measurements and unresolved gates.

The first broad race pass attempt finished nonzero: the Unix SOCKS UDP contract
failed, Hy2 UDP-manager shutdown raced in AnyTLS chain/process tests, and the
AnyTLS default-private-protection test failed. The first two now have scoped
fixes/regressions; their broad rerun and the private-protection diagnosis remain
required. REALITY's memory-layout fix passed scoped strict-pointer/GC tests,
then subprocess tests exposed shared TLS-detection state races and a test-harness
false-PASS boundary. Spec007 records the subsequent corrections and reruns.

Scenario child Core builds now inherit `-race`. Stderr race diagnostics are
remembered across chunk boundaries and cause cleanup to fail even if a child
exits zero; cleanup joins stderr forwarding before checking the result.
`go test -race ./testing/scenarios -run '^TestScenarioRace' -count=3
-timeout=1m` passes (8.065s), including zero-exit child negative controls for both
single-server and all-server cleanup. This makes future scenario acceptance
stricter; earlier child processes were not equivalent validation.

The AnyTLS private-protection failure was traced to upstream Freedom's protocol
name switch omitting the local `anytls` inbound. Added it alongside VLESS/Hy2,
retaining explicit final-rule overrides. `go test -race ./proxy/anytls -run
'^TestAnyTLSDefaultFreedomPrivateProtection$' -count=3 -timeout=2m` passes
(7.871s): default private traffic reaches no target; explicit allow reaches it.
This is a security contract fix, not a test expectation relaxation.

### 2026-10-15: regression and migration checkpoint

The complete unfiltered scenario race suite passed in 1088.169s with a 45-minute
aggregate budget: 77 top-level tests / 90 test and subtest pass events, zero
skips or race reports. Child Core processes were built with `-race=true` and
race diagnostics fail the parent. The earlier 15-minute timeout is retained in
[spec007 evidence](../007-core-regression/upgrade-20261014.md). Hy2 lifecycle
source changed during this run, so this is not a frozen final-tree result.

Real certificate-backed migration validation reached 43 passing runtime configs,
including regenerated local configuration. Two cases are still under review:
legacy protocol chaining in `hq`, and duplicate SS2 keys for `admin` and
`la.akile.lite` on LA Akile. No production credentials or tracked runtime configs
were changed to bypass these failures. Configuration staging is private and has
no apply/deploy operation.

Sidecar's real mixed-protocol matrix exposed additional Core issues: cross-instance
dialerProxy ownership, a Unix path encoded as a SOCKS CONNECT reply address, and
Hy2 resources retained after instance shutdown. Corrections and their repeated
tests are still in progress; passing earlier package tests does not close these
integration gates. Sidecar snapshot/restart and 44 node YAML checks are separate
from these failures.

Public tests were explicitly enabled as well: existing local Xray SOCKS port 1089
passed ten DNS/ECH cases, but all three AdGuard DoQ cases timed out both through
that proxy and directly. No active local proxy configuration was changed. These
failures remain failures pending diagnosis; offline DoQ fixture success does not
establish public-network reachability.

### Local build checkpoint

Save this integration as a local spec010 checkpoint to satisfy the clean-tree
official build guard, not as a release approval. It includes the adaptations of
spec002-009 and their owning-spec evidence; no canonical branch/pin is adopted.
Final long-running scenario/AnyTLS gates and duplicate-key migration remain open.

After UDP worker retirement was repaired, the package race partition excluding
the separately run `testing/scenarios` and `proxy/anytls` trees passed with 2649
test/subtest passes and 21 explicit opt-in skips. Those skips are the thirteen
public DNS/ECH tests, the independently executed SS soak, and seven credentialed
live XDrive/Yandex tests. They are not counted as passes. The earlier normal
partition passed with 2646 passes/21 skips, before the three new worker tests.
Ordinary scenarios passed in 280.956s (before the worker patch); the updated
complete scenario race gate is running separately. Whole-tree vet and formatting
passed at their recorded checkpoints and must be repeated after final edits.

The coverage child entry also needed correction: test-runner flags and Core
arguments now use a `--` boundary, and coverage output defaults to an owned
directory when XRAY_COV is absent. Its previous UDS scenario failed to start;
race-enabled coverage UDS/diagnostic tests now pass in 9.984s. This changes only
the coverage test executable and harness, not production command parsing.

### Resumed Fixes, 2026-10-15

The user explicitly requested continuing to fix the failed gates. This is not a
request to deploy or adopt the candidate on canonical develop.

- Candidate LA Akile admin credentials are separated from its node identity,
  with ASZ's management caller updated together. Existing business/Hy2 keys
  remain unchanged. Config's 45/45 parser migration and 519 tests pass against
  the recorded checkpoint; a real SS2 test proves exact per-user counters and
  that removing admin preserves node traffic. Final-pair rerun remains required.
- The new SS transport half-close tests pass race count=10, Linux/amd64 count=20,
  and fail under a deliberate timeout-selection mutation. Full SS/inbound race
  count=3 passes. See SAFETY.md for commands and scope.
- Stats concurrent manager Close now has an explicit 128-caller regression:
  channel removal, stopped state and subscriber closure/cleanup are checked.
  Atomic Stats tests pass race count=30 (1.880s). Channel.Start always returns nil
  in this implementation; tests.md records the absence of a fallible production
  path rather than claiming an injected failure was tested.
- U-011 now has independent [symlink ownership evidence](../009-direct-control-listener/u011-symlink-20261015.md)
  (file/live socket/stale socket/dangling target, Darwin race x10 and Linux
  race x5) and [system-only CA evidence](../007-core-regression/u011-system-ca-20261015.md)
  (five Linux subprocess trust controls, race x5). No host trust store or
  production TLS settings changed; neither gate disables certificate checking.
- Linux/amd64 executable tests exposed a listener-counter fixture publication
  race: receiving bytes does not join CounterConnection.Write's later increment.
  Exact assertions now wait for publication; repeated Darwin race and translated
  Linux runs pass without changing runtime counters or their byte semantics.
- Updated non-AnyTLS/non-scenario package partitions: normal and race each
  **2653 test/subtest passes, 21 opt-in skips, exit 0**. Complete scenario race:
  **90 passes, no skips, 1079.708s**, exit 0. Logs are
  `/tmp/core-upgrade-resume-packages-{normal,race}.jsonl` and
  `/tmp/core-upgrade-resume-scenarios-race.jsonl`. These do not establish AnyTLS
  acceptance while its independent repair is underway.
- AnyTLS diagnosis found peer FIN could cancel an already-delivered physical
  response write and close unrelated streams. Engine and UoT business-path
  regression controls reproduce the defect. Timer ownership during peer FIN,
  read-triggered handshake and shutdown is under review; final frozen-source
  normal/race/soak evidence is still required, not inferred from a short rerun.
- Sidecar additionally fixes stale H1 cancellation state across Transport retry
  acquisition. Deterministic virtual-time/network-pipe tests replace wall-clock
  20ms scheduler assumptions without widening production timeouts. Independent
  native-client and live usersync checks continue in its spec006.
- Config's TUI module is now tested against the candidate dependency graph;
  its race tests and vet pass. Exact final Core pin/build remains pending.

Canonical Core/Sidecar/Config worktrees remain unchanged and clean. The old
be9f5afd/d8381f0 formal artifacts do not include these new fixes. Do not deploy
them as a completed upgrade or mistake pending tests for passed gates.

### Further Shutdown Defects

The v4 AnyTLS normal soak passed 601.09s, but race teardown exposed a shared
[ActivityTimer self-deadlock](../007-core-regression/activity-timer-upgrade-20261015.md).
Its correction and deterministic negative regression are now present; full
integration reruns remain required. Independently, Sidecar's real TCP/UDS
fixture proves accepted connections continued forwarding after Core.Close.
[Worker-owned admission/cancellation/retirement](../002-core-connection-lifecycle/accepted-stream-retirement-20261015.md)
now fixes this boundary; focused TCP/UDS race tests pass ten repetitions. The
subsequent real Sidecar churn also found a separate
[UDP dispatcher retirement race](../002-core-connection-lifecycle/udp-dispatch-retirement-20261015.md).
Its plain-bool negative control fails under race detection, and the fixed package
passes ten repetitions. Neither defect is waived by a later successful repeat
or a larger timeout; final whole-integration reruns remain required.

The next normal non-AnyTLS/non-scenario partition exits zero. Its race partition
fails in Sudoku's first subprocess startup (TCP connection refused); a harness
diagnostic/readiness correction is being verified separately. The next full
scenario race exits nonzero at VLESS Vision/Reality because local loopback port
allocation/dial returns `can't assign requested address`. This is recorded as a
failed run, not evidence of protocol success or proof of a particular host cause.
Logs: `/tmp/core-upgrade-final-shutdown-{normal,race,scenarios}.jsonl`.

Follow-up attribution: the concurrently launched Mihomo diagnostic100 workload
recorded 16,319 TIME_WAIT sockets and EADDRNOTAVAIL at the same 02:36 boundary.
It has been stopped. Independent Vision/Reality race tests subsequently pass in
9.500s, but the failed full run remains failed. Final socket-heavy acceptance
must run serially, not alongside repeated external-client diagnostics. AnyTLS
v5 normal/race also fail: earlier Hy2 relay recovery and intermittent Mihomo
first-request failures precede exhaustion and remain actionable defects; later
port-allocation failures cannot explain those earlier observations. Neither v5
soak is accepted. Whole-tree go vet and the gofmt gate pass at this checkpoint.

Both early integration failures now have independent reproducers and scoped
repairs: [Hy2 listener retirement](../002-core-connection-lifecycle/hy2-listener-retirement-20261015.md)
notifies the peer before destroying transport and passes the unchanged fresh-ID
recovery fixture; AnyTLS's UPGRADE report records Mihomo's provider-loading
readiness barrier and old-helper negative control. The
[Sudoku harness](../007-core-regression/sudoku-harness-upgrade-20261015.md)
now observes child startup/exit and propagates child race failures. Final serial
source verification and official artifacts are the next gates.

The next serial complete scenario race run at `8e246952` failed after 800.457s:
the reverse heartbeat writes its drain flag concurrently with worker selection.
The child-race guard correctly rejected this run. A
[narrow atomic publication repair](../007-core-regression/reverse-drain-upgrade-20261015.md)
has an independent baseline-failing regression and package race x50 pass.
Complete scenario acceptance must be rerun after this repair; the preceding
normal scenario pass (90 events, 281.215s) is not substituted for it.

The complete scenario race rerun now passes at aa6ca3ed: 90 events, no skips,
1053.085s. Linux/arm64 ten-package runtime race also passes (283 events, three
explicit skips). Sidecar normal/race each pass 1020 events, but its subsequent
1000-chain resource gate fails a Hy2-to-SOCKS request at batch36. P10/P11 remain
open while that independent failure is investigated. See the current local
verification summary; do not infer stability from the shorter full-suite passes.

The Hy2 follow-up now has an independent natural endpoint-collision trace and a
deterministic wire baseline-red/fix-green regression. The scoped
[loopback source repair](../002-core-connection-lifecycle/hy2-loopback-source-20261015.md)
preserves explicit Gateway, proxy chains and cancellation; it changes neither
ChromeParrot nor budgets/retries. Full Hy2 race, 1000 fresh handshakes and the
unchanged 1000-chain race gate pass. The earlier failed run remains failed;
original same-cause attribution is explicitly an inference. P11's last changed
leaf and final artifact checks remain to be closed, not skipped by this repair.

### Local Verification Closed

Core runtime is frozen at d2cda3ef; subsequent changes are evidence only. The
changed-leaf Hy2 race passes on Darwin and Linux, affected AnyTLS chain/process
race passes (121 events), and Sidecar final normal/race each pass 1020 events.
The unchanged 1000-chain resource gate passes after the independently reproduced
Darwin source-binding repair. Full source vet and strict upstream formatting pass.

The official clean-tree wrapper built c0c0cbf7 for Darwin/arm64 and Linux/amd64
with VCS metadata, executing Darwin version. Its artifact passes 45/45 staged
runtime configs, with one ready local declaration, 47 changes across 42 of 46
documents, zero blockers and complete=true. No stage was applied. Old Sidecar
501ed69 passes all seven real binary control/query assertions against it;
enabled native SS usersync mutation separately passes in the final Sidecar race.

The final evidence-only commit is rebuilt so its release metadata matches the
committed source. Exact paired artifacts, consumer pins, TUI and Config checks
are recorded by Config's candidate audit. Those operational records do not
authorize adoption or deployment. P12 remains deliberately separate.
Windows execution is a user-deferred TODO; public DoQ reachability remains a
recorded external failure, not part of the passed offline gates. No universal
cross-platform/public-network or production-soak green claim is made.
