# Outbound Implementation and Evidence

Date: 2026-09-28. Base: `develop` at `4d9abecd`; this is a local
development candidate, not a deployed version. No production config,
subscription renderer, listener or Sidecar release pin was changed.

## Implemented

- Native `ClientConfig`, JSON outbound registration, equivalent sender validation
  at JSON and HandlerService creation, single-endpoint settings and UoT v2.
- Handler-owned physical pool; request-owned logical flows and cancellation.
  Initial authentication/SYN completes before admission. Retire cancels pending
  admission, closes idle sessions and drains active flows. Close also joins copies
  and engine readers. Same-tag replacements have independent pool ownership.
- Pool selection and timeout defaults remain the pinned engine's policy, without
  an invented capacity/queue. Pool-locked reservation covers take/open, and a
  control-operation count covers queued FIN/heartbeat writes until completion.
- Opt-in native write cancellation handles dispatch connections whose deadline
  methods do nothing. Watchers are joined before write-lock release; failed or
  partially written records invalidate the session before any idle publication.
  FIN/control writes use the existing five-second limit with a close fallback.
- TCP and UDP use Core's dialer, policy and existing accounting. No new dispatcher,
  router, common/mux, singbridge or TLS accounting algorithm was introduced.
- Burst probe connections own a cancelable dispatch context. HTTP connection
  close now cancels pending protocol admission, while observer shutdown also
  reaches transport-detached dial contexts. This is a burst-local wrapper, not
  a change to tagged.Dialer or Core's general connection-close semantics.
- Optional manager retirement keeps removed handlers reachable until drained and
  includes them in instance shutdown. Close snapshots outside the manager lock.
  Add refuses a closed manager and publishes only after successful Start; rejected
  newly-created handlers are closed by `core.AddOutboundHandler`.

## Executed Tests

Commands below ran from Core unless another cwd is specified. Public-network
opt-in tests were not enabled. Passing the default suite is not a claim that
those optional network tests ran. Tests use loopback, temporary CA certificates,
isolated ports/UDS and cleanup; official local Xray was not replaced or restarted.

| Test | Observed evidence |
|---|---|
| Client config + infra/conf | Native invalid-account/address/port/idle tables, proto round-trip, JSON RAW/TCP/TLS and rejected transport/mux cases pass |
| TestClientSequentialReuseAndContextIsolation | 100 sequential streams, exact random payloads through 1 MiB, one physical dial and no inherited business context |
| TestClientConcurrentStreams | 32 workers x 16 exchanges, exact payloads |
| TestClientGracefulEOFPreservesQueuedResponse | Response buffered before Process returns remains readable; abort cleanup no longer discards it |
| TestClientCancellationInterruptsPhysicalWrites | Barriers block authentication, SYN and data writes with no-op transport deadlines; cancellation joins them |
| TestClientRetireDrainsAndCloseInterrupts / RetireCancelsPendingDial / CloseActiveStream | Graceful removal, rejected admissions, canceled pending dial and immediate close |
| TestClientFailedStatisticalDial | Error with a nil-wrapped CounterConnection returns an error, not a cleanup panic |
| Engine idle/reservation/control tests | minIdle 0/1/3, no predial, active exclusion, retirement override, take/open reservation, overlapping control ownership, five-second blocked FIN cleanup and disabled-reuse shutdown |
| TestAnyTLSOutboundRoutingStatsAndChains | Eleven paths: direct; proxySettings and dialerProxy over freedom, SOCKS, VLESS, AnyTLS and Hy2. TCP markers, exact two-user/two-domain stats, nonzero per-hop wire stats and three UDP sizes pass |
| TestAnyTLSOutboundFailureRecovery | Wrong password/SNI/CA fail; both proxySettings and dialerProxy reject missing intermediate tags, self-cycles and two-tag cycles. Each case then replaces the handler and verifies a successful exchange. Expanded matrix passed 20 repetitions under race (25.346s) |
| TestAnyTLSOutboundChainedReplacementDrains | Both chain entrances over AnyTLS: old flow survives top/relay removal, new wrong credentials cannot reuse old pool, missing relay fails, restoring relay recovers, retired top drains after flow closes (5.970s) |
| TestAnyTLSOutboundControlPlaneValidation | Real gRPC add/list/decode/reject/remove/re-add, no failed-tag publication |
| TestAnyTLSOutboundStatisticsSwitches | All 16 independent inbound/outbound/user/domain switch combinations pass; exact user/domain payload deltas (20.129s) |
| TestClientDefaultIdleTimer | Strict-mode real-time default 30s/30s idle eviction passes (31.553s including test/race overhead) |
| TestAnyTLSExternalServers | Core to independent sing-box and Mihomo: verified TLS, custom padding, random TCP sizes 1/8192/65535/65536/1 MiB, UDP 1/512/8192 and reply endpoint; 32 x 16 concurrent exact exchanges, stopped-server failure and fresh-request recovery without retry or handler replacement |
| TestAnyTLSExternalControlPairs | Independent sing-box -> Mihomo and reverse (no Core instance), verified TLS, exact TCP/UDP and endpoint, stopped-server no-bypass. Both external suites passed ten race repetitions together (37.637s) |
| TestAnyTLSOutboundObservatoryUDS | Real HealthPing/Dispatcher/AnyTLS/UDS: GET/HEAD, HTTP reuse on/off, query preservation, 204-only health, 503/200/302/timeout failure. Windows 1/2/3/20 verify immediate failure and min(3, window) consecutive-success recovery; expanded race run passed (5.826s) |
| TestAnyTLSOutboundObservedBalancers | Real registered burst observer, two AnyTLS outbound pools and HTTP target. Group isolation, default/longest-prefix URLs with escaping, actual leastPing/weightedLeastPing selection, alive high-RTT exclusion, historical-loss tolerance, all-dead equal-weight fallback, literal 1.5:1 rotation both healthy/all-dead, and cached stale history expiration |
| TestAnyTLSOutboundObserverStopRestart | Actual 3s/2s/20 scheduler, stop during remote HTTP response wait, no canceled-result publication, restart and health recovery; combined with balancer test passed ten race repetitions (21.638s) before adding the latter's stale assertion |
| TestAnyTLSOutboundConnectivitySuppression | Real remote HTTP and separate direct connectivity fixture: network-down produces no sample; healthy network plus failed remote produces dead sample; remote recovery becomes healthy. Exact target/connectivity request counts; twenty race repetitions passed (2.133s) |
| TestAnyTLSOutboundAddressIPv6 / AddressDNSLocality / AddressStaticBinding | Real IPv6 transport and multi-destination TCP/UDP, controlled endpoint DNS vs preserved payload names, independent TLS SNI/CA, static source and kernel interface binding. Darwin tests pass except unavailable 127.0.0.2 alias (explicit skip/default, strict failure); isolated Linux strict run passes all address cases three times (2.072s) |
| TestAnyTLSOutboundRejectsDynamicSourceJSON / ControlPlaneValidation | Both origin/srcip sources rejected with the fixed-sendThrough contract through JSON and real gRPC, no rejected tag published; ten race repetitions passed (conf 1.711s, AnyTLS 2.452s) |
| TestAnyTLSOutboundProbeTimeoutCancelsTLS | Real silent TLS socket reproduced a pending handshake after HTTP timeout. After burst-local cancellation fix, socket teardown completes within 500ms of the 200ms probe timeout. Timeout/balancer/stop-restart/UDS combined race run passed three repetitions (24.458s) |
| TestAnyTLSOutboundProbeTimeoutCancelsDial | A barrier in the real system-dial path waits on context rather than opening a socket. HTTP timeout cancels admission and joins it without waiting for the Core handshake limit; dial/TLS cancellation tests passed twenty race repetitions (10.868s) |
| TestAnyTLSOutboundProbeTimeoutCancelsAuthWrite | Verified TLS 1.2 handshake passes, first application record blocks on a channel, timeout closes the underlying connection and the blocked write returns. Twenty race repetitions including the explicit write-exit barrier passed (6.477s) |
| TestAnyTLSOutboundProbeTimeoutPreservesBusiness | Direct and both AnyTLS chain entrances: keep Alice's stream active across an HTTP-response timeout, verify continued exact echo and fresh stream admission, and exact Alice 11/12-byte uplink/downlink counters excluding probe traffic. Ten race repetitions passed (9.632s) |
| TestContractProbeConnectionOwnsDispatchContext | Closing one connection cancels only its dispatch; another connection and parent remain live. Observer stop cancels the detached remaining dial. 100 race repetitions passed (1.642s) |
| TestAnyTLSOutboundOBResourceCounts | All four endpoint/HTTP-keepalive combinations; actual native Core/TLS, separate HTTP/logical/TLS counters, Dispatch and peer-stream completion barriers. Six HTTP requests use 4 or 6 streams but one TLS session; HTTP close preserves native pool, full shutdown joins and closes it. 100 race repetitions passed (115.6s) |
| Sidecar TestAnyTLSOutboundRuntimeDisplay | Candidate proto reflection yields anytls, endpoint, remote email and no password in public metadata |
| Sidecar TestAnyTLSOutboundEgressUDS | Real HealthPing -> Core AnyTLS -> UDS -> actual Sidecar handler, local QoS fixture; default/named healthy/named mismatch/missing; GET/HEAD and ordinary/keepalive endpoints |

