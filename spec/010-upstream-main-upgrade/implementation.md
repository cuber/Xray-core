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
- [ ] P02 Establish targeted upstream baseline; port spec002 dispatch lifecycle
  and independent tests without restoring removed SS bridge/legacy proxy path.
- [ ] P03 Port shared spec007 readv/regression changes and later spec002
  retirement/validation capabilities; adapt Windows readiness behavior.
- [ ] P04 Port spec003 multi-listener, SOCKS/Unix redirect contracts and tests.
- [ ] P05 Port spec004 OB and prerequisites, preserving 3s and full cancellation.
- [ ] P06 Port spec005 routing metadata/weighted selection and spec006 domain
  traffic against atomic upstream APIs; regenerate reviewed protobuf.
- [ ] P07 Port spec009 API UDS ownership beyond upstream's basic UDS support.
- [ ] P08 Port spec008 AnyTLS, required Sing dependencies and new chain matrix.
- [ ] P09 Resolve native SS gates U-001/002/003 and shared U-004..012;
  no source-level concern is declared fixed without a regression reproducer.
- [ ] P10 Integrate isolated Sidecar/config migrations; no production pin change.
- [ ] P11 Full uncached/race/interop/chain tests, format/vet, Darwin/Linux builds
  through an explicitly candidate-aware wrapper; do not weaken normal develop
  release protections. Record every failure and skip.
- [ ] P12 Review/adopt candidate and release pins only on explicit approval;
  production rollout is a separate operation.

## Verification

Each phase runs its owning spec tests; the additional U-* matrix in tests.md
applies throughout. Partial phase success is not full upgrade acceptance.
Never copy a prior fork's green report into this ledger as candidate evidence.

Go source changes require the official dual-platform build workflow before
release; this branch is an incomplete integration candidate, not a release.
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
