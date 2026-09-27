# Current Outbound Acceptance Audit

Final disposition: the bounded gaps recorded below are closed by the follow-ups
and [final local acceptance](outbound-final-acceptance.md). The earlier incomplete
decisions are retained as chronological audit evidence, not the current task
status. No push or deployment was performed.

Date: 2026-09-28. Initial source snapshot:
`8d966b71ded6ca81b6310bc61ad8729ac28f3940`.
That initial inspection was read-only apart from this document. Subsequent
sections record later test additions and executions; they are not all evidence
for the initial snapshot. The latest reconciled source is `b6769bbb`.

Authority: [outbound.md](outbound.md), [outbound-tests.md](outbound-tests.md)
and the task mapping in [tasks.md](tasks.md). Results below are existing evidence
from [outbound-implementation.md](outbound-implementation.md) and the explicitly
identified follow-up executions below.
[outbound-coverage.md](outbound-coverage.md) contains an old matrix followed by
newer overrides. Its remaining-gap cells are not a current executable checklist.

## Decision

**O-T03, the UoT v2 adapter task, satisfies its original OT-06 contract at the
specified adapter boundary.** It is not OT-03, the sequential/concurrent pool
test. Do not keep O-T03 open because of unrelated pool/accounting/OB gaps.
Full outbound acceptance remains incomplete for the bounded items below.
An incomplete test oracle is not, by itself, evidence of a runtime defect.

## Current Evidence Boundary

The historical paragraphs below retain failures and their later resolutions.
For release-gate decisions, use these revision-qualified facts:

- Darwin whole-Core race passed at `62c3560d`: AnyTLS 457.153s, engine 8.723s,
  DNS 24.475s and scenarios 278.249s. Log:
  `../xray-config/.cache/anytls/full-race-62c3560d-darwin.log`.
- A new Darwin whole-Core run compiled `b6769bbb` tests before this follow-up
  was added and exited 0: AnyTLS 487.605s, engine 8.901s, DNS 24.319s and
  scenarios 277.503s. Command: `go test -race ./... -count=1 -timeout=12m`.
  Log: `../xray-config/.cache/anytls/full-race-b6769bbb-darwin.log`.
  This run does not include the new buffered-FIN test below; that addition has
  separate Darwin/Linux race evidence. Documentation was edited during the run,
  so this is not described as an entirely clean-worktree release execution.
- Linux/arm64 whole-Core race passed at `f9d6f9c4`: AnyTLS 429.326s and
  scenarios 264.501s. Log:
  `../xray-config/.cache/anytls/linux-address/full-race-f9d6f9c4.log`.
- `git diff --name-only 62c3560d b6769bbb` contains only Go test files and this
  audit document. These runtime-equivalent revisions are still different test
  suites; the older whole-suite pass does not certify the added tests together.
- The approved dual-platform build at `b6769bbb` produced Darwin/arm64 and
  Linux/amd64 artifacts with that revision and `vcs.modified=false`. The Darwin
  candidate passed `run -test` with the outbound validation configuration.
  This is build/config evidence, not Linux/amd64 runtime execution.

Remaining functional assertions are the bounded unresolved parts of items 1-3
below. Item 4's OB gaps and item 5's consumer checks have follow-up evidence;
they are not blanket absence claims. Final requirement mapping, shared-code
review and release integration remain open. No push or deployment is authorized
by this audit.

## O-T03 / OT-06 Evidence

| Original requirement | Current assertion and existing result |
|---|---|
| UoT v2, packet boundaries, reply source | `client.go` selects `uot.MagicAddress`, writes `uot.Request`, and installs `clientPackets`; `client_udp.go` preserves each buffer's destination and full reply destination. It reads the full wire datagram before validating the Core 8192-byte limit. |
| IPv4/IPv6/domain; upload and download 0/1/8192/8193 | `TestClientUDPPacketBoundaries` compares exact payload and complete destination in both directions and checks explicit invalid-size errors. The current whole-Core run reports the AnyTLS package PASS (425.013s); it is not opt-in. |
| Native oversized upload, no truncation, subsequent legal request | `TestAnyTLSOutboundUDPInvalidSizeAndNoDirectBypass` constructs one intact 8193-byte buffer, checks the actual size-validator error before caller cancellation, and joins dispatch. Legal 1/8192-byte packets succeed before and after. Nine paths: direct and four relays through both entrances. Final cleanup ordering passed 30 race rounds (12.287s). |
| Native zero/oversized download | `TestAnyTLSOutboundUDPResponseBoundaries` sends 1/8192/0/1/8193/8192-byte responses for IPv4/IPv6/domain destinations on all nine paths. Complete endpoint and payload are compared; zero/oversize require the adapter error, not a timeout. Peer/dispatch joins precede once-only receipt assertions. Recorded 30 race rounds PASS (69.902s); the saved log was read for this audit. |
| Multiple destinations in one association | `TestAnyTLSOutboundUDPUserDomainAccounting` exchanges alpha/beta packets repeatedly within each user's UoT connection across nine paths. `exchangeUoT` now compares the entire endpoint, including port. `TestAnyTLSOutboundAddressIPv6` additionally uses two real IPv6 UDP ports in one association. Linux strict address tests passed three rounds (2.072s); Darwin's unavailable source alias is not silently counted as covered. |
| No direct UDP fallback, including outer Hy2 | The invalid-size/no-bypass test leaves the final server and UDP destination live while removing the relay and recreating the top pool. Failed IDs must produce explicit errors and zero destination receipts; first fresh legal requests succeed after restoration. Separate-process acceptance also checks exact UDP and failed-ID receipts. |
| Independent interoperability | External servers and both independent control pairs verify legal TCP/UDP; external chained servers cover sixteen paths. These are complementary interoperability evidence, not malformed-packet boundary oracles. |

