# Spec010 Sidecar Evidence

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

Date: 2026-09-27. Scope: V-024/V-026/V-027, extended to V-025 after the user
selected the best-effort cache contract. No commits, rebases, version-pin edits,
production services, or deployments were performed by this task.

## Checkout and Contract

- Sidecar started clean on master at `d96dba326614ffd8df11d410137c7576ecf98a36`.
- Its unchanged go.mod replaces Core with `../xray-core`, clean at
  `e7a2197424614e47d3e728da1d9cdded3454f559` during the original-Core checks.
- Final cross-consumer tests used a temporary modfile pointing at the dirty
  authorized `../xray-core-spec` worktree, not a new RPC or a version-pin change.
- Both Core and Sidecar now discard LRU detail directly. Sidecar does not
  retract previously collected data when Core evicts it. Sidecar's own budgets
  and retention can still discard that history. No new eviction bytes enter
  other; old Core/snapshot other and anonymous unknown remain readable.

## Files

All source paths below are relative to `../xray-sidecar`:

- `domain_traffic.go`: direct-discard eviction and atomic validation of legacy
  duplicates, alignment, missing/orphan indexes, boot/cursor, and anonymous
  other/unknown summaries in both snapshot schemas.
- `domain_traffic_test.go`, `domain_traffic_user_test.go`: update eviction
  expectations to the explicitly authorized lossy contract.
- `domain_traffic_rpc_test.go`: real Core stats manager and gRPC on ephemeral
  loopback ports; paging, repeated cursor, open latest, old cursor/gap, boot
  merge, and two actual early/late Sidecar consumers.
- `domain_traffic_budget_test.go`: three virtual days, exact combination/entry
  limits, shared identities across buckets, cutoff boundaries and idle expiry.
- `domain_traffic_snapshot_test.go`: malformed snapshots, failure atomicity,
  legacy migration, smaller entry budget, filesystem modes/failures, concurrent
  Apply/Result/Cursor/Save and restore of internally consistent snapshots.
- `domain_traffic_process_test.go`: readiness-controlled test subprocesses,
  actual SIGKILL/restart, persisted versus unsaved state, replay, boot merge,
  malformed-snapshot error reporting and real temporary HTTP availability.

## Commands and Results

Commands ran from `/Volumes/Linux/opensource/cuber/xray-sidecar` unless noted.

1. Initial new regression subset against original Core, before validation fixes:

   ```sh
   go test -race -count=1 -timeout=5m . -run '^TestDomainTraffic(MalformedSnapshotsAreAtomic|ThreeVirtualDaysExactBudgets|BoundaryAndSharedIdentityEntryBudget|LegacySnapshotBoundaryBudgetsAndReplay)$'
   ```

   Exit 1, 1.462s. Nine malformed inputs were accepted: schema1 duplicate entry,
   duplicate bucket, orphan last_seen, unaligned time, cursor without boot,
   named other/unknown, plus schema2 named other/unknown. These failures were
   reproduced before adding validation. After fixes the original-Core focused
   run passed (11.377s); a three-repeat run passed (31.691s,
   `/tmp/xray-sidecar-spec010-race.jsonl`). Those earlier eviction expectations
   preceded the user's final contract and are NOT the final acceptance evidence.

2. Historical V-025 run against unchanged original Core (the consumer test's
   incorrect producer-specific assertion was subsequently corrected below):

   ```sh
   go test -race -count=1 -timeout=5m . -run '^TestDomainTrafficRPCBestEffortEarlyLateCollection$'
   ```

   Exit 1, 2.541s. Late consumer got 130/240, including other=100/200, instead
   of the then-asserted retained 30/40. This distinguished producer semantics
   but incorrectly failed Sidecar's valid backward-compatible handling. The
   Core contract remains strict in Core tests; this is not a valid reason to
   fail the standard Sidecar build on its unchanged development pin.

3. Final cross-consumer run, after all code changes:

   ```sh
   cp go.mod go.sum /tmp/xray-spec010-sidecar.3HxdNu/
   go mod edit -modfile=/tmp/xray-spec010-sidecar.3HxdNu/go.mod -replace=github.com/xtls/xray-core=/Volumes/Linux/opensource/cuber/xray-core-spec
   make check-fmt
   go test -modfile=/tmp/xray-spec010-sidecar.3HxdNu/go.mod -json -race -count=3 -timeout=5m . -run '^TestDomainTraffic' > /tmp/xray-sidecar-spec010-best-effort-final.jsonl
   ```

   Exit 0; package PASS in 37.522s. 21 distinct top-level scenario tests, each
   repeated three times; the separate subprocess helper is intentionally SKIP
   in the parent process and is actually exercised by the two subprocess tests.
   No race reports. `make check-fmt` and `git diff --check` passed.
   `git diff -- go.mod go.sum` was empty. Temporary modfile and logs remain for
   parent inspection; test-created snapshots/listeners/processes were cleaned.

## Observed Oracles

- RPC page1 seq=1, page2 seq=2, open latest=3 but cursor=2; closing it yields
  450/660 exactly once. New Core boot seq=1 adds 7/11, giving 457/671.
- An expired cursor reports has_gap=true, oldest=2 and only retained 3/4 bytes;
  the consumer does not invent missing traffic.
- V-025 Core capacity1: early alice+bob=130/240; late bob=30/40; both other=0.
  Re-reading the full response into the early consumer does not retract alice.