Critical pool/client/manager tests ran 100 repetitions under `-race` (latest
control-count run: client 31.341s, engine 1.507s, manager 2.404s, exit 0).
The five-second control timeout case runs separately, not 100 times.

After adding actual redirect Location/target counting and HTTP 404, the real UDS
OB matrix passed three race repetitions (12.612s). Redirect-target requests are
asserted zero. Shared Observatory/Router/Proxyman/Dispatcher/Stats/mux race suites
also passed after the burst cancellation fix (all package exits 0).

Linux address acceptance used the already-running local OrbStack engine and a
cached Go image, no host interface changes and no external network:

```sh
# cwd: ../xray-config; source and module cache are read-only
docker run --rm --name anytls-address-acceptance --network none \
  --mount type=bind,src=/Volumes/Linux/opensource/cuber/xray-core,dst=/src,readonly \
  --mount type=bind,src=/Users/cube/Dev/go/pkg/mod,dst=/go/pkg/mod,readonly \
  -w /src -e GOPROXY=off -e GOSUMDB=off -e GOTOOLCHAIN=local \
  -e ANYTLS_STRICT=1 golang:1.26.1 \
  go test -race ./proxy/anytls -run '^TestAnyTLSOutboundAddress' -count=3 -timeout=2m -v
```

Kernel observations included source/local 127.0.0.2 and interface `lo`. Container
exited 0 and was automatically removed. This is Linux arm64 runtime evidence,
not a claim that the Linux amd64 release artifact has run.

```sh
go test -race ./proxy/anytls/... ./app/proxyman/outbound \
  -run 'TestClient(Sequential|Concurrent|Graceful|Cancellation|Retire|Close|Failed|Reserved|Queued|Idle)|Test.*Retir|Test.*Shutdown' \
  -count=100 -timeout=5m

env ANYTLS_STRICT=1 \
  ANYTLS_SINGBOX=/Volumes/Linux/opensource/cuber/xray-config/.cache/anytls/tools/sing-box-1.14.2-darwin-arm64/sing-box \
  ANYTLS_MIHOMO=/Volumes/Linux/opensource/cuber/xray-config/.cache/anytls/tools/mihomo \
  go test -race ./... -count=1 -timeout=10m
```

The first full Core run exited 0 (scenarios 281.671s). It preceded the final
reservation/control-write hardening; a final full rerun is recorded below when
complete. Targeted strict interoperability + config/manager/inbound rerun exited
0 (AnyTLS 64.560s). `go vet ./...`, gofmt and `git diff --check` passed before the
last small control-operation change and must be rechecked for release.

External binaries are sing-box 1.14.2 (revision
`af6e64c3b69e6132ebaee0e1a3d24e93903f6709`) and Mihomo 1.19.31. These executable
versions are not the same as the source-review checkout pins in outbound.md.

Sidecar uses an ignored **test-only** alternate modfile with a local replacement:

```sh
# cwd: ../xray-sidecar; do not alter the production go.mod pin
env ANYTLS_STRICT=1 go test \
  -modfile=../xray-config/.cache/core-spec-acceptance/anytls-outbound.go.mod \
  -race ./... -count=1 -timeout=5m
```

AnyTLS/domain-specific run exited 0 (20.427s). First full Sidecar run failed its
existing `TestEgressGuardHistoryRetainsRecent25` at a local HTTP deadline while
other stress tests were running. A full isolated rerun passed (23.715s); this
does not erase the first result or establish timing robustness. New real egress
UDS test subsequently passed separately (5.915s). Current release pin predates
ClientConfig: the new consumer tests explicitly skip without it unless strict
mode is requested, in which case they fail. Advancing the pin is a release step.

## Shared-Code Review

The manager capability is interface-based, not a protocol string special case.
Legacy handlers still do not receive Retire on removal. Manager Start/Add are
now terminal after Close, and failed Start no longer publishes a default/tag;
dedicated fake-handler tests cover these changes. Existing reverse, routing,
VLESS, Hy2, SS2022, mux and transport tests passed in the full Core run.

Review found and fixed: failed-statistical-dial panic; graceful response
truncation; blocked authentication/SYN/data cancellation; add-after-shutdown;
take/open idle re-publication; queued-control re-publication; recursive Close
under disableReuse plus cancelable writes. Each has a focused regression.

No benchmark claim is made: native cancelable writes use a short-lived watcher
per serialized data/request write. This ensures correctness on non-deadline
dispatch transports but allocation/throughput overhead still needs measurement.

## Remaining Acceptance Gates