Zero-length **upload** needs a precise qualification: Core pipe/Copy can discard
an empty MultiBuffer before `clientPackets`. Thus adapter-level rejection is
verified; an end-to-end observable error for a zero-byte application write is
not promised. OT-06 explicitly calls this the adapter limit. Requiring a new
whole-pipeline empty-datagram API would change scope, not close a missing UoT test.
Oversize tests correctly avoid `core.Dial(...).Write(8193)`, which can split the
input into two legal buffers and would not test one illegal packet.

The historical Hy2 UDP failures remain in the implementation record. Some have
proven Darwin wildcard/IPv4 collisions or prior-fixture datagrams; one old request
lacks enough evidence for attribution. Explicit test socket ownership and IPv4
binding subsequently passed 1000 Hy2 subtests (75.698s) and the final nine-path
rounds. This qualifies the tested fixture, not native Hy2 global-manager cleanup
or a retrospective explanation of every failure. It does not leave an absent
UoT adapter requirement.

## Stale Gaps To Retire

| Old absence statement | Current replacement evidence |
|---|---|
| No independent Core processes; no non-AnyTLS relay processes | `TestAnyTLSOutboundSeparateProcesses`: direct plus SOCKS/VLESS/AnyTLS/Hy2 through both entrances, separate entry/relay/remote processes, relay and remote outages, fresh-ID recovery, exact final receipts and 43 joined children. Strict processes PASS 196.108s. |
| No external chained final servers or reverse external-client/native-outbound path | `TestAnyTLSExternalServersChained` sixteen-path matrix and `TestAnyTLSExternalClientsThroughNativeOutbound` for both independent clients; strict chains/external passed. |
| No reply-port validation, UDP no-bypass or native invalid response | Tests listed above supersede OT-06/C-06 and O-01 absence statements. |
| No native TLS/auth retirement barrier or stale-handler rejection | `TestAnyTLSOutboundRetirementDuringTLS` direct/two AnyTLS entrances: 100 race rounds, 314.985s. `...DuringAuthWrite` direct: 100 rounds, 6.832s, write/socket/dispatch join and rejected old-handler admission. This does not close native open/return races. |
| No rejected constructed-handler cleanup | `core/outbound_cleanup_test.go` has real constructor/manager duplicate and closed-manager ownership tests, 100 race rounds (2.197s). Invalid parameter tests need not instantiate or inventory a pool that was never constructed. |
| Old-only cleanup never checked against a live replacement | `TestAnyTLSOutboundChainedReplacementDrains` now retains replacement traffic across old-only drain and checks continued/fresh use; recorded ten race rounds. |
| Chain receipt snapshot relies on a 100ms sleep | Current chain-fault and process tests close producers and join backend workers before final receipt accounting. |
| Hy2 45s close failure remains unexplained | The later idle-timer differential explains the invalid fixed deadline: late TLS close_notify can advance QUIC idle detection. Default-timer derived close barrier passed twice per entrance (194.676s), with live contexts and first-new-ID recovery. Keep the original failure, but do not call this an unresolved AnyTLS bug. |
| Chained probes use TCP instead of UDS | `TestAnyTLSOutboundChainProbeTimeoutIsolation` now uses remote UDS on eight paths; three race rounds PASS 76.147s. Registered observer/balancer composition remains distinct. |
| No strict soak, process-tree cleanup or retained redacted configs | Strict ten-minute direct soak passed with 19,984 TCP/6,792 UDP/2,102 probes and 119 retirement/RST cycles; owned workload resources zero. Runner owns POSIX groups, fails exit-zero residual children and saves redacted fixture references. Dynamic gRPC mutations are not all persisted; source/tests remain part of the evidence. |