- Three virtual days, 432 steps per budget profile, 32 fresh combinations per
  step and duplicate replay: buckets=144/145/145, entries and LRU identities
  exactly 7/7/7 or 9/9/9. Every step verifies actual map counts, index membership,
  cutoff/alignment and independent totals `(16B+K)/(20B+2K)`. Old other input
  stays `11B/13B`; unknown stays `5B/7B`; evictions add neither.
- First final-run GC heap samples (bytes): combination budget
  1534408/1543824/1543216; entry budget 1580264/1580592/1586184.
  Snapshot bytes: 25265/25592/25616 and 25555/25880/25900. These support the
  exact structural bounds; there is no vacuous RSS > 0 or universal heap claim.
- Snapshot mkdir and final rename failure both preserved the old bytes and
  left no temp snapshot files. First-run old SHA-256:
  `b0b92c407d7b5630dac464b30a46359dcdc1ef0d3d4a29fa7c1e941febc8a7a5`.
  Each test compares before/after bytes, not a global constant; ordering of
  same-domain entries can give different valid hashes on separate saves.
- New snapshot directory=0700, file=0600. Schema1 migration with a reduced
  entry budget loses the evicted 400/600 detail, preserves legacy other=17/19
  and unknown=2/3, retaining total69/82 without inventing users.
- Subprocess save acknowledges 450/660 and cursor3; SIGKILL after unsaved9/13
  restores only450/660. Replay adds9/13 once; new boot adds7/11 =>466/684.
  A second kill/restart restores that state and rejects duplicate sequence.
- Malformed JSON produces `snapshot recovery: decode snapshot: unexpected end
  of JSON input`; the temporary HTTP handler remains enabled/available.

## Limits and Handoff

- The subprocess is a real re-executed test binary using production aggregator
  and HTTP handler methods, not the production main/service or minute timer.
- No simulated disk-full/write syscall/fsync failure or power-loss directory
  durability claim. Faults cover deterministic parent-file and rename failures.
- Heap samples measure this test process, not a fleet RSS ceiling or an
  adversarial arbitrarily large snapshot decoder allocation bound.
- The initial Sidecar V-025 assertion required upgraded Core. The compatibility
  correction below removes that build dependency without skipping tests or
  relaxing the Core-owned direct-discard contract.
- Full suite and release acceptance remain parent-owned. The Core build-wrapper
  limitation and narrow runtime fixes are in `evidence-010-core.md`.

## Parent Handoff

After the focused evidence above, the parent independently reported Sidecar
full-suite race exit0 (458 pass events, 7 skips) using a temporary modfile
targeting xray-core-spec, Sidecar vet exit0, and TUI full tests exit0. These are
parent-reported results, not additional commands executed by this task; the
parent owns their full logs and final acceptance. Default development pin is
unchanged. Core and Sidecar source changes from this task are frozen for merge.

## Post-Commit Compatibility Correction

The parent committed Sidecar as `7e71c4c`, then identified that its normal build
still legitimately links original Core e7a21974. This follow-up changes only
`domain_traffic_rpc_test.go` in Sidecar, plus evidence/spec test documentation;
no runtime code, Core test, go.mod, go.sum, or production pin is changed. No
follow-up commit is made; the parent will amend its single spec010 commit.

- `TestDomainTrafficRPCBestEffortEarlyLateCollection` now owns consumer
  compatibility: early detail stays alice100/200+bob30/40 (130/240), with no
  retraction or replay duplication. Late detail and user summary are exactly
  bob30/40. Late other must exactly equal the aggregate other in the actual
  RPC, including its anonymity and total, rather than being discarded.
- Thus old Core yields late detail30/40 plus other100/200, total130/240;
  upgraded Core yields late detail30/40 plus other0, total30/40. Neither case
  permits Sidecar to manufacture, erase, or assign anonymous bytes to bob.
- New independent `TestDomainTrafficNewCoreResponseEarlyLateCollection`
  explicitly constructs seq1 emptied by direct discard and seq2 bob30/40.
  Regardless of linked Core, it strictly requires early130/240, late30/40,
  other0 and cursor2 after replay. No skip or version detection is used.
- Core `TestDomainTrafficLRUDiscardsUncollectedHistory` and all other strict
  producer tests remain unchanged and retain ownership of new Core behavior.

Follow-up commands executed from Sidecar after this correction:

```sh
gofmt -w domain_traffic_rpc_test.go
make check-fmt
go test -json -race -count=3 -timeout=5m . -run '^TestDomainTraffic' > /tmp/xray-sidecar-spec010-compat-default.jsonl
go test -modfile=/tmp/xray-spec010-sidecar.3HxdNu/go.mod -json -race -count=3 -timeout=5m . -run '^TestDomainTraffic(RPCBestEffortEarlyLateCollection|NewCoreResponseEarlyLateCollection)$' > /tmp/xray-sidecar-spec010-compat-new-core.jsonl
git diff --check
```

All exit0. Default-pin domain suite PASS37.739s, three repeats; RPC other was
exactly100/200 each time. Patched-Core two-test run PASS6.930s, three repeats;
RPC other was exactly0/0 each time. The independent new-response test passed
under both producers. This follow-up did not rerun the entire Sidecar suite or
release build. Only `domain_traffic_rpc_test.go` is dirty in Sidecar; no commit
was made. Source is frozen again for the parent's amend.
