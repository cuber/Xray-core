# Verification Plan

This matrix was written during the draft investigation and is now the candidate
acceptance plan. Historical statements below describe that investigation, not
the current execution state. Current results and owning-spec evidence are linked
from [implementation.md](implementation.md); an unchecked gate is not accepted.
Existing test functions alone are not proof they passed against this candidate.
Store results with commit, dirty state, OS/Go version, config/DAT/client hashes,
command, exit status, test count/skips, resource cleanup and elapsed time.

## Fixed Main Refresh: Migration Gates (2026-10-14)

Candidate upstream is `0888beb38a9ba45440557175e71ce21cf8ba3b13`, not moving
origin/main. The [refreshed review](review-20261014.md) records 18/254 commits,
363/680 paths, 68 overlaps and 34 merge-tree conflicts. Its separate evidence
generator and --check passed. The older V-001 numbers below remain historical.

The following U-* cases are proposed test work, NOT existing passing tests.
Run each relevant case on the selected unmodified upstream and on the adapted
candidate, distinguishing an upstream defect from an integration regression.

| ID | Fixture / operation | Required oracle |
|---|---|---|
| U-001 SS revocation | Two users with active UDP sessions; remove A through real gRPC, send A/B packets, re-add A with a new key | Declare old-session revocation policy explicitly; verify old/new keys, no cross-user route/counter attribution, B unaffected; TCP sessions tested separately |
| U-002 SS allocation | Controlled local invalid EIH/AEAD packets with unique session IDs, then stop traffic; fake clock or injected cleanup tick | Unauthenticated traffic must not retain an unbounded session set; prove cleanup without needing another new packet; measure session/window counts, no external flood |
| U-003 SS concurrency | Interleave A/B UDP sessions sharing inbound transport; roam A's source; run under -race | Each dispatcher context and reply belongs to its authenticated user, no shared-context race or response to B; close/remove/re-add does not revive stale link |
| U-004 chain migration | Generate Sidecar via and SOCKS UDS configs; TCP and UDP through SS/VLESS/AnyTLS/Hy2, with domain endpoint and refused hop | Parser accepts replacement; exact intended hop/DNS site; no direct escape; per-hop and user byte oracles unchanged; both APIs populated must fail or follow a documented compatibility rule |
| U-005 finalRules | Convert each intentional empty ipsBlocked fixture; private/public A+AAAA, loopback, WG address, UDS, explicit block and dialerProxy | Preserve prior intended allow paths; explicit block never dials target; distinguish timeout from deliberate delayed block; AnyTLS policy matches documented contract |
| U-006 Stats API | Concurrent first-use registrations plus spec006 writer/error/EOF/sniff tests | Same counter instance, exact normal totals, no lost first bytes, no duplicate domain bytes; disabled DomainTraffic does not disable ordinary Stats |
| U-007 proto/control | Old and candidate Sidecar clients query and modify an isolated candidate; regenerate descriptors | ListInbounds/users/certificates/OB/ListRule/domain traffic decode correctly; rejected incompatible mutation is explicit and atomic, not a silently misread field |
| U-008 OB | Fake clock plus real local H2 server and cancelable dialer | 3s configured cadence preserved; latest-dead/N-success startup contract unchanged; retry stays inside total timeout; probe transport identity distinct from measured subject; Close joins work |
| U-009 Hy2 | Cancel while waiting client lock and during QUIC auth; cleanup concurrent with another destination; repeated Core start/stop | Request cancellation does not kill unrelated live sessions; cleanup does not hold map lock across I/O; retired instances release transports/map entries and cannot dial again |
| U-010 router | Reload with valid webhook followed by invalid rule/balancer; concurrent ListRule/selection | Failed reload leaves previous rules usable and closes newly owned resources; successful publication returns coherent metadata/selection; weighted distribution and failover preserve spec005 |
| U-011 TLS/UDS | Public-like test CA explicitly configured, system-only CA, wrong name, stale/live/symlink API sockets | Expected trust policy enforced without allowInsecure; API listener never steals a live socket or removes an unrelated path; restart and gRPC reconnect work |
| U-012 matcher | Current DAT hashes and independent matcher oracle; repeated reload/GC with active references | Exact matching including case/regex/attrs; active matchers survive; retired caches collectible; measure peak/live heap/RSS/lookup time independently, no invented savings |

Retain all spec002-009 tests in addition to U-*; these additions do not narrow the
existing external-client, Windows readv, mux, chain, snapshot and resource gates.
No runtime tests were run for the new upstream in this audit; previous fork-green
results cannot be transferred to a candidate that has not been implemented.