The processes report was inspected at
`../xray-config/.cache/anytls/strict-processes-reader-interrupt/report.json`:
status passed, exit 0, no errors, recorded revision `ab2d1fc0...`.
This is prior execution evidence, not an execution of the audit HEAD.

Current-head strict address evidence was also inspected at
`../xray-config/.cache/anytls/linux-address/strict-8d966b71/report.json`:
revision exactly matches this audit, worktree is clean, Go 1.26.1 Linux/arm64,
address exit 0, status passed, errors empty. The parent reports 1.438s with no
skips. This supersedes the older address execution qualification above; strict
address execution is not a remaining blocker.

## Minimum Remaining Acceptance Work

These are grouped closures of explicit requirements, not a demand to multiply
every phase by every protocol, platform and external implementation. Reuse the
existing fixtures; do not infer a new runtime feature from a missing assertion.

1. **Pool and accounting oracles (OT-02/03/09, C-03).** The 100 sequential /
   32x16 adapter workloads now have a synchronized start, recorded seed, actual
   destination sockets and exact joined physical/logical/destination counts:
   Darwin 30 race rounds 12.903s, Linux 30 rounds 12.591s. This direct-concurrency
   gap is closed.
   The two-AnyTLS workload now has real destination sockets and exact joined
   receipts (see follow-up). Reconcile relay-layer observations for the other
   declared chain types. The peer-idle-close-to-fresh-request oracle is now
   covered by the follow-up below, independently of timer-duration acceptance.
   External success/concurrency is not proof of external physical reuse.
   UDP reuse/exact-wire evidence is now closed by the follow-up below. No
   arbitrary deeper topology is required.
2. **Lifecycle, wire and bounded resource closure (OT-07/08/12 and resource
   gates).** Native TLS/auth-write, late dial publication, open/SYN and the
   FIN-return/Close overlap now have native manager evidence in the follow-ups.
   The return overlap uses real transport barriers, not an invasive hook at the
   lock-protected idle-list insertion. Repeated native fault/recreate warmed
   resource bounds are now asserted by the follow-up below; healthy adapter
   heap and direct-soak log samples remain complementary.
   SYN-before-PSH/FIN and parked-reader peer-FIN wire oracles are now covered by
   the follow-up below. The requested inverse read/write and delayed-EOF TCP
   shapes are covered by the TCP-shape follow-up below. Document actual
   half-close behavior instead of adding unsupported half-close semantics.
3. **Remaining chain path/fault assertions (C-02/05, OT-07).** Established relay
   and remote cuts/restarts with no replay are done. Add bounded dial/handshake
   interruption with later fresh-ID recovery at the relevant hop. Refused
   business destination, competing precedence markers and actual chained socket
   binding are now covered by the follow-ups below. Malformed-response no-replay
   already exists at adapter scope; avoid relabeling it absent. Dynamic origin/srcip
   rejection is an intentional tested contract, not missing implementation.
4. **OB composition (B-03/05/06, C-07).** The literal loopback trap, real gRPC
   health windows, admission-timeout business isolation and registered
   observer/balancer/UDS composition now have named evidence below. These bounded
   gaps are closed; separate existing Sidecar egress and direct scheduling
   evidence must remain mapped rather than being replaced by these Core fixtures.
   B-01/02/04/08 and direct B-07 should not be restarted as missing work.
5. **Actual consumer decoding (OT-10).** Core's sixteen statistics switches and
   exact logical counters are implemented/tested. Follow-up
   `TestAnyTLSOutboundLiveControlHTTPDisplay` in sibling Sidecar now adds/removes
   a real native outbound through gRPC and queries Sidecar's actual HTTP debug
   handler. Protocol, address, user, exactly one live row, removal visibility and
   credential exclusion passed ten race rounds (2.448s), using the candidate
   Core modfile. `TestAnyTLSOutboundLiveDomainAggregation` now drives real
   local inbound -> native outbound -> remote inbound -> TCP backend traffic;
   Sidecar collectors verify exact user/domain upload/download totals at both
   hops, distinct local/remote identities, duplicate bucket idempotence and
   domain HTTP output. Both live tests passed ten race rounds (11.697s).
   This closes the bounded consumer check, not the entire OT-10 matrix. Do
   not redesign sequence/snapshot persistence or require its full Cartesian
   product with every relay. Sidecar release-pin advancement is a separate
   release integration step, not an unfinished UoT implementation.
