# Specs 008/009 test implementation evidence

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

Date: 2026-09-27. Worktree: `/Volumes/Linux/opensource/cuber/xray-core-spec`.
Observed HEAD: `a603b44ecc9db77370869e1900e54b4d149d767f`.
Toolchain: `go version go1.26.1 darwin/arm64`.

All core changes below are uncommitted in the isolated shared worktree. No
checkout, commit, rebase, original-core edit, or deployment was performed.
Other agents' files were preserved, including the 011 cancellation change in
`app/router/command/command.go`. Tests execute the shared cumulative tree; they
are not evidence that other agents' changes have been independently reviewed.

## File ownership for folding

Paths in this table are relative to `xray-core-spec`, not the original core.

| Spec | File | Change |
|---|---|---|
| 008 | `app/observatory/burst/contracts_http_test.go` | New V-010 through V-014 integration/state/concurrency tests (9 top-level tests) |
| 008 | `app/observatory/burst/healthping.go` | Scheduler cancellation, tracked workers/timers, bounded selector wait, publication barrier |
| 008 | `app/observatory/burst/ping.go` | Bind HTTP requests to the probe context |
| 008 | `app/observatory/burst/manager.go` | Document resolver lifecycle constraint |
| 008 | `infra/conf/contracts_observatory_test.go` | JSON group validation, independent settings, 3s/2s/20 preservation (2 tests) |
| 009 | `app/router/contracts_balancing_test.go` | V-015 through V-017 weighted/health/user/leastLoad matrix (6 tests) |
| 009 | `app/router/strategy_weightedleastping.go` | Literal first-match cached weights; exclude nonpositive and nonfinite weights |
| 009 | `app/router/command/contracts_rpc_test.go` | V-018 real TCP gRPC lifecycle, concurrency and compatibility (4 tests) |
| 009 | `infra/conf/contracts_balancing_test.go` | JSON rejection of zero/negative and preservation of fractional weights (1 test) |

This evidence file belongs to the 005 validation umbrella and documents both
008 and 009. No other spec/task/status documents were edited by this worker.

## Actual runtime changes

### 008: stop, cancellation and publication

The new stop test initially failed with `StopScheduler left live HTTP request`.
Previously Stop only stopped the ticker; delayed callbacks and HTTP requests
survived it, and HTTP requests had no probe context.

The fix gives each scheduler run its own cancelable context, serializes
Start/Stop with `schedulerMu`, and joins tracked scheduler/probe workers before
Stop returns. Randomly distributed samples retain their existing scheduling
semantics, but their timers can now be canceled and joined. The HTTP requests
and optional connectivity check share that context, and idle connections close
after the requests finish. The settings normalization is unchanged: 3s interval,
2s timeout, and 20 samples remain exactly those values.

Stop acquires the result lock when canceling. Scheduled publication checks the
context while holding that same lock and uses a lock-held writer. Consequently,
a scheduled result either publishes before this cancellation barrier or is
discarded; cancellation cannot occur between the eligibility check and the
scheduled write. Public `PutResult` is still usable independently, and Stop is
not a ban on explicit caller writes or unrelated one-shot `Check` calls.

WaitGroup additions originate from workers already counted before Stop can
wait; a new Start cannot reuse the group until Stop's wait completes. Tests
exercise 8 concurrent callers, each doing 20 Start/Stop pairs.

The existing selector signature cannot cancel arbitrary user code. Selection
therefore runs behind a buffered result channel; canceling stops waiting for
the callback rather than indefinitely blocking Stop. The documented contract
requires a concurrency-safe selector that returns promptly and releases its
own resources. A violating callback can outlive Stop until it returns; it is
NOT forcibly terminated or claimed to be reclaimed. The blocked-selector test
starts both initial/scheduled callbacks, proves Stop returns before releasing
them, then releases and observes both callbacks for cleanup. Late selection
results do not start successful probes or publish samples.

### 009: literal finite positive weights

The new fallback test initially observed `[a b negative zero]` instead of
`[a b]`. The shared WeightManager treats nonpositive weights as automatic
numeric/default weights for leastLoad; that made weightedLeastPing's existing
`weight <= 0` filter ineffective for raw protobuf settings.

Only weightedLeastPing now reads literal first-match weights. It retains the
same substring/regex matcher, matching order, default weight 1, invalid-regex
skip behavior, and mutex-protected per-tag cache. Zero, negative, NaN and either
infinity are excluded from both healthy and fallback pools. An invalid first
matching weight does not fall through to a later valid match, and numeric tag
suffixes cannot resurrect zero/negative values. NaN/infinite weights are
explicitly rejected from the pool because they make round-robin arithmetic
nonfinite. JSON already cannot encode NaN/Inf; raw protobuf inputs can.