## V-001: Inventory and Provenance (Executed for Cached Refs)

Run inventory.py and then --check with the arguments in inventory.md. Assert:
merge-base equals the fixed fork base, 8 fork commits, 203 unique upstream commits,
326/569 changed paths, 67 intersections and byte-identical full diffs/SHA-256.
Parse every JSON and TSV. Validate local Markdown links and index discoverability.
The fresher web history must not change the cached diff's claimed endpoint.
Wrong ref, missing object or changed config evidence must fail rather than silently
fetch/reset. Re-run after any future baseline or configuration change.

## V-002: QUIC Panic Regression (P0, FR-006)

Use the pre-fix fork as baseline. Construct valid QUIC Initial headers with the
header-protection sample truncated at length 0 through blockSize-1; cover packet
number lengths 1..4, supported versions, oversized offsets and empty packets.
At least one reproducer must panic on the baseline and return errNotQuic/error
without panic on the fix. Also decode a valid locally generated Initial/SNI and
assert unchanged extracted domain, not simply no crash.
Add seeded fuzzing for SniffQUIC (proposed test, not an existing command): run
60s locally and longer in CI; record retained corpus. Bound per-input allocation
and runtime. Test public SS sniffing paths with routeOnly behavior unchanged.

## V-003: SS Silent and Partial Handshakes (P1, FR-003)

Use a local owned listener/accepted net.Conn and fake dispatcher. Configure a
1s handshake timeout; test single-user, multi-user and relay variants separately.
Exercise no bytes, only salt, incomplete fixed header, wrong key and slow byte
trickle. The total authentication budget is absolute, not renewed per byte.
Record server-side closure and Process return, not the test client's self-close.

Oracle: baseline silent peer remains open after its policy budget, while fixed
server closes within timeout plus <=1s scheduling budget. Wait for all owned
goroutines with completion channels/WaitGroups; do not rely only on sleeps or
runtime.NumGoroutine. Fake dispatcher records zero successful dispatches and
ordinary authenticated-user counters remain unchanged. A timeout/error returned
by SetReadDeadline itself is propagated and the accepted socket is released.
Ensure test cleanup closes the baseline peer so reproducing failure cannot hang CI.

Repeat 200 silent connections over 10 waves. After each wave, outstanding handlers
and sockets return to baseline; post-GC retained live objects must plateau after
warm-up. Use counted owned resources as the deterministic oracle; Go background
goroutine count/FD sampling are supporting diagnostics, not exact global equality.

## V-004: Successful Handshake Is Not a Session Timeout (FR-003/004)

Use real SS wire encryption and two distinct test users routed to separate local
echo targets. Authenticate before 1s, remain connected for >=3s, transmit data
before/after the handshake budget, and verify exact replies and once-only receipts.
Use longer ConnectionIdle policy so it cannot mask deadline-clearing failure.
Capture deadline calls on the underlying conn: finite before authentication,
cleared before long-lived copy. Include n>0 with EOF/error and an authentication
header plus early payload. A valid user's selected Level must control normal
session timeouts, not pre-auth level0 forever.

Trigger client cancellation, target dispatch failure, EOF and half-close. Assert
downlink/uplink-only policy transitions, no retained timer/pipe/socket and correct
return. Validate zero/disabled handshake policy according to the approved existing
policy contract. Do not disable all idle handling to make long-lived tests pass.

## V-005: SS Control Plane, UDP and Native Engine Compatibility (FR-004/005/009)

First run existing SS2022 outbound-close regression for TCP/UDP and TimeoutOnly.
For dynamic users, add/list/count/remove via actual HandlerService RPC with two
identities; validate per-user routing, duplicate/re-add semantics and denial of
new sessions after removal. Track already-authenticated TCP/UDP session behavior
separately; do not claim immediate live revocation unless explicitly implemented.

If selecting native SS: run old fork client -> new server and new client -> old
server, plus independent sing-box/Mihomo clients where supported. Pin binary
versions/hashes. Test AES128/AES256, master:user keys, long bidirectional payloads
larger than multiple frame/buffer boundaries, initial early data and multi-user
UDP responses decryptable with the correct user's key. Include fragmented header
delivery and first-header framing per SIP022: a single read requirement must be
verified against accepted clients, not inferred from TCP packet boundaries.

Test UDP wrong key, replay, out-of-order/duplicate packets, session expiry,
RemoveUser followed by packets from a cached session, and correct source/target
for simultaneous destinations. Decide/reconcile the revocation contract before
native migration. No tests using only new Xray at both ends are sufficient to
detect the old-client regression reported upstream.