6. **Final execution/evidence gates (OT-13/14, O-T06/O-T09).** Linux/arm64
   whole-Core race at clean f9d6f9c4 passed, including the format gate with real
   Git metadata (AnyTLS 429.326s, scenarios 264.501s). Preserve the earlier failed
   logs. Darwin's earlier QUIC fixture failure is not retrospectively explained;
   the explicitly bound fixture passed 600 leaves, and the later Darwin
   whole-suite result is recorded above. Refresh final shared-change regression/format/vet evidence,
   and match clean dual-platform artifacts to the final local candidate. The
   latest reconciled build is `b6769bbb`. Bind selected
   strict reports to their actual revisions; do not present old green builds or
   skipped platform capabilities as current all-green acceptance.

Items 1-5 are missing acceptance assertions, not six demonstrated product bugs.
Item 6 retains stale platform/final-revision release gates. Completed independent-process,
external, idle, UoT and ten-minute tests need not be reimplemented. No new
mandatory all-protocol/all-platform cross-product, Linux-amd64 runtime execution,
hard pool capacity, or global Hy2-manager ownership contract is introduced here.

Parent follow-up: the Linux whole-Core snapshot run subsequently failed the
sequential-reuse and ordinary-OB physical-session count assertions. Treat those
as unresolved failures in item 1, not merely missing assertions. The independent
format-gate test also requires Git metadata absent from that archive fixture.
Exact results and opt-in DNS diagnostic failures are retained in
[outbound-implementation.md](outbound-implementation.md). Local candidate builds
do not close these release gates.

Follow-up: the sequential/OB fixture preconditions are now explicit next-read
upload-completion barriers. Their original strict counts pass on Linux and
Darwin (sequential 100 rounds, OB 30 rounds). Delivered-but-still-in-flight
cancellation has a separate real-socket recovery/no-replay test (Linux 100).
These focused failures are reconciled; a whole-suite green gate and the other
item-1 count/concurrency assertions remain outstanding.

## Local Follow-up: Admission, Selection And OB

The following test-only additions supersede the corresponding absence claims
above, without closing the complete outbound acceptance checklist:

- `TestAnyTLSOutboundRetirementAdmissionBarriers` uses real sockets and gRPC
  removal/replacement at late successful dial return and blocked SYN write.
  Old admission is rejected; old socket/worker/dispatch terminate while the
  replacement's existing business stream survives. An exact internal
  return-to-pool/Close overlap barrier is distinct from these assertions
  and is now covered by `TestAnyTLSOutboundRetirementFINReturnAndClose` below.
- `TestAnyTLSOutboundChainPrecedence` observes competing A/B relay markers:
  proxySettings wins, a missing selected handler does not fall back to the
  viable dialerProxy, and restoring the selected path succeeds. An armed direct
  endpoint receives zero connections. `TestAnyTLSOutboundChainedSocketBinding`
  observes source and kernel interface on the actual SOCKS relay socket through
  both chaining entrances, including wrong-family rejection and recovery.
- `TestAnyTLSOutboundHealthThroughControlAPI` queries the real gRPC observation
  API for windows 1/2/20, with both initially successful and initially failed
  histories. Empty history, interrupted recovery and stale/dead observations
  are asserted. Checks are manually driven after stopping the scheduler; this
  is not the concurrent registered-observer/balancer C-07 acceptance.
- `TestAnyTLSOutboundProbeLoopbackTrap`, enabled only by
  `ANYTLS_LOOPBACK_TRAP=1` in an isolated network namespace, arms literal local
  127.0.0.1:8080 and runs the remote UDS probe matrix against that same virtual
  URL. A direct control hits the trap once; proxied probes add no hits. Never
  enable this fixture on the workstation hosting the official Sidecar.

Darwin focused race, three rounds of admission/selection/binding/health tests:
PASS, 22.819s. The same selection plus the loopback trap passed three race
rounds on Linux/arm64 (33.257s), in a network-disabled Go 1.26.1 container with
`ANYTLS_STRICT=1` and `ANYTLS_LOOPBACK_TRAP=1`; no platform skip was accepted.
Whole-Core Darwin race at committed `62c3560d` also exited zero
(AnyTLS 457.153s, engine 8.723s, DNS 24.475s, scenarios 278.249s), before these
test-only additions. Log: xray-config
`.cache/anytls/full-race-62c3560d-darwin.log`. This closes the previously pending
Darwin whole-suite execution, not optional strict tests or all item-1..5 oracles.
Current `go vet ./...` and the repository gofmt gate pass. Test-only additions
must be included in the amended local commit and both artifacts rebuilt from
that clean HEAD; no push or deployment is authorized in this step.

## Scheduled OB Composition And Admission Isolation Follow-up