Detailed source-level coverage and remaining oracles are tracked in
[outbound-coverage.md](outbound-coverage.md). The audit distinguishes partial
integration coverage from a passing focused test; do not infer completion from
this results list alone.

This is not full OT/B/C completion. Keep release acceptance open for:

- The initial ten-minute soak failed on Darwin wildcard UDP sockets; the isolated
  fixture rerun now passes. Preserve both results below rather than deleting the
  failed run or generalizing loopback success to production networking.
- Remaining chain cases: multi-hop interruption and no-replay receipt accounting.
  Mixed-entrance two/three-tag cycles now cover both two-tag orders and all six
  mixed three-tag orders: each fails within five seconds and recovers after
  replacing the top handler. All 17 failure cases pass under race; the eight new
  mixed cases also pass ten race repetitions. Distinct relay/server DNS names and preserved
  payload domain now pass ten strict race repetitions through both SOCKS entrances.
- Remaining OB composition: broader fault-phase isolation across all chain
  variants and final requirement mapping. Group URL
  overrides, independent samples, actual strategy selection and observer
  stop/restart now have end-to-end tests; this does not close every B/C row.
- Resource ownership counters/heap measurements and final evidence review against
  every matrix row.
- Sidecar pin advancement. Initial
  `xrayctl build core` correctly refused the dirty tree. On 2026-09-28 the user
  authorized one local Core implementation/spec commit followed by the normal
  build wrapper, without push or deployment. Sidecar release pin remains unchanged.

### Failed ten-minute soak (2026-09-28)

`ANYTLS_SOAK_STRICT=1 ANYTLS_SOAK_DURATION=10m go test -race ./proxy/anytls
-run '^TestAnyTLSOutboundSoak$' -count=1 -v -timeout=12m` exited 1 after
600.751s. This run includes the burst cancellation fix. It recorded 19,839 TCP,
6,715 UDP and 2,092 OB successes, 119 handler retirement/recreation cycles and
119 deliberate backend resets. Three UDP exchanges failed at the three-second
deadline with 1339/8192, 0/8192 and 0/8192 bytes. These failures were neither
retried nor ignored. Follow-up differential testing reproduced missing replies
with CNC, PacketConn and UoT, including freedom direct without AnyTLS. Echo-side
IDs and hashes showed intact 8192-byte datagrams received and sent, while the
Core socket missed the reply. A pure-Go Darwin control reproduced dual-stack
`[::]:59957` sharing a port with existing IPv4 `0.0.0.0:59957`; the old IPv4
socket received the echo. This establishes a host wildcard-socket collision,
not an AnyTLS-only defect. The original 1339-byte packet's contents were not
captured, so its specific origin is not proven.

The loopback fixture now explicitly binds freedom's source to `127.0.0.1` and
reads UDP as one datagram instead of `io.ReadFull` across packets. No production
UDP behavior changed. Ten differential groups / 80,000 packets passed under race
(14.433s). The strict ten-minute rerun subsequently passed independently:
10m1.080s workload/teardown, 602.878s total race-test time, exit 0. Counts were
19,971 TCP, 6,802 UDP, 2,101 OB, 2,102 HTTP requests, 120 retirement/recreation
cycles and 120 deliberate resets. Zero failures and zero retries. Owned workers,
echo connections/readers and active HTTP handlers all reached zero. Supplemental
post-close samples were 10 goroutines, 8 FDs and 2,886,232 retained heap bytes;
these samples alone are not a leak proof.

Teardown did pass: owned workload workers, echo connections/readers and active
HTTP handlers all reached zero; Core and fixture joins completed within bounds.
Supplemental post-teardown samples were nine goroutines and seven FDs. These
counts do not erase the functional failures or independently establish no leaks.

### Post-build regressions

The post-burst-fix full `go test -race ./... -count=1 -timeout=10m` exited 0:
AnyTLS 99.571s, engine 8.760s, scenarios 282.176s. Both external executable
variables were set. ANYTLS_STRICT was not set for this whole-Darwin run: the
unavailable source alias, opt-in default-idle timer, soak, and optional public
network gates are not claimed as run here. Separate strict Linux/address,
real-time idle, external and soak results are recorded individually. Later
test-only additions (auth-write exit barrier, dynamic-source rejection and
distinct relay DNS) passed separately; no later runtime delta preceded this entry.
The strengthened unequal Alice/Bob and two-domain counter matrix passed all
eleven direct/chained paths (14.999s).

The subsequent Sidecar full race run against the unchanged candidate runtime,
using the test-only local replacement above, passed (28.534s). Production pin
remains unchanged. Format check, full Core vet and diff whitespace checks passed
again after adding the soak differential and mixed-chain-cycle fixtures.
The final whole-Core race rerun with both external executables exited 0
(AnyTLS 99.843s, engine 8.159s, scenarios 284.798s). Linux strict address tests
including the distinct relay-DNS extension passed three times (2.270s).
The real-time default idle timer also passed again in its engine package
(31.424s); an initial selector against the parent package matched no tests and
is not counted as evidence.

Post-build acceptance additions include a burst cancellation fix. The full rerun
above includes that fix; amendment into the same local feature commit and a
dual-platform rebuild are required. Before this runtime delta, strict
AnyTLS/engine suites passed (96.643s/36.524s). Those earlier results are historical
evidence, not a substitute for the post-fix full run.
The first observer-stop test incorrectly required remote HTTP teardown in one
second: nine of ten runs hit that bound. Source review confirmed the inbound's
configured one-second downlink-only drain after FIN. The corrected test separately
requires local observer Stop under one second and remote drain within three
seconds (policy plus scheduling allowance), checks no canceled sample, then
restarts. No runtime timeout or transport behavior was changed to pass the test.

- Before the burst cancellation fix: strict `go test -race ./... -count=1 -timeout=10m`
  exited 0; AnyTLS 65.261s, engine 7.813s, scenarios 282.879s.
- The subsequently added statistics-switch and real-time idle tests passed
  separately (20.129s and 31.553s), before the later burst cancellation fix.
- `go vet ./...`, `.github/check-gofmt.sh`, and `git diff --check` exited 0.
- Regenerated config.pb.go with the existing protoc 33.5/protoc-gen-go 1.36.11
  toolchain, eliminating compiler-header churn; configuration tests passed again.
- Sidecar candidate-Core vet passed. Its full race rerun passed before adding
  the real UDS test; the latter passed separately. Old release-pin targeted run
  passed with candidate-only cases skipped as documented.
- No push or deployment. Remaining acceptance gates above remain open despite
  green default full-suite results. Local candidate commit/build is authorized;
  its result must be verified independently of these test results.

### Additional Acceptance (After af1e18ee Build)

These additions are test/harness-only; runtime code remains the af1e18ee candidate.