## V-006: DNS Name/Error Handling (FR-006)

Use owned UDP, TCP, DoH and DoQ fixtures with trusted certificates. Names:
ordinary FQDN, trailing dot, empty input, overlong label, total name >255 bytes,
and maximum valid boundaries. Invalid input must return a bounded error, never
panic, allocate repeatedly or enqueue unresolved pending questions indefinitely.

For A-only, AAAA-only and A+AAAA, assert exactly the expected number of result/error
completions and that sendQuery callers terminate after cancellation. Include DoH
self-resolution protection, timeout, negative reply and valid cache-hit cases.
Preserve local DoQ injected TLS roots; public network reachability is not a required
oracle. Run race checks for simultaneous valid/invalid calls and shutdown.

## V-007: Buffered Writer Exact Bytes (FR-004/006)

Test buffered/unbuffered modes at empty, remaining-1, remaining, remaining+1,
capacity*2+1 and multiple full buffers, with an already partially filled buffer.
Compare output bytes to a separately generated deterministic payload and ordering.
Baseline partial-capacity reproducer must fail, candidate must pass. Inject flush
and underlying short-write/errors at each boundary; returned n/error and retries
must not lose or duplicate bytes. Verify packet framing and existing readv/reader
contracts, plus user/inbound/outbound counters through an end-to-end transfer.

## V-008: Atomic Stats Registration (FR-004/005/006)

Start 128 goroutines behind a barrier; each gets/creates the same counter and adds
a known number of bytes. Assert pointer identity, no duplicate-registration errors
and an exact total, then reset and increment again. Repeat with different names,
unregister/recreate, OnlineMap and Channel. Channel must be started before callers
observe it and stop exactly once on Close; injected Start errors must not publish
an unusable channel. Include NoopManager and all test mocks after interface changes.

Run actual TCP/UDP user transfers for two authenticated users plus unknown/admin
identity, compare ordinary RPC counters against received fixture bytes, and prove
DomainTraffic disabled leaves Stats available. DomainTraffic enabled must retain
LRU discard semantics, no other bucket, sequence/boot IDs and unchanged proto fields.

## V-009: Router Coherent State and Cleanup (FR-005/006)

Run PickRoute, detailed ListRule, ReloadRules append/replace, RemoveRule and balancer
override concurrently under -race. Use two distinct complete rule/balancer
generations. Each result must belong to one valid generation; never missing/mixed
references, partial rule state or mutated returned protobufs. Inject invalid rule,
duplicate tags, missing balancer and webhook construction failure mid-build.

Oracle: failed replacements preserve all old routes and close every newly created
owned resource; successful replacement closes retired resources according to the
approved in-flight lifetime contract. Use resource counters/channels, no fake
network webhooks. Verify user:domain matching, leastLoad tolerance, weighted ratios,
healthy/unhealthy/all-unhealthy pools and override/remove/recovery behavior.

## V-010: Geodata Cleanup and Semantic Equivalence (FR-007)

Build a fixed synthetic rule set: full, suffix/domain, keyword, regex, mixed case,
overlapping matches, IPv4/IPv6, geosite attributes and repeated ext references.
Compare matched rule IDs/first route to an independent simple reference matcher,
not to two calls through the same optimized implementation. For MPH compaction,
assert Full-vs-Domain precedence, collision behavior, empty groups, duplicate values,
invalid regex/overflow errors and prefilter false-negative absence.

Hold a live routing snapshot across repeated GC; every live matcher still works.
Drop all strong references to retired snapshots over 300 churn rounds. Use bounded
GC/retry observations of weak pointers/cleanup, not a flaky assertion that one GC
immediately runs every cleanup callback. Retired entries become collectible;
cleanup for an old value must not remove its replacement under the same key.
Run cache Store/Load/Range/reload/GC concurrently under -race.

Benchmark a pinned production-like DAT and record its SHA-256. Separate construction
peak, retained heap after build/GC, objects, query ns/op and allocs/op. Run on Linux
and Darwin; do not quote unmeasured percentage savings or assume mobile matcher
changes have equal desktop benefit. Confirm static no-churn live heap is legitimate.

## V-011: Conditional Transport/OB/HY2 Tests (FR-005/006)

- XHTTP: forced RoundTrip error/non-200/upload-only success, then body/conn close
  counters; WaitReadCloser Set/Close/Read races and EOF cleanup; H3 UDP ownership.
- Observatory: empty selector with bounded CPU/work counter; Close responsive.
  Burst: retain 3s interval/2s timeout, per-prefix/query URLs, pending-round cancel,
  no first-sample false-positive, latest-failure dead, recovery <=3 successes,
  keepAlive and service Close joins all workers.