`TestAnyTLSOutboundScheduledBalancerUDSIsolation` composes a registered burst
observer, a configured leastPing balancer, two independently named groups and
two authenticated business users over both AnyTLS chaining entrances. The
actual inbound route selects the healthy handler; the remote UDS receives the
exact named URLs. Stopping the scheduler during a stalled HTTP response joins
the canceled request without publishing a late failed sample. Existing user
streams continue with exact per-user bytes; full instance shutdown joins native
dispatches while those streams are still open, followed by HTTP server shutdown.
This supplies the previously missing C-07 composition, not another independent
strategy unit test. Other relay transports retain their separate chain/UDS
timeout evidence; this is not a new all-protocol scheduler Cartesian product.

`TestAnyTLSOutboundProbeAdmissionPreservesSharedPoolBusiness` keeps a real
authenticated business stream active in the same native pool while the next
physical connection stalls at dial, TLS read or authentication write. The OB
timeout is one second. Check, blocked I/O, socket close and native Dispatch must
all finish within one shared two-second deadline, strictly before the fixture's
20-second native handshake or ten-second parent context. This distinguishes OB
cancellation from eventual unrelated timeout cleanup. Business transfers before,
during and after the stall, then the first fresh probe succeeds; exactly three
physical dials (business, failed probe, recovered probe) are required. Existing
HTTP-response timeout tests supply the fourth B-06 phase.

Both tests together passed ten race rounds on Darwin (55.584s) and Linux/arm64
(54.793s, network-disabled Go 1.26.1 container). The earlier version allowed
separate five-second joins and did not independently observe native Dispatch;
those weaker results are not the cancellation-causality evidence above.

Command from Core:

```sh
go test -race ./proxy/anytls -run '^TestAnyTLSOutbound(ProbeAdmissionPreservesSharedPoolBusiness|ScheduledBalancerUDSIsolation)$' -count=10 -timeout=2m
```

## UDP Reuse And Exact Wire Accounting

`TestAnyTLSOutboundUDPReuseExactWire` now combines four completed native UDP
associations, twelve exact packet echoes, Alice/Bob unequal payload lengths and
alpha/beta domains in one measured workload. A transparent verified-TLS bridge
counts one client-facing post-TLS byte layer; it does not implement authentication
or UoT, and forwards to the real Core inbound. Every association must reuse the
same one physical session. Real gRPC user/domain totals match only payload bytes,
while outbound counters exactly match measured framing/padding/authentication
bytes rather than adding the bridge's two legs. Retirement joins the physical
bridge's two copy workers and closes the one session.

The initial Linux ten-round combination failed once with the first physical
session already ended (accept/ready/ended=1/1/1). Receiving a UDP echo did not
prove the native upload write/watch had finished; closing the association could
still abort that in-flight write. The fixture now observes the next native
ReadMultiBuffer call after each packet, proving the preceding WriteMultiBuffer
returned before closing. It does not sleep, retry, relax counts or change runtime
behavior. Final focused race: Darwin 30 rounds 30.866s; Linux/arm64 30 rounds
30.288s, network-disabled container. This closes the item-1 UDP reuse/wire oracle,
not every other chain/accounting or lifecycle requirement.

```sh
go test -race ./proxy/anytls -run '^TestAnyTLSOutboundUDPReuseExactWire$' -count=30 -timeout=2m
```

## Wire Ordering And Refused Destination

`TestSessionWireSYNPrecedesPSHAndFIN` records actual authenticated-session bytes
over net.Pipe, including a non-vacuous completed stream, an unused stream and a
Close racing the request write. Both reference and cancelable-write paths require
SYN before any same-stream PSH/FIN. `TestSessionWirePeerFINWakesParkedReader`
observes consumption of a read-signal token before sending the peer's FIN frame;
Read, ReadBuffer and WaitReadBuffer must then return EOF rather than a deadline
or a local close, without terminating the session. Writer/recorder/read-loop and
reader workers are joined. This is session-wire evidence, not a substitute for
native manager retirement barriers or additional TCP half-close semantics.
Darwin: 100 race rounds, 500 subcases, 1.566s. Linux/arm64: the same 100 rounds,
1.121s in a network-disabled container.

`TestAnyTLSOutboundRefusedDestinationFreshRecovery` tests a closed TCP business
destination on the direct path and SOCKS/VLESS/AnyTLS/Hy2 through both entrances.
The first failed ID must fail; binding the same destination then permits the
first fresh ID, without retrying the business request. After native shutdown,
the joined backend must have exactly one fresh receipt and no trailing bytes.
Darwin: three race rounds, 27 paths, 57.580s; Linux/arm64: three rounds, 57.139s.
An earlier ten-round command hit
its aggregate two-minute Go test alarm, without a reported assertion failure;
one nine-path iteration takes about 18.6 seconds. It is not a PASS or evidence
of an unbounded individual request. Final three-round execution retains the
same four-second per-request bound and stronger backend-drain assertions.