| Test | Actual result and scope |
|---|---|
| TestAnyTLSOutboundUDPUserDomainAccounting | Nine direct/chained topologies, concurrent Alice/Bob, two domains, unequal 1/8192 and 37/512-byte packets, three repeats each; exact user/domain bytes and fixed remote-account aggregate. Ten race repetitions pass, 91.252s. |
| TestClientLifecycleAtWriteBarriers / LateDialCannotPublishAfterRetirement | Authentication/SYN/data barriers under Close/Retire; admitted data survives retirement until Close, pending admission is canceled; late successful dial is closed without authentication/publication. 100 race repetitions pass, 1.817s. |
| TestClientCompletedResourceRounds | 24 rounds x 8 workers x 8 exact exchanges, owned physical sockets and pending/active operations zero after Close, peer fixture joins, two-GC retained heap bounded relative to warmed baseline. Three race repetitions pass, 2.812s. This is adapter-level healthy workload evidence, not every native TLS fault case. |
| TestAnyTLSOutboundShutdownOwnsDrainingNativePool | Real gRPC duplicate rejection, active-old remove/recreate, cumulative tag counters retained/increased, exact user bytes, instance Close drains removed old pool and terminates old stream (not just timeout). Direct and both AnyTLS chain entrances, 100 race repetitions pass, 13.683s. |
| TestClientMalformedResponseDoesNotReplay | After peer receives business bytes, truncate header/body; one physical attempt and receipt, bounded failure/peer join, next independent request succeeds on fresh connection. 100 race repetitions pass, 1.854s. This isolates native adapter/engine, not a TLS/multihop malformed-peer matrix. |
| TestAnyTLSOutboundChainProbeTimeoutIsolation | SOCKS/VLESS/Hy2 x both entrances; simultaneous healthy probe, stalled probe, Alice/Bob streams; exact independent counters, recovery, observer cancellation and native shutdown. Three race repetitions pass, 57.3s. |
| TestAnyTLSOutboundChainLayerCounts | Both AnyTLS-over-AnyTLS entrances, per-layer physical/logical counters, sequential reuse, concurrent traffic and shutdown cleanup. Extended plaintext protocol-byte oracle exactly matches client/hop gRPC counters at each TLS boundary; authenticated Alice cancellation then Bob reuse retain exact 12/12 and 22/22 user counts. Three race repetitions pass, 63.4s. |
| TestAnyTLSOutboundCanceledUserThenReusedPool | Full native remote inbound for direct and both AnyTLS chains: actual system-dial counter stays one across Alice disconnect and Bob request; independent exact user counters. Ten race repetitions pass, 33.622s. |
| TestAnyTLSOutboundNativeValidationMatrix / JSONIdleIntervalBoundaries | 33 invalid native gRPC configurations leave registry unchanged and original business flow operational; 22 JSON timer-boundary cases. Ten race repetitions pass, 4.153s. |

The strict runner at `testing/anytls_outbound_acceptance.py` completed `external`
and `idle` groups with no skips, required tests present and exit 0. Its four
Python oracle tests pass. Evidence is retained under xray-config's ignored
`.cache/anytls/strict-outbound-af1e18ee/` (report, JSON events, stderr); executable
SHA-256 values are `d652879eed7e38b866fa980bc2e119dcdb13968fe5e6583670293a258686d0e7`
for sing-box and `fae1f37e28ee53fcf5be7a8bb121099db1fe442e44205734ed49c62579364090`
for Mihomo. This does not claim address/soak groups were rerun by this runner.

The combined new-test race run passed (53.121s), covering lifecycle barriers,
late dial, resource rounds, malformed replies, UDP accounting, shutdown, layer
counts, chained probes, canceled-user reuse and the native/JSON validation
matrices. The Python runner's four tests, package vet and repository gofmt gate
also passed. These are additional focused results, not a new full-suite run.

`TestAnyTLSOutboundChainFaultReceipts` passed all eight default cases under race
(75.230s including the explicitly skipped opt-in diagnostics). TCP relays must
recover on the first independent request. Hy2 cases first require a read failure
with the request context still live, then restart the relay and require the
first independent request to succeed. Business IDs are never retried to turn a
failure into success; backend receipts detect duplication and hop bypass.

Initial Hy2 recovery before the old QUIC session expired failed (`after=0`,
closed pipe). Native Hy2 without AnyTLS and a no-forwarder control reproduced
this: cached connection health follows its QUIC context; default idle timeout
is 30 seconds with no keepalive. Closing the server listener/UDP socket does not
mean the client has observed connection closure. Latest cases observed EOF at
30.00s/30.03s with live request contexts, then recovered on the first new request.

One earlier combined run reached the 45-second local deadline without observing
the protocol-close barrier. Its cause remains unresolved, despite subsequent
independent and three-round passes. The test keeps this deadline a hard failure;
it is not evidence of a fixed defect. Slow early-recovery diagnostics require
`ANYTLS_CHAIN_FAULT_DIAGNOSTICS=1`; the default suite does not run their 31-second
sleep. No Hy2 runtime behavior was changed to satisfy this test.

### Follow-up Shared Readv And External Chain Acceptance

After the cd5ed598 build, ChainedReplacementDrains was strengthened: a replacement
stream stays active across the removed handler's drain completion, then both it
and a fresh replacement request must work. Ten race rounds passed (44.727s).
The UoT accounting helper now checks the entire reply endpoint, including port;
inbound accounting plus nine native outbound paths passed three rounds (31.592s).

ExternalServersChained extends the existing independent-server workload across
SOCKS/VLESS/AnyTLS/Hy2 and both proxySettings/dialerProxy entrances (16 paths).
The initial run failed restart recovery. An observed-hop completion barrier
showed residual dispatches five seconds after server exit. Stacks retained
ReadVReader; disabling readv passed the focused SOCKS/sing-box case three times.
An independent real TCP reset regression demonstrated lost ECONNRESET before
the shared readv fix. See outbound-impact.md for its scope and error contract.

With readv enabled and the fix applied, all 16 paths passed under race (57.105s):
verified TLS/padding, exact TCP up to 1 MiB, UDP payload/endpoints, 32x16 concurrent
requests, observed hop teardown, stopped-server failure and first-request
recovery on the same handler. The barrier observes dispatch completion rather
than retrying business requests; this is not zero-latency recovery immediately
after process exit. Full readv tests passed 100 race rounds (6.971s); new full
Core regression is pending. These source changes postdate the built candidate.

The strict runner's `chains` group also passed with all 16 required subtest
events and no skips. JSONL/stderr, binary versions/hashes and dirty source-state
metadata are retained in xray-config `.cache/anytls/strict-chains-readv/`.
Its five Python oracle tests pass, including rejection of any missing chain.

A simultaneous full Sidecar candidate run failed three local QoS HTTP deadlines
(`AllowsAnyExpectedASN`, `FirstTimeoutFailsImmediately`,
`FailureResetsRecoveryProgress`, total 28.087s). This is not erased by focused
passes; an isolated rerun is required. Production Sidecar pin remains untouched.

