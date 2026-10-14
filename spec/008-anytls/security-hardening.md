# AnyTLS Security Hardening (2026-09-30)

Scope: corrections to the audit of `47ec227e`, plus two isolated reproductions
of inbound write timeout failures. This is part of spec 008, not a new protocol.
No production migration or deployment is included. Source and tests belong here;
the original audit is preserved in xray-config.

## Contracts

### Padding

Local configuration, engine service updates and remote UpdatePaddingScheme frames
use the same validator. Limits are 8192 text bytes, packet indexes 0..255,
stop 1..256, 16 ranges per packet, individual sizes 1..65535, 128 KiB sum of
maximum sizes per packet, and 1 MiB sum across the scheme. Markers count toward
the range limit but not the byte budget. The entire scheme is checked, including
records outside stop and unused handshake ranges. Unknown/duplicate keys, index
aliases, reversed ranges and malformed entries are rejected, not silently skipped.

This deliberately tightens the old upstream-compatible permissive parser:
negative stop, oversized or malformed schemes are not supported. Default
schemes and ordinary bounded custom schemes retain their wire behavior, including
the reference implementation's exclusive random upper bound.

An invalid remote update is ignored with a warning, preserving the old factory;
the connection is not replayed or silently rerouted. Factories own their raw
bytes and are atomically published per pool. Other pools are unaffected. Both
handshake and padded-record allocation check sizes before arithmetic/narrowing.
The writer also enforces the packet budget before emitting any record.

### Write Ownership

Control frames have a five-second timer covering lock wait and physical write.
They never set or clear a shared connection write deadline. Timer completion is
joined before returning; expiration closes the session to unblock lock/I/O waiters.
This applies to servers as well as clients, including no-op transport deadlines.

Server stream writes observe logical stream deadlines/closure during physical
I/O, just like the native client. Canceling a partial frame closes the entire
session, since reusing a partially emitted frame would corrupt other streams.
Healthy completed writes and normal FIN do not unconditionally close sessions.
Existing completion-vs-cancellation arbitration remains in place.

### Outbound Pool Bounds

New JSON/protobuf fields (same validation through native HandlerService):

| Field | Zero/default | Meaning |
|---|---|---|
| `maxSessions` | 256 | All registered sessions plus reserved creations |
| `maxIdleSessions` | min(64, maxSessions) | Retained idle sessions |
| `maxConcurrentDials` | min(64, maxSessions) | Creations, including authentication |

Limits must be positive after defaulting, at most 4096, and idle/dial bounds must
not exceed total sessions. `minIdleSession` must not exceed the idle bound.
Existing configurations without these fields receive finite defaults. Zero does
not mean unlimited. No pre-dialing, wait queue, new retry or business replay is
introduced. Capacity exhaustion returns an explicit error before dialing.

Reservations are made under the existing client mutex and released on all
failure/cancel/publication paths. Network I/O and Close remain outside it. Idle
overflow closes the returned idle connection; healthy active streams are not
evicted to make room. Pool Close cancels and joins in-flight creations, then
joins session readers. Handler retirement still drains already-admitted streams.

## Regression Evidence Plan

- `TestPaddingValidationBounds`: sizes, integer overflow, negative stop, duplicate
  and excessive entries, record and total budgets, legal default/boundary schemes.
- `TestPaddingFactoryOwnsScheme`: no mutable input alias.
- `TestRemotePaddingFramesRejectAndRecover`: actual frame reader, invalid update
  preserves factory, subsequent write is safe, valid update recovers, pool isolation.
- `TestPaddingAllocationDefense`, `TestPaddingHandshakeAllocationDefense`:
  independent allocation guards even with a corrupted internal factory.
- `FuzzRemotePaddingScheme`: generated size/budget invariants for accepted input.
- `TestControlWriteDoesNotChangeTransportDeadline`: no shared deadline mutation.
- `TestServerQueuedControlWriteTimeout`: control completion cannot erase the
  next writer's timeout; test transport intentionally ignores deadlines.
- `TestServerStreamWriteDeadlineAndClose`: cancellation after one byte of an
  actual frame has been consumed, worker join and session retirement.
- `TestPoolLimitsValidation`, `TestPoolConcurrentDialLimitAndClose`,
  `TestPoolDialFailureReleasesCapacity`, `TestPoolSessionAndIdleCaps`: bounded
  admission, immediate rejection, failure recovery, cancellation, idle reuse and
  retirement. Native config roundtrip and JSON mapping/rejection tests cover fields.

Run focused tests repeatedly with race detection, all engine tests, then the
whole Core race suite. Run independent sing-box/Mihomo client/server and chain
acceptance, including UDP, exact accounting, OB, user revocation and handler
replacement regressions. Run bounded padding fuzzing, gofmt, vet and the approved
Darwin/Linux build. Record actual outcomes below; historical passes do not count.

## Execution Results

Verified on Darwin/arm64 with Go 1.26.1, against the working-tree correction to
`47ec227e` on 2026-09-30 (subsequently folded into the AnyTLS commit):

| Check | Result |
|---|---|
| `go test -race ./... -count=1 -timeout=15m` | PASS, exit 0; AnyTLS 544.751s, engine 12.038s, existing scenarios 285.467s |
| New padding, pool and write tests with `-race -count=3` | PASS; engine 16.579s |
| `go test -race ./infra/conf -run '^TestAnyTLSOutboundPoolLimitsJSON$' -count=3` | PASS, 1.686s |
| `go vet ./...`, `.github/check-gofmt.sh`, `git diff --check` | PASS |
| `FuzzRemotePaddingScheme`, 30s, four workers | PASS, 4,716,513 executions, 36 new interesting inputs |
| Strict acceptance runner, `--group external --group chains` | PASS, both exit 0, no missing/skip gate errors |
| Sidecar `go test ./... -count=1 -timeout=5m` with sibling Core replacement | PASS; main package 26.676s |

Independent peers: sing-box 1.14.2 and Mihomo Meta v1.19.31. The strict runner
records binary hashes and generated fixtures. Logs/report are in the local,
ignored xray-config `.cache/anytls-audit/` directory (`fix-full-race.log`,
`fix-padding-fuzz.log`, `fix-external-chains/report.json`). Test source is tracked
beside the implementation; these ignored logs are not required to rerun tests.

The default whole-repository suite does not enable opt-in public-network tests.
Remote padding rejection is exercised through the real frame reader, not a live
malicious public TLS server. No new long-duration soak, Linux runtime or production
load test is claimed. Darwin/Linux release builds use xray-config's
`scripts/xrayctl.py build core` after committing; no deployment is included.