```sh
go test -race ./proxy/anytls/internal/engine -run '^TestSessionWire' -count=100 -timeout=2m
go test -race ./proxy/anytls -run '^TestAnyTLSOutboundRefusedDestinationFreshRecovery$' -count=3 -timeout=3m
```

## Healthy Chain Counts Versus Retirement

The two-layer AnyTLS count fixture now uses a real TCP backend rather than
inline echo: 13 destination sockets, 17 unique framed requests received exactly
once, exact Alice/Bob payload bytes, per-layer wire equality and joined socket/
copy/dispatch workers. It retains the original bounded sequential/four-held-flow
workload through both entrances. Healthy sequential requests require one inner
physical session and one established outer tunnel; four concurrent held streams
require exactly four sessions at each layer. Shutdown closes all four. This
proves inner idle reuse and continuing established outer tunnels, not independent
idle reuse of an outer pool after forced inner retirement.

The old fixture mixed a different operation into that healthy sequence: remove
the inner handler/pool, close its TLS transport, then demand the outer physical
session count remain one. A diagnostic fifteen-round run observed one failure;
the closing outer connection was only 5.115s old and its stack passed through
watchWrite -> session.Close -> clientConnection.Close -> UConn.Close. uTLS took
the in-flight-Write close branch despite the lower socket wrapper reporting no
active Write. Thus cancellation overlapped a nested TLS write, not the 30s idle
timer. The exact TLS tail frame was not decoded and is not claimed to be
close_notify. No runtime change was made to preserve a partially written stream.

OT-03's one-dial oracle requires healthy completed uploads; C-03 records separate
layer counts, while C-04 requires safe replacement/drain rather than retention
of the old physical socket. The fixture therefore separates healthy C-03 counts
from existing `TestAnyTLSOutboundChainedReplacementDrains` C-04 coverage. It does
not replace exact counts with a range, retry failed requests or treat a close
that harms unrelated active users as acceptable. The original failure remains
recorded above. Darwin healthy-chain race: three rounds, 63.791s; Linux/arm64:
three rounds, 63.097s. Existing chained replacement drain plus native admission
retirement barriers also passed ten Darwin race rounds together (45.228s).

```sh
go test -race ./proxy/anytls -run '^TestAnyTLSOutboundChainLayerCounts$' -count=3 -timeout=2m
go test -race ./proxy/anytls -run '^(TestAnyTLSOutboundChainedReplacementDrains|TestAnyTLSOutboundRetirementAdmissionBarriers)$' -count=10 -timeout=2m
```

All changes in this follow-up are tests and evidence. Repository formatting,
`git diff --check` and whole-Core `go vet ./...` pass. The full-suite results
above retain their actual revision identities; these additions have focused
Darwin/Linux race evidence, not a newly claimed full-suite execution.

## Buffered FIN Follow-up

`TestSessionWireBufferedPSHSurvivesPeerFIN` closes the explicit unread-buffer
oracle from the TCP-shape checklist. A real session read loop consumes one
40,000-byte PSH followed by FIN before the application starts reading. Under
the reader lock the test proves EOF is published while the complete payload
remains pending; it then reads 79 chunks of at most 512 bytes, checks the
pending-to-cache transition, exact bytes, final EOF and empty buffers. The
physical session must remain open. Reader, session read loop and wire recorder
are joined. No sleep or TCP half-close behavior is introduced.

Focused race: Darwin 30 rounds 1.438s; Linux/arm64 30 rounds 1.044s in a
network-disabled container. Command:

```sh
go test -race ./proxy/anytls/internal/engine -run '^TestSessionWireBufferedPSHSurvivesPeerFIN$' -count=30 -timeout=2m
```

The separate delayed-response requirement concerns **local** upload EOF:
`Client.Process` changes to the DownlinkOnly timeout and continues receiving.
It does not propagate TCP CloseWrite to the remote business socket. A final
response test must gate on local reader EOF, not demand that the remote peer
observe a protocol half-close. The following TCP tests supply the missing
server-first and delayed-response barriers rather than relying on existing echo
or queued-output tests.

## TCP Shape Follow-up

`TestClientTCPServerFirst` performs an actual native Client.Process connection
and reads the complete server banner before issuing any business Write. It then
exchanges a request and waits for upload completion before cancellation. Both
copy workers and the fixture's server workers are joined.