- Hy2: dynamic user authentication, TCP and UDP exact bytes, ChromeParrot boundary
  datagram sizes and errors, manager Add/Remove/Close races without blocking global
  lock; sendThrough/interface/dual-stack binding and outage recovery.
- Mux.Cool report: accept-and-immediately-close fake remote, race-enabled Dispatch
  versus SessionManager.Close; bounded pipe/resource cleanup. A newly written
  reproduction is required before claiming this external report applies locally.

## V-012: Existing Fork Contract Regression (FR-005)

| Existing Core contract | Must remain true after every shared change |
|---|---|
| spec002 connection lifecycle | dispatchConn cancellation/Interrupt, first bytes, Hy2 per-client lock boundaries |
| spec003 listeners/UDS | multi-IP partial-bind cleanup, SOCKS UDS auth/absolute path, Freedom UDS and local-domain probes |
| spec004 observatory | 3s cadence, destinationsByPrefix/pingGroups URL overrides, ob= and health hysteresis |
| spec005 routing/balancing | full copied ListRule details, user-domain and weighted ratios, coherent API changes |
| spec006 user-domain traffic | user isolation, fixed retention/LRU/cursor/boot, optional service and Sidecar snapshots |
| spec007 shared regressions | DNS/ECH/REALITY/VLESS/XHTTP/readv and offline test infrastructure |
| spec008 AnyTLS | inbound/outbound, TCP/UoT, identity/stats/domain, pooling, quotas, controller and chains |

Run the existing AnyTLS acceptance runner with pinned sing-box and Mihomo binaries,
including native processes/chains, OB/balancer/Sidecar integration and real default
idle/ten-minute soak where touched. Retaining a singbridge file is not evidence its
semantics survived. No successful fallback bypass may conceal a broken first hop.
Verify returned certificate/control fields and Sidecar/TUI generated proto consumers
for any API/schema change; do not renumber existing fields or remove optional APIs.

## V-013: Whole-Tree Gates and Official Builds (FR-005/006/010)

Commands below are existing gates, to run only against the future candidate:

```sh
go test ./common/protocol/quic ./proxy/shadowsocks_2022 ./common/buf \
  ./app/dns ./app/stats/... ./app/router/... -count=1 -timeout=10m
go test -race ./common/protocol/quic ./proxy/shadowsocks_2022 ./common/buf \
  ./app/dns ./app/stats/... ./app/router/... -count=10 -timeout=15m
go test ./... -count=1 -timeout=15m
go test -race ./... -count=1 -timeout=15m
go vet ./...
bash .github/check-gofmt.sh
```

No passing-by-skip claim, certificate verification disablement or vet=off.
Opt-in public tests use documented XRAY_TEST_NETWORK/XRAY_TEST_PROXY; an unreachable
enabled public test remains failed, not silently direct. Local fixtures must work
without depending on public DNS/proxy. Missing dependencies/network prerequisites
are reported, never hidden by claiming an unexecuted full suite passed.

After a local committed release state and build authorization, run from xray-config
`./scripts/xrayctl.py build core`. It must emit build/xray-darwin and
build/xray-linux-amd64 and preserve VCS revision/time. Execute Darwin version and
native run -test using real cert fixtures/DAT. Linux listener/interface/resource
tests require a controlled Linux environment; cross-compiling is not running them.
No release build or deployment was done for this document-only task.

## V-014: Long-Duration Memory and THP A/B (FR-008)

Use four independent runs: old/candidate code crossed with baseline/THP mitigation,
same config, Go version, kernel, service limits and deterministic workload. Record
uptime and process restart explicitly. Include idle startup, authenticated traffic,
silent/partial probes, user updates, route churn, traffic drain and a quiet tail.

Sample RSS, AnonHugePages, HeapAlloc/Inuse/Idle/Released/Sys, post-GC inuse profile,
goroutines, accepted/authenticated/pending sockets, FDs and service cgroup memory.
Retained sockets and owned goroutines must be bounded after each failure wave.
After warm-up, identical churn cycles must not monotonically retain live heap.
THP A/B isolates resident-page inflation; heap-plateau tests isolate object retention.
Define tolerances from baseline variance before judging performance; no arbitrary
global FD/goroutine equality or speculative memory-saving threshold.

Soak for at least one hour locally, then 24-48h on an authorized canary under
representative traffic. Separate source-fix success from operational mitigation.
Counters/user routes/admin/usersync must remain correct; no full upgrade is accepted
solely because process RSS fell immediately after restarting.