The isolated full Sidecar rerun passed (28.437s; route-match 1.995s). The first
failure remains evidence of timing sensitivity under concurrent stress.
The full Core race run after the readv fix **failed** only
`TestAnyTLSOutboundChainFaultReceipts/dialerProxy/hysteria-close-barrier`:
the 45s context deadline expired, and both new recovery IDs returned EOF.
AnyTLS package duration was 299.079s. Other reported packages passed, including
the engine, buffers, transport and existing protocol/scenario suites. Logs are
retained at xray-config `.cache/anytls/full-race-readv.log` and
`sidecar-race-readv{,-isolated}.log`. This reproduces the prior Hy2 residual;
readv is independently fixed, but full Core acceptance is explicitly not green.

### Reverse External Clients and Hy2 Failure Capture

`TestAnyTLSExternalClientsThroughNativeOutbound` adds independent sing-box and
Mihomo clients through the Core authenticated inbound, native AnyTLS outbound,
and remote Core inbound. Three strict race rounds passed (11.670s, six client
subtests, no skips). Each verifies rejection of an incorrect TLS trust root,
TCP payloads through 1 MiB, UDP payload and full reply endpoint at two targets,
and exact ingress-user and remote-user counters of 1,205,259 bytes each way.
Removing the native outbound prevents further traffic despite reachable echo
servers. This is an independent-client test, not separate Core processes.

The strict runner now requires these two reverse-client subtests as well as
the original six external paths. The expanded external group passed without
skips; its report, JSONL, binary hashes and versions are retained in xray-config
`.cache/anytls/strict-external-reverse-131fa880/`. Six Python validator tests
pass, including rejecting either missing reverse-client result.

The full chain-fault diagnostic repeated three times under race: two rounds
failed and one passed (278.331s total). Both Hy2 entrances exhibited the 45s
failure across the run. With `ANYTLS_TEST_DEBUG=1`, failure stacks show a live
QUIC connection run/send loop and a Hy2 stream read waiting inside quic-go.
That narrows the observed state but does not establish the underlying cause;
do not classify this as fixed or increase the test deadline to make it pass.
Captured log: xray-config `.cache/anytls/hy2-close-stacks.log`.

QUIC debug follow-up reproduced the failure in one of two complete matrix
rounds (166.563s); two isolated dialerProxy rounds passed. In the failing
connection `5b1226a349e78c344c808bc8d68e`, final received packets were at
02:27:25, then stream 4 sent another 24-byte data frame at 02:27:55 before
STOP_SENDING/FIN. The held flow was on stream 8. The server expired at 02:27:55,
while the client remained live at the request's 45s bound. quic-go's
`idleTimeoutStartTime` takes the later of last receive and first subsequent
ack-eliciting send. Therefore a negotiated 30s idle timeout is not a fixed
30s-after-cut bound. Idle AnyTLS TLS-session cleanup is a plausible source of
the late 24 bytes, not yet independently proven. Verify that source and the
appropriate close oracle before changing timeout assertions or runtime logic.
Log: xray-config `.cache/anytls/hy2-quic-debug-matrix.log`.

### Hy2 Idle Timer Differential

A diagnostic-only client pool interval/timeout of 10s moved the late stream-4
24-byte send from 30s to 10s. Two race rounds returned protocol EOF with live
request contexts at 30.004s and 39.996s, respectively, and recovered with the
first two new IDs. Log: `.cache/anytls/hy2-idle10-debug.log` in xray-config.
The source path is idle cleanup -> session.Close -> TLS.Close -> close_notify;
Go TLS can send that alert even while another QUIC stream is active. The QUIC
idle start then advances to the first ack-eliciting send after the last receive.
This explains why a fixed 45s-after-cut assertion was not a valid closure oracle
for a default 30s AnyTLS idle pool layered over a 30s QUIC idle connection.

The close-barrier test now derives a 100s bound from pool idle timeout (30s),
pool scan interval (30s), QUIC idle timeout (30s), and 10s scheduling allowance.
Fixture policy connIdle is 180s, above that bound, so policy expiration cannot
satisfy the protocol-close assertion. The request context must still be live
when EOF arrives; both recovery IDs must succeed without retries. Runtime
defaults are unchanged. `ANYTLS_CHAIN_IDLE_SECONDS=2..30` is diagnostic only.
Default-timer repeated validation passed both entrances twice under race
(194.676s): observed close times approximately 60/30/60/30s, live request
contexts and both fresh recovery IDs successful in every case. This resolves
the fixed-45s oracle failure, not proof of a new Hy2 runtime fix. Log:
xray-config `.cache/anytls/hy2-derived-bound.log`.

Final receipt assertions also close the Core fixture and join all backend
workers instead of sampling receipts after a fixed 100ms sleep. This strengthens
the completed-worker accounting oracle, not a claim that every shared transport
goroutine in Core is joined by Instance.Close.
The strengthened receipt barrier passed all six TCP-relay paths three race
rounds (20.943s) and a default Hy2 dialerProxy round (34.686s). `go vet
./proxy/anytls/...` passed. Whole-Core `go test -race ./... -count=1 -timeout=10m`
at `330cd213` passed (exit 0): AnyTLS 279.214s, engine 8.626s, existing scenarios
277.103s. Log: xray-config `.cache/anytls/full-race-330cd213.log`. This validates
the shared runtime changes and corrected default close gate; opt-in suites are
separate evidence. New cleanup/process/UDP-boundary tests written afterward are
not included in that run, and remaining behavioral coverage gaps stay open.

### Independent Processes and UDP Boundaries

`TestAnyTLSOutboundSeparateProcesses` runs distinct Core OS processes for direct
and both AnyTLS-relay entrances. Verified TLS, exact TCP/UDP body and endpoint
markers, unique business receipts, stopped-server failure and first-new-request
recovery after restart are checked. The initial run failed all three paths on
entry shutdown: TimeoutWrapperReader hid cancellation from UDP copy workers.
After the independently reproduced wrapper fix, three race rounds passed
(109.773s); all 33 child processes exited and were joined without watchdog or
forced kill. The five-second process-close limit was not relaxed. This covers
AnyTLS relays, not separate-process SOCKS/VLESS/Hy2 or the full OB/stats topology.

`TestAnyTLSOutboundUDPInvalidSizeAndNoDirectBypass` sends an intact 8193-byte
datagram through native dispatch, rather than accidentally splitting a byte
stream into legal packets. It requires explicit size rejection, no endpoint
receipt, exact 1/8192-byte legal packets before and after rejection, no direct
bypass with a broken chain, and first-request recovery after handler restoration.
Direct plus SOCKS/VLESS/AnyTLS/Hy2 through both entrances passed 30 race rounds
(12.214s). A prior ten-round run had one Hy2 first-packet EOF after roughly 2s;
the cause remains unproven. Added feedback/context diagnostics and 60 focused
Hy2 subtests also passed, but do not erase that unexplained failure. No business
retry or longer timeout was added. Zero-length/download-oversize cases remain
outside this integration fixture.