`TestClientTCPDelayedResponseAfterLocalEOF` gates the server response on an
explicit local Reader EOF signal. Before releasing the response it checks that
Process has not returned; afterward it requires successful completion and exact
response bytes. Cases cover 7-byte upload reads / 64KiB response and 1MiB upload /
17-byte response consumed in at most 3-byte reads. Core's buffer size still
limits individual native upload chunks; the test does not claim a single 1MiB
wire write. The output is read after Process completion, retaining the existing
queued-output invariant. Process and server completion are bounded; the test
does not require remote EOF or add half-close semantics.

Darwin focused race: 30 rounds, 1.970s. Linux/arm64 focused race: 30 rounds,
1.308s in a network-disabled container. These are additions after the whole
`b6769bbb` suite, not retroactively part of that execution.

```sh
go test -race ./proxy/anytls -run '^TestClientTCP' -count=30 -timeout=2m
```

## Peer Idle-Close Recovery

`TestClientPeerIdleExpiryFreshRequest` exercises native Client.Process over real
TCP at the adapter boundary. Two completed requests must share one physical
session. After all logical/destination connections have closed, the fixture
closes only the server-side idle AnyTLS socket and waits for the client's
physical-close observation. It does not reset the pool or retry requests. The
first fresh ID must succeed, with exact totals of two physical sessions, three
logical streams, three real destination sockets and one receipt per ID. Shutdown
joins all owned workers and requires zero retained sockets.

Darwin 30 race rounds: 1.785s. Linux/arm64 30 rounds: 1.182s, network-disabled
container. This controlled idle-close stimulus does not test TLS or a peer's
timer duration; existing default 30s timer and external interoperability evidence
remain separate. It closes recovery after an observed expiry, not an unobservable
race where the peer closes simultaneously with a new request.

```sh
go test -race ./proxy/anytls -run '^TestClientPeerIdleExpiryFreshRequest$' -count=30 -timeout=2m
```

## Native FIN Return And Close Overlap

`TestAnyTLSOutboundRetirementFINReturnAndClose` adds the direct native-manager
overlap oracle without production hooks. A verified TLS peer observes the real
AnyTLS FIN stream ID after decryption; the physical Write has sent those bytes
but is held before returning. The test deletes the old handler through gRPC,
installs a same-tag replacement and exchanges replacement traffic while the old
FIN/control return is pending. It then releases that write and holds the old
raw socket Close after actual socket shutdown but before return. Replacement
traffic must still succeed, retirement/old Dispatch remain pending, and a stale
handler Dispatch must return with zero output and no additional physical dial.

Releasing Close must join old Dispatch, retirement and explicit handler Close;
replacement traffic then still succeeds. Finally both peer sessions and owned
workers terminate with exact accept/ready/ended counts 2/2/2. The entire held
overlap has one three-second budget, shorter than the five-second FIN watchdog;
watchdog expiration cannot satisfy the oracle. An independently started old
Close goroutine being pending alone is not proof it ran: the actual raw socket
Close barrier is the deterministic cleanup-overlap evidence.

Darwin race: one round 1.742s, then ten rounds 1.720s. Linux/arm64: ten rounds
1.368s in a network-disabled container. No runtime changes or new half-close/
idle-capacity contract were needed.

```sh
go test -race ./proxy/anytls -run '^TestAnyTLSOutboundRetirementFINReturnAndClose$' -count=10 -timeout=2m
```

Remaining bounded gaps: other relay-layer count observations and relevant chained
dial/handshake failure recovery. Final requirement mapping and release integration are still
open; these new tests do not substitute for them. Shared-hunk review and fresh
non-AnyTLS regressions are now recorded in outbound-impact.md (O-T09 complete).

## Repeated Native Fault Resource Bounds

`TestAnyTLSOutboundNativeFaultResourceRounds` reuses the native manager FIN/Close
overlap workload through `runNativeReturnClose`, not a second simplified client.
Each of 32 completed subtests creates its own Core instance, gRPC manager, TLS
peer, held FIN/Close fault, same-tag replacement and stale-admission rejection.
Existing exact ownership assertions and all fixture cleanups run before the
parent samples resources. Four rounds warm the runtime; later post-GC samples
must stay within baseline +8 MiB heap, +8 goroutines and +4 FDs. Unsupported FD
platforms report -1 without skipping the workload. Failure retains a heap
profile and goroutine stacks. Limits do not increase with the round number.

Darwin: one 32-round race execution, 2.962s; FD 4/4 and goroutines 3/3 throughout
measured rounds, final heap 2,177,872 versus 2,047,272 bytes. Linux/arm64: three
32-round executions, 19.867s in a network-disabled container; final FD 6/6 and
goroutines 3/3 in each, final heap excesses 143,624 / 74,768 / 55,032 bytes.
This supplies repeated native cancellation/retirement resource bounds alongside
the existing ten-minute mixed TCP/UDP/OB/RST soak; it is not a new claim that the
soak itself enforced these bounds or that every protocol was repeated here.

