# Spec010 Core Evidence

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

Date: 2026-09-27. V-019 through V-025 implemented/tested in
`/Volumes/Linux/opensource/cuber/xray-core-spec`, branch
`refactor/spec-series-20260927`, observed HEAD
`a603b44ecc9db77370869e1900e54b4d149d767f`.
Other agents' dirty work was preserved. No commits, rebases, pin edits or deploys.

## Authorized Contract and Fixes

The user selected a bounded best-effort cache, not a complete byte ledger:
Core and Sidecar LRU discard detail directly; neither manufactures other.
Previously collected Sidecar history is not retracted by Core eviction.
Late collection may lose data. Ordinary Stats counters retain their existing
complete accounting independently of this cache.

- `app/stats/domain_traffic.go`: discard LRU details without moving bytes;
  clamp backward wall time to the last observed logical time for both Record
  and Snapshot. UTC strips Go's monotonic component before wall comparison.
  This prevents reopening consumed buckets and sequence/time inversion.
  Avoid `afterSequence+1` overflow in gap detection. No RPC/proto changes.
- `app/dispatcher/default.go`: cachedReader now retains data accompanying EOF
  or another read error, while returning the original error to sniffing. The
  buffered payload is replayed without re-entering the domain Stats reader.
- `app/stats/domain_traffic_test.go`, `domain_traffic_user_test.go`: update the
  old lossless-eviction assertions to the explicitly selected lossy contract.
- New `app/stats/domain_traffic_contract_test.go`: early/late cache views,
  time/cursor boundaries, saturation, authenticated unknown, tie eviction,
  concurrent writes/snapshots and separation from complete Stats.
- New `app/stats/command/domain_traffic_contract_test.go`: real loopback gRPC
  disabled versus enabled-empty domain stats alongside ordinary Stats=123.
- New `app/dispatcher/domain_traffic_contract_test.go`: three-identity byte
  oracle, read data+errors, timeout capability, writer failure/order,
  attribution priority and actual WrapLink/sniff-cache replay.

Generated protobuf changes observed elsewhere in the worktree were not made
by this task and are not part of these runtime fixes.

## Negative Evidence

Before the fixes, this command exited 1 (0.434s):

```sh
go test -race -count=1 -timeout=5m ./app/stats -run '^TestDomainTraffic(LRUDiscardsUncollectedHistory|BoundariesIdleAndRollback|PagedCursorOpenLatestGapAndBoot|ConcurrentRecordSnapshotAndCompleteStats)$'
```

- Late consumer retained130/240 instead of30/40 because of old other transfer.
- After105->99 rollback, response oldest=2 but latest=1 (time/sequence inversion).
- MaxUint64 cursor overflow falsely set has_gap=true.
- Concurrent capacity2 cache retained2000/4000 via other, instead of2/4.

Before the sniff-cache fix, this command exited 1:

```sh
go test -race -count=1 -timeout=5m ./app/dispatcher ./app/stats/command -run '^TestDomainTraffic'
```

`TestDomainTrafficSniffCacheReplayCountsOnce/EOF` and
`/context_deadline_exceeded` both lost the100-byte replay payload (EOF).
The ordinary domain Reader still counted it, exposing a real forwarding loss.

## Final Focused Results

From the Core worktree, after all fixes:

```sh
go test -json -race -count=3 -timeout=5m ./app/stats ./app/stats/command ./app/dispatcher -run '^(TestDomainTraffic|TestGetDomainTraffic)' > /tmp/xray-core-spec010-race.jsonl
```

Exit0. 21 distinct top-level tests, each repeated three times, no race reports.
Package PASS: stats1.555s, dispatcher1.860s, stats/command2.328s.
Only owned files were formatted; targeted `gofmt -l` returned no files and
`git diff --check -- app/stats app/dispatcher` passed.

- V-019: true RPC enabled-empty and disabled both preserve ordinary123.
- V-020: success/EOF/deadline with successful/failed writer all retain the
  no-eviction independent oracle450/660 across three identities. Sniff replay
  gives100/200 for both domain and complete user Stats, once only.
- V-021: seven attribution cases include last-hop and no-backtracking; existing
  normalization/user isolation plus authenticated/anonymous unknown remain covered.
- V-022: 104.999999999 open,105 closed; rollback99 stays logically105;110 closes
  seq2. Idle60s creates no fake sequence, start==cutoff stays, expired data is
  not resurrected by a second rollback.
- V-023: LRU refreshed A survives/B disappears, other0; equal-time victim is
  deliberately unspecified; concrete and unknown counters saturate at MaxUint64.
  Four writers x500 updates leave ordinary Stats2000 but cache2/4 and exactly
  two identities, under race detection.
- V-024: page size1, repeat reads, open latest3, consumed cursor2, new bootseq1,
  expired cursorgap and MaxUint64 future cursor. Actual Sidecar RPC consumers
  additionally pass; see the Sidecar evidence.
- V-025: early130/240, late30/40, both other0; previously returned snapshot is
  not mutated. The original two-Sidecar gRPC test also distinguished old Core,
  but its producer-specific failure was later removed from the consumer test:
  Sidecar must retain legitimate old other, not require its Core pin to change.
  Strict Core tests remain unchanged; a separate explicit new-Core response
  fixture now enforces the consumer's new-response oracle independently.

## Build Gate and Remaining Scope

The required repository wrapper was invoked from xray-config:

```sh
XRAY_CORE_SRC=/Volumes/Linux/opensource/cuber/xray-core-spec XRAY_BUILD_DIR=/tmp/xray-spec010-sidecar.3HxdNu/build bash xray-core/build.sh
```

Exit1: `FATAL: /Volumes/Linux/opensource/cuber/xray-core-spec is not a git repo`.
This is the wrapper's `.git` directory check rejecting a valid linked worktree
whose `.git` is a file, not a failure of the tested Go packages. Its later dirty
tree gate is also incompatible with this no-commit task. No wrapper bypass,
checkout, stash or production artifact replacement was attempted. Darwin/Linux
release binaries were not produced; parent handles final build/release workflow.

The tests exercise actual manager/RPC and actual WrapLink/cache, but do not
claim every protocol's full network path or production deployment acceptance.
Forward clock jumps still expire cache data; rollback may delay logical bucket
closure until wall time catches up. LRU ties are intentionally unspecified.
Full repository tests and one-spec-per-repository commits remain parent-owned.

Handoff: this task's Core/Sidecar source is frozen for parent merge. The parent
has reported independent Sidecar full race/vet and TUI test success against
the worktree via a temporary modfile; details and provenance are recorded in
the Sidecar evidence. No default pin was changed.