The independent constructor cleanup test passed 100 race rounds (2.197s).
Timeout reader interruption passed 100 race rounds (1.895s), covering normal
and already-timed-out reads with Interrupt-only and Close-only sources. The
pre-fix four-case run failed all four as expected. `go vet ./common/buf ./core
./proxy/anytls/...`, repository gofmt gate and `git diff --check` passed.

Whole-Core race regression with the wrapper fix and all three new integration
test files passed: `go test -race ./... -count=1 -timeout=10m`, exit 0; AnyTLS
256.010s, engine 8.664s. Log: xray-config
`.cache/anytls/full-race-timeout-wrapper.log`. Opt-in external/address/soak
acceptance remains separate; this pass does not resolve the earlier isolated
Hy2 EOF or the remaining coverage-audit gaps.

### Strict Runner and Chained UDS Follow-up

At the clean 52b618bd candidate, strict `external`, `chains` and `idle` groups
passed with all required events and no skips. This includes eight external
directions/compositions and sixteen relay/entrance/final-server combinations.
Evidence: xray-config `.cache/anytls/strict-interop-52b618bd/`. Full Sidecar
candidate race via the separate modfile also passed (28.957s); its release pin
was not advanced.

The acceptance runner now owns a POSIX process group for each command and has
a separate wall-clock bound including compilation. Interrupted, timed-out and
failed runs clean TERM-resistant descendants and preserve a failed report.
Even valid PASS events plus exit zero fail if the command leaves descendants.
Thirteen Python tests passed (10.308s), including SIGINT/SIGTERM, invalid JSON,
normal completion, inherited listeners and TERM-resistant child/grandchild
cleanup. A child deliberately escaping with setsid is outside this contract;
non-POSIX execution is explicitly refused rather than claiming tree cleanup.
The updated runner itself subsequently passed external/chains/idle with actual
Go and independent binaries, preserving redacted fixtures and clean process-group
completion. Report: xray-config `.cache/anytls/strict-owned-runner/report.json`.

Supported fixture constructors persist redacted JSON structures when an
absolute ANYTLS_EVIDENCE_DIR is supplied. The separate live configs are never
mutated. Passwords, IDs, SOCKS/Hy2 credentials and inline/private key paths are
removed; restart snapshots get unique mode-0600 files. Unit and native chain
smoke tests passed (14.770s). Runtime gRPC mutations are not all captured, so
these artifacts supplement source/event evidence rather than replace it.

ChainProbeTimeoutIsolation now sends HTTP through a real remote UDS across
SOCKS/VLESS/Hy2/AnyTLS and both chain entrances. Eight paths passed three race
rounds (76.147s): exact ob queries, two authenticated users, overlapping healthy
and stalled checks, immediate unhealthy result, recovery, unaffected business
after observer cancellation, exact user deltas and joined Core/UDS shutdown.
This is not a full multi-process observer/balancer/Sidecar composition.

Strict ten-minute mixed-load acceptance at 52b618bd passed (601.961s package
time): 19,984 TCP, 6,792 UDP, 2,102 OB checks, 119 retirement/recreation cycles
and 119 backend RSTs. Zero failed transactions or retries; owned workload workers,
echo connections/readers and active HTTP handlers were all zero after teardown.
The runner required the soak test's pass event, no skips, package success and
exit zero. JSONL and report: xray-config `.cache/anytls/strict-soak-52b618bd/`.
This workload is the direct native path; it does not replace chained fault-resource
tests or prove that all process-global background workers are fixture-owned.

`TestAnyTLSOutboundRetirementDuringTLS` passed 100 race rounds (314.985s):
direct and both AnyTLS chain entrances, complete ClientHello observed at a
silent peer before real gRPC RemoveOutbound, native dispatch 1/1 and retirement
completion within one second (below the 2s handshake budget), peer worker EOF,
and same-tag replacement traffic surviving an additional old-handler Close.
Remote socket join separately permits the relay's existing downlink-only window;
it cannot substitute for the prompt local cancellation assertion. Auth/SYN/return
native barriers are not claimed by this TLS-phase result.

### Native Auth Retirement and UDP Response Follow-up

`TestAnyTLSOutboundRetirementDuringAuthWrite` passed 100 race rounds (6.832s).
Its direct native handler completes verified TLS 1.2 before the first application
write is parked. Real gRPC removal must release that write, close the socket and
join dispatch and retirement within one second while the parent context remains
live. A retained old handler must reject another request without dialing; a
same-tag replacement serves authenticated traffic before and after old Close.
Chained inner auth and native SYN/return phases are not covered by this test.

`TestAnyTLSOutboundUDPResponseBoundaries` passed 30 race rounds (69.902s), log
`xray-config/.cache/anytls/udp-response-race30.log`. Nine direct/chained native
paths each check IPv4, IPv6 and domain response endpoints, exact legal payloads
(1/8192 bytes), explicit adapter errors for zero/8193-byte responses, and legal
requests after each rejection. Per-ID receipts must equal one after Core and
controlled peer teardown; a timeout is not accepted as rejection. The peer uses
the embedded engine over verified TLS, not an independent external server.

### Hy2 UDP Diagnostic Follow-up

The remaining intermittent UDP-boundary failure was investigated without
changing production transport code or retrying business requests. Each run used
both chain entrances 500 times (1000 subtests), with the actual system dialer
connection returned unchanged so QUIC's UDP/OOB interfaces remain visible:

| Fixture | Failures | Observation |
|---|---:|---|
| Default wildcard hop | 2 | One approximately 2s request failure; one extra endpoint datagram failure |
| Wildcard plus 64 owned loopback sockets | 7 | Six failures include confirmed port collisions; one request failure remains unexplained |
| Explicit IPv4 hop plus 64 owned sockets | 1 | No request EOF/collision observed; one extra endpoint datagram failure |

Logs: `/tmp/anytls-hy2-diagnostic-500.log` (83.698s),
`/tmp/anytls-hy2-diagnostic-occupied-500.log` (95.273s),
`/tmp/anytls-hy2-diagnostic-ipv4-500.log` (84.478s). No race detector report.
These are failed diagnostic runs, not acceptance passes.

One concrete collision assigned `[::]:60111` to the Hy2 client while an owned
IPv4 socket was already bound to `127.0.0.1:60111`. Replies from its server at
port 60850 reached that owned socket. The IPv4-control residual instead received
56/51-byte packets from the preceding fixture's client at a reused server port,
with the same short-header CID `46c7072c`; encrypted contents do not establish
that these are close frames. The earliest extra datagrams lack header evidence
and are not retroactively attributed. This narrows the failure classes but does
not close the unexplained request failure or strict receipt isolation gap.

`ANYTLS_HY2_OCCUPIED_PORTS=64` and `ANYTLS_HY2_BIND_IPV4=1` reproduce the
test-only pressure/control. The diagnostic owns and joins its loopback readers,
restores global hooks after Core closure, and must not run in parallel. Default
fixture binding and runtime behavior remain unchanged.