```sh
go test -race ./proxy/anytls -run '^TestAnyTLSOutboundNativeFaultResourceRounds$' -count=1 -timeout=3m -v
```

Logs: `../xray-config/.cache/anytls/native-resource-rounds-darwin.log` and
`../xray-config/.cache/anytls/native-resource-rounds-linux.log` (Linux count=3).

## External Physical Reuse And TCP Relay Counts

`TestAnyTLSExternalServers` now additionally runs ten sequential requests against
each independent sing-box/Mihomo server before the existing interoperability
workload. A successful system-dial counter requires exactly one verified-TLS
physical connection throughout; native Dispatch and real backend receipts require
ten logical streams, ten destinations and each request exactly once. Upload
completion is observed before cancellation. Retirement joins the physical close;
the parent workload then receives a fresh pool. Strict Darwin race passed once
(2.655s) and three rounds (3.256s), using the pinned external binaries. No retries
or relaxed count ranges are used.

`TestAnyTLSOutboundTCPRelayLayerCounts` covers SOCKS and VLESS through both
`proxySettings` and `dialerProxy`. Three sequential requests, four simultaneously
held streams with two messages each, and one final sequential request require
four physical relay TCP connections, four decoded relay requests, four native
AnyTLS TLS connections, eight logical/destination connections and twelve unique
once-only receipts. All owned workers join. Darwin race passed three rounds
(105.191s); Linux/arm64 passed one round (35.690s) in a network-disabled container.
Hy2 physical QUIC counts and admission cancellation are recorded separately;
TCP connection counts do not establish QUIC connection counts.

`TestAnyTLSOutboundHy2RelayLayerCounts` now supplies that separate QUIC oracle:
an independent protocol fixture counts actual QUIC accepts and unique connection/
stream IDs while the client uses unchanged native Core Hy2. Both entrances require
one authenticated QUIC connection, four business streams, four inner AnyTLS TLS
connections, eight logical/destination connections and twelve once-only receipts.
Business workers join before fixture QUIC shutdown; final accept/auth/close is
1/1/1. Darwin TCP+Hy2 combined race passed three rounds (154.049s). This is not
a claim that the independent server fixture tests all production Hy2 hub behavior.

## Chained Admission Cancellation Finding

The new `TestAnyTLSOutboundChainAdmissionFaultFreshRecovery` fixture found a real
pending-hop cancellation defect: `proxySettings/anytls` joins, while
`dialerProxy/anytls` leaves the hop Dispatch pending during both dial and TLS
admission cancellation. The detached redirect context remains live after the
returned pipe-backed connection closes. TLS cancellation can therefore close the
outer dispatch connection without canceling the pending relay handshake. This is
not a failed business request replay or an external-server availability issue.
The failing assertions are retained; subsequent repair and regression evidence
must supersede this finding before release acceptance.

Repair: `transport/internet/redirect` now creates a cancelable child of its
existing detached context. Only the returned connection owns cancellation;
Close cancels the pending relay operation as well as closing the pipes. Original
request cancellation and runner return do not cancel the connection. The wrapper
preserves the multi-buffer reader/writer interfaces, and NewDispatchConn itself
is unchanged. Independent TCP/UDP lifecycle regression failed before the fix and
passed 100 race rounds afterward (1.676s). Native chained dial/TLS cancellation
and first-fresh-ID recovery passed ten Darwin race rounds (43.604s); failed IDs
have zero receipts and fresh IDs exactly one, with dispatch/socket joins.

Linux/arm64 focused race combines the redirect lifecycle tests and Hy2 counts/
chained admission tests for three rounds: transport/internet 1.025s and AnyTLS
62.629s, exit 0 in a network-disabled container. Strict external and external
chains groups also pass after this fix, with no accepted skips and both binary
versions/hashes recorded in
`../xray-config/.cache/anytls/strict-final-local/report.json`.
The report accurately records the dirty candidate atop 23c1449c; do not describe
it as a clean-commit run. Final whole-suite/build status is kept in
[the acceptance index](outbound-final-acceptance.md).

## Original Review Limits

This is a bounded source/evidence reconciliation, not a fresh whole-repository
review. Packet adapter and its boundary/response/accounting assertions, process
and runner selection, relevant pool/resource/OB/Sidecar consumer code, and cited
reports were inspected. Other historical test internals and every archived JSON
event were not re-audited; their execution claims are attributed to the
implementation record, not independently recertified. Do not infer absence
merely because a test is not listed here. During finalization a separate change
to `app/dns/nameserver_fixture_test.go` appeared; it is outside this audit and
was neither reviewed nor modified. Its future results may supersede item 6.