The shared WeightManager and leastLoad automatic numeric weighting are not
changed. JSON validation still rejects zero/negative configuration weights.

## New coverage

| ID | Exact new coverage |
|---|---|
| V-010 | Two real loopback HTTP endpoints and independently timed manager checks: A times out, B succeeds while A is in flight, results remain group-local, unmatched tags ignored; runtime legacy-empty manager does not resolve or probe. JSON tests cover identical/nested cross-group selectors, same-group duplicates/nesting, null/missing/empty groups, independent URL/interval/timeout/sampling/method/keepAlive. |
| V-011 | Actual tagged-dialer/HTTP requests for a-x, a-b-x, z capture `/a?ob=x`, `/ab?ob=y`, `/default?ob=z&raw=%2F`; exact RequestURI and healthy results asserted. |
| V-012 | Real HTTP 204/200/302/503/timeout with error sentinel assertions, redirect target has zero visits; ten requests per client produce exactly ten accepted connections with keepAlive=false and one with true. Request remote addresses logged; clients and servers closed. |
| V-013 | Capacities 1/2/20; S, SF, FSSS, FSFSSS checked sample by sample against independent alive/streak expectations. Healthy cache is populated, sample timestamps moved beyond validity without changing system time, stale cache becomes dead/empty, then recovery requires 1/2/3 successes. No sleep-based expiry oracle. |
| V-014 | Blocked WalkResults callback does not block concurrent PutResult; mutation of returned stats does not affect next snapshot. Stop cancels real in-flight HTTP and delayed samples without publishing failures, server active count returns to zero. 3s/2s/20 invariant tested at runtime and JSON build. Concurrent Start/Stop, blocked-selector stop, and canceled successful request publication covered. Existing reentrant callback test also passes. |
| V-015 | Fresh strategy per serial/concurrent run, fake Observatory with equal health/RTT, public PickOutbound used 600 times; serial and 12 workers x 50 both yield exactly a=300,b=200,c=100 for weights 1.5:1:0.5. Counter has an independent mutex. No byte-ratio claim. |
| V-016 | Full pool plus 60-choice counts for maxRTT 249/250/251ms, relative 149/150/151ms against best=100ms/delta=50ms, failure 3/4/5 of 20 at tolerance=.2, disabled tolerance, samples 4/5/6 at minimum 5, dead/missing statuses, Delay fallback and insufficient non-burst samples. Burst Average deliberately disagrees with Delay. Error/empty/wrong-type/dead observations fall back to all positive candidates; duplicates, removal/current-state purge, recovery, empty pool, nonpositive/nonfinite weights, regex/substring first-match tested. |
| V-017 | Exact user case sensitivity; case-insensitive last-@ full domain including multiple @, no subdomain match, missing @, trailing @, empty user/domain rule, empty local-part, valid/invalid regex, and empty `regexp:` literal behavior. leastLoad failure 3/4/5 of 20 tested at tolerance=.2 and 0. Each user case logs input and result. |
| V-018 | Real loopback TCP gRPC server/client: static ordered detailed rules; append third; remove second; duplicate append rejected without changing rules; shouldAppend=false replaces entire list, not per-tag upsert; missing removal idempotent, empty removal rejected. Nested domain/IP bytes/users/target mutations of both RPC and direct-service responses do not affect subsequent ListRule. Four workers each perform 100 Add/List/mutate/Remove/List cycles, assert own rule existence/removal and static order. Another test runs 100 whole-list replacements against four readers x 100 lists, with no mixed/partial snapshots. Legacy protobuf wire fields 1/2 remain readable while skipping detail field 3. |

## Commands and actual results

Unless stated otherwise, cwd was `/Volumes/Linux/opensource/cuber/xray-core-spec`.

Reproduced stop defect before changing runtime:

```sh
go test -race -count=1 -timeout=30s ./app/observatory/burst -run '^TestContractSchedulerStopCancelsInflight$'
```

Exit 1: `StopScheduler left live HTTP request`; package FAIL in 1.504s.

An initial combined `go test -race -count=1 -timeout=5m
./app/observatory/burst ./app/router ./app/router/command -run '^TestContract'`
returned exit 1: the new RPC test initially had an incorrect geodata import
(corrected to `common/geodata`), and all four weighted fallback cases exposed
the nonpositive-weight defect above. The tests were not weakened to accept it.
Subsequent focused four-package contract run passed before the final expansions.