### UDP Boundary Fixture Isolation

Follow-up source inspection found that the existing Hy2 process-global manager
retains clients beyond an individual Core instance's Close. Its 30s maintenance
closes only inactive clients, not all connections at a fixed 30s deadline.
`interConn.Close` closes a stream, not the cached QUIC connection. Consequently
the boundary fixture now owns the actual UDP sockets it creates (returned
unwrapped) and explicitly closes them after Core shutdown and before endpoint
receipt draining. Late dial completion after cleanup also closes its socket.
Default test-only Hy2 source binding is loopback IPv4; set
`ANYTLS_HY2_BIND_IPV4=0` to investigate the earlier wildcard behavior. Production
transport code is unchanged. This supersedes the prior fixture default above.

With 64 owned occupied ports and explicit cleanup, both Hy2 entrances passed
500 race rounds each (1000 subtests, 75.698s), with no failed request, extra
receipt or race report. Log: xray-config `.cache/anytls/hy2-isolated-500.log`.
That run closed sockets at test cleanup; the final fixture also invokes the same
idempotent cleanup before the receipt oracle. This isolates the AnyTLS boundary
test; it does not prove native Hy2 manager shutdown or join every QUIC worker,
nor retroactively explain the historical request with missing packet evidence.
The final cleanup ordering then passed 30 race rounds of all nine native paths
(12.287s, 270 subtests), still with occupied-port pressure on both Hy2 paths.

### All-Relay Independent Processes and Reader Cancellation

The process fixture now covers direct and SOCKS/VLESS/AnyTLS/Hy2 through both
chain entrances, each with a separate entry, relay and final Core process.
The first expanded run passed (110.808s). Adding a relay-only outage correctly
rejected immediate recovery for both Hy2 paths while the old transport was not
yet observed closed; evidence remains in
`xray-config/.cache/anytls/strict-processes-relay-cuts/`.

A held-business-flow close barrier then exposed a distinct shared-reader defect:
dokodemo DispatchLink's raw readers erased Interrupt and AnyTLS waited for their
blocked upload workers. Both AnyTLS relay cases reached the 100s local deadline;
the failed long run was explicitly terminated, its owned process group cleaned,
and failure retained under `.cache/anytls/strict-processes-close-barrier/`.
Independent reproducer/fix evidence is in outbound-impact.md. No business retry
or shorter production pool/QUIC timer was introduced.

After forwarding Interrupt, the `processes` strict group passed all nine paths
(196.108s): verified TLS where configured, exact TCP/UDP responses, separate
relay and remote outages with zero failed-ID receipts, first new requests after
each restart, and exactly eight distinct once-only receipts per chain (five
direct). The held relay flow must fail without a local read timeout before
restarting the relay. Its 100s bound derives from existing pool/QUIC cleanup;
fixture connIdle is 150s so it cannot impersonate the close observation. This
observes the held flow, not every internal transport worker's lifetime.

All 43 child Core processes reported joined exits. Child Core exit remains
bounded by 5s; parent request workers are canceled and
joined even on fatal test paths. The runner requires all nine explicit pass
events, package success, exit zero, no skips and no surviving process group.
Redacted configs, JSONL and report are preserved in
`.cache/anytls/strict-processes-reader-interrupt/`. Fourteen runner tests pass
(11.002s), including missing each required process-chain event. Whole-Core
regression must be refreshed for the shared-reader change; earlier full-suite
passes cannot establish this revision's correctness.

The subsequent whole-Core race run recorded a DNS fixture failure:
`TestLocalDNSTransports/quic/timeout.test` received no query before its 300ms
request deadline; background QUIC dialing/cleanup completed around eight seconds.
The original log is `.cache/anytls/full-race-reader-interrupt.log`. This path uses
quic-go DialAddr, not the modified buf readers. Independent reproduction attempts
passed timeout-only 30x race (10.648s) and the complete QUIC subgroup 30x race
(12.133s), logs `/tmp/dns-quic-timeout-race30.log` and
`/tmp/dns-quic-all-race30.log`. Missing local socket evidence prevents attributing
the failure to the known Darwin wildcard collision or scheduling. No DNS runtime
or fixture timeout/retry was changed; these narrow passes do not erase the
whole-suite failure.
The run completed with exit 1 and only that DNS package failure. AnyTLS passed
425.013s, engine 8.662s, common/buf 4.999s and protocol scenarios 283.845s; no
race detector report. Command: `go test -race ./... -count=1 -timeout=12m`.
This establishes those package results, not a green whole-Core release gate.

### Follow-up Acceptance Evidence

Linux/arm64 full `go test -race ./... -count=1 -timeout=12m` passed at clean
`f9d6f9c4`, using an independent Git clone with read-only DAT/module mounts and
network disabled. Unlike the earlier git-archive run, the format-gate test had
real Git metadata and passed (1.306s). AnyTLS 429.326s, engine 6.096s, DNS 24.003s,
common/buf 1.201s, scenarios 264.501s. Log in xray-config:
`.cache/anytls/linux-address/full-race-f9d6f9c4.log`. This is not a strict run of
every opt-in group and does not cover subsequent test-only edits.

The default local DNS QUIC fixture now explicitly owns an IPv4 loopback socket,
with error/connection/cleanup closure and joined workers. It passes the concrete
UDPConn to quic.Dial (preserving OOB and single-use client CID behavior), without
changing production DNS or retry/deadline policy. Full QUIC subgroup race 100
rounds/600 leaves passed (35.986s), `/tmp/dns-quic-ipv4-race100.log`. The optional
wildcard64 diagnostic remains available. One old diagnostic failure and the old
whole-suite timeout lack attribution; do not retroactively call them explained.

OT-03 concurrent fixture now synchronizes all 32 workers before 16 requests each,
records fixed seed 80303216, and checks unique payload IDs across varied sizes.
Every logical stream opens a real TCP destination socket. After owned workers
join, logical open/close and destination open/close counts must each be 512,
with 512 once-only receipts and zero remaining owned sockets. Physical dial,
accept, authenticated-session and closed-session counts must agree, without a
one-connection demand for concurrent traffic. The backend waits for the proven
upload completion boundary before replying. Darwin race 30 rounds passed
(12.903s); Linux/arm64 race 30 rounds passed (12.591s). Service streamWG joins
both copy workers before final accounting.

Sibling Sidecar's new `anytls_outbound_runtime_test.go` exercises live Core
gRPC add/remove -> actual HTTP status decoding and verified-TLS native outbound
traffic through two Core instances. It checks credential exclusion, runtime
protocol/address/user identity, exact logical user/domain bytes at both hops,
duplicate bucket idempotence and domain HTTP rendering. Ten race rounds of both
tests, including explicit scalar user-counter checks, passed (11.697s);
the candidate modfile, not the release pin, selects the local Core. Sidecar
release pin/deployment remain unchanged and test work belongs to that repository.
The subsequent complete Sidecar race suite with the candidate modfile passed:
main package 29.190s, route-match 1.663s. No tests skipped due to a missing
candidate outbound registration (`ANYTLS_STRICT=1`).

Subsequent fixture correction separates healthy completion from in-flight abort:
the sequential reader signals only when Copy enters the next ReadMultiBuffer
after the full payload. The previous WriteMultiBuffer, padding and watcher join
have therefore returned before cancellation. Original 100-request/one-physical-
connection assertion is unchanged. Linux race 100 rounds passed (25.046s),
Darwin 100 rounds passed (16.163s). This supersedes the sequential-reuse failure
as an OT-03 fixture-precondition issue, not a claim that arbitrary in-flight
aborts must preserve their physical session.

`TestClientDeliveredWriteCancellationAndFreshRecovery` provides the complementary
controlled boundary: real TCP writes deliver the first request but their return
is held until Close; the peer echoes it while Write remains active. Cancellation
must join that write and Process, then a fresh request succeeds. Exactly two
physical connections and the two distinct once-only peer receipts are required,
after both peer handlers join. Linux race 100 rounds passed (1.143s); Darwin
combined with sequential reuse passed 30 rounds (6.214s). No retry is introduced.

OB resource-count fixtures use the same next-read boundary, parsing completed
HTTP requests before allowing the peer's response/FIN. The parser handles split
headers/bodies/chunked requests and coalesced requests; all split positions and
Interrupt forwarding have a unit oracle. HTTP/logical/TLS/session-close exact
counts remain unchanged. Darwin focused race 30 rounds passed (34.803s);
Linux/arm64 race 30 rounds passed (34.599s), network-disabled container.

The narrowed reuse/OB tests reproduced repeatedly on Linux/arm64 (30 rounds,
40.850s, failure), while Darwin passed the same 30 rounds (39.157s). Source
review found a definite late-cancellation window: after the writer closes the
finished channel, select can still choose the simultaneously closed stream
channel and close the physical session. Per-write atomic arbitration now makes
completion and cancellation mutually exclusive winners; stop still joins the
watcher before releasing the session write lock. No pool timeout/retry changed.

The initial GOMAXPROCS(1) regression failed before the fix at iteration zero and
passed 100 rounds after it (1.611s), but review correctly found that preemption
can let cancellation win before stop, even with one P. That stress oracle was
replaced by explicit completed-then-close and canceled-then-complete tests,
covering both stream and deadline cancellation (100 rounds, 1.781s). These
ordering tests do not independently reproduce the original simultaneous-ready
select interleaving. Engine race passed 6.422s; protocol vet and format passed.

The fix does NOT close the Linux reuse gate: a subsequent 30-round run still
failed (39.891s), and also encountered two fixture port-allocation collisions.
Darwin reuse/OB plus physical-write/lifecycle cancellation tests passed 30 rounds
(39.243s). An opt-in `ANYTLS_REUSE_DIAGNOSTIC=1` test wraps the fixture's physical
socket and records Close stacks and active Write calls. On Linux a 100-request
run observed three watcher-triggered closes, each with activeWrites=1; final
retirement had activeWrites=0. Thus receipt of a complete echo is not proof that
the sender's Write call has returned. The diagnostic does not reveal kernel
buffer completion or justify retaining a potentially partial frame. Normal
completion reuse versus cancellation of an in-flight physical write still needs
separate faithful acceptance oracles; do not relax counts or add sleeps/retries.

Additional 2026-09-28 evidence at committed `8d966b71`:

- Linux/arm64 whole-Core race against a writable `git archive 8d966b71`
  snapshot, read-only DAT/module mounts and network disabled exited 1.
  `TestClientSequentialReuseAndContextIsolation` observed 9 physical connections
  rather than 1; `TestAnyTLSOutboundOBResourceCounts/ordinary/keepAlive=false`
  observed 2 TLS sessions rather than 1. These are unresolved reuse assertions,
  not accepted platform skips. AnyTLS package duration: 426.603s.
  `TestFormatGateRejectsWithoutModification` also failed because the archive
  intentionally lacks `.git`; that fixture setup error is separate from reuse.
  DNS (23.906s), common/buf (1.206s), engine (6.124s), and scenarios (266.280s)
  passed. Log: `.cache/anytls/linux-address/full-race-snapshot-8d966b71.log`.
  The earlier read-only-source attempt had missing DAT and certificate output
  write failures; it is not product evidence. Both container runs have exited.
  A final local build is a compile/identity check only, not release acceptance.
- After the opt-in diagnostic addition, default Darwin local DNS transport
  tests passed under race (2.667s); `go vet ./...`, gofmt gate and diff checks
  passed. They do not supersede either whole-suite failure.

- Strict Linux/arm64 address group passed (1.438s), no skips, clean source,
  Go 1.26.1, network-disabled container. Report:
  `.cache/anytls/linux-address/strict-8d966b71/report.json` in xray-config.
  Covers the fixture's IPv6, DNS locality, source alias and kernel interface
  assertions, not every relay/address Cartesian product.
- The approved wrapper built Darwin/arm64 and Linux/amd64 from that clean
  revision; both identify `vcs.modified=false`. Darwin minimal outbound config
  validation passed. Amendments require rebuilding to refresh artifact identity.
- An opt-in DNS fixture diagnostic (`XRAY_DNS_QUIC_DIAGNOSTIC=wildcard64`)
  preserves DialAddr's wildcard allocator and zero-length client CID while
  recording concrete sockets and 64 owned IPv4 listener receipts. Command:
  `XRAY_DNS_QUIC_DIAGNOSTIC=wildcard64 go test -race ./app/dns -run
  'TestLocalDNSTransports/quic' -count=100 -v`. The recorded run took 61.119s:
  597 passed / 3 failed leaves, no race report. Two failures have collision and
  misdirected QUIC packet evidence; a third malformed.test handshake timeout
  has no collision evidence. Log: `/tmp/dns-quic-wildcard64-race100.log`.
  This does not prove the cause of the earlier whole-suite timeout, fix DNS,
  or establish all-green acceptance. Normal tests/runtime retain their original
  dial, timeout and retry behavior. Diagnostic sockets/workers are explicitly
  closed/joined; the diagnostic run has exited.

The approved wrapper completed on 2026-09-28 after local commit `8e10c0ba`:
Darwin/arm64 and Linux/amd64, both with VCS metadata and a clean source tree.
Darwin `version` executed and the minimal AnyTLS outbound fixture passed
`run -test -config ../xray-config/.cache/anytls/outbound-validation.json` with
`Configuration OK`. The Linux file was verified as a statically linked x86-64
ELF; this is a cross-build check, not Linux runtime acceptance.

This evidence-only amendment keeps the feature as one local commit. Rebuild after
amending so both final artifacts identify the final HEAD, and verify
`vcs.modified=false`. Build products remain ignored in xray-config/build; no
production node or local LaunchAgent is updated by this build.