Final complete target-package regression:

```sh
go test -json -race -count=1 -timeout=5m ./app/observatory/burst ./app/router ./app/router/command ./infra/conf > /tmp/core-spec-008-009-race.json
```

Exit 0. Package PASS elapsed: burst 6.037s, router 5.904s,
router/command 7.945s, infra/conf 7.355s. 277 test/subtest pass events;
117 are new Contract test/subtest events (22 new top-level tests).
Zero failures and zero skips. Existing tests in all four packages also ran.

Final repeated new-contract race regression:

```sh
go test -json -race -count=10 -timeout=5m ./app/observatory/burst ./app/router ./app/router/command ./infra/conf -run '^TestContract' > /tmp/core-spec-008-009-repeat.json
```

Exit 0. Package PASS elapsed: burst 11.611s, router 5.692s,
router/command 41.071s, infra/conf 6.317s. 1170 test/subtest pass events,
zero failures and zero skips. These include 10 repetitions of concurrent
Start/Stop and both real RPC concurrency scenarios.

Formatting and diff hygiene:

```sh
gofmt -w app/observatory/burst/contracts_http_test.go app/observatory/burst/healthping.go app/observatory/burst/manager.go app/observatory/burst/ping.go app/router/contracts_balancing_test.go app/router/strategy_weightedleastping.go app/router/command/contracts_rpc_test.go infra/conf/contracts_observatory_test.go infra/conf/contracts_balancing_test.go
gofmt -l app/observatory/burst/contracts_http_test.go app/observatory/burst/healthping.go app/observatory/burst/manager.go app/observatory/burst/ping.go app/router/contracts_balancing_test.go app/router/strategy_weightedleastping.go app/router/command/contracts_rpc_test.go infra/conf/contracts_observatory_test.go infra/conf/contracts_balancing_test.go
git diff --check
```

All exit 0; `gofmt -l` and `git diff --check` print nothing.

Required build-wrapper attempt, cwd `xray-config`, isolated source/output only:

```sh
env XRAY_CORE_SRC=/Volumes/Linux/opensource/cuber/xray-core-spec XRAY_BUILD_DIR=/tmp/core-spec-008-009-build ./xray-core/build.sh
```

Exit 1: `FATAL: /Volumes/Linux/opensource/cuber/xray-core-spec is not a git repo`.
The wrapper requires `.git` to be a directory; a legitimate linked worktree has
a `.git` file. It exits before any checkout or artifact write. It also requires
a clean tree for release builds. No workaround, commit or original-core build
was performed. Darwin/Linux release artifacts remain unbuilt for these changes;
the parent must run the approved build after review/folding in a suitable tree.

JSON artifact SHA-256 values (`shasum -a 256`, exit 0):

- `/tmp/core-spec-008-009-race.json`: `47d26eec7b95963d37214ac99a15157f1e360be2b77bbea53637262ec3584e1d`
- `/tmp/core-spec-008-009-repeat.json`: `7e575300503204aa269d473d2783b667e89454d5b3ad79bc549218733f8080ee`

## Remaining boundaries / gaps

- No missing core-only V-010 through V-018 scenario identified in the assigned
  matrix remains unimplemented. This is not full cross-component acceptance.
- Actual Sidecar/TUI client decoding was not run or changed: it lies outside the
  assigned core module ownership. The new legacy-wire decoder test is NOT
  evidence of those actual clients passing.
- The HTTP integration hook replaces only tagged dialing with a loopback TCP
  dialer; HTTP transport, timeout, redirects, scheduling and state are real.
  It does not exercise deployed Xray dispatch/remote routes or external services.
- A permanently blocked external selector cannot be forcibly reclaimed; Stop
  returns without waiting, but callback resource ownership remains with its
  caller as documented above. Test callbacks are explicitly released.
- Cache expiry uses controlled sample timestamps rather than an injected global
  clock. It asserts an unequivocally expired window, not nanosecond-exact equality
  at the validity boundary. Scheduler timing is not asserted as exact 3s pacing.
- Runtime cancellation and nonfinite/raw nonpositive weight behavior are actual
  fixes, so the cumulative tree is intentionally no longer byte-identical to
  the historical reconstruction. They require parent review/folding per spec.
- Full repository tests and Darwin/Linux release builds are not claimed PASS.
  No production deployment or original core modification was performed.
