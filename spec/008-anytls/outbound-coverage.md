# AnyTLS Outbound Coverage Audit

Date: 2026-09-28. Read-only source audit against [outbound.md](outbound.md) and
[outbound-tests.md](outbound-tests.md), including the latter's stricter procedures.
Core snapshot: `af1e18eef94fef66d29402d7af6e232594c2764a`, initially clean.
Sidecar snapshot: `7e1f36dace7a97dfdaa0da32a2677ed5205bfd26`, including its existing
untracked `anytls_outbound_test.go`; that candidate test is not release-pin evidence.
Only this document was written. No tests, builds, commits or deployment were run
for this audit. Earlier run results are attributed to
[outbound-implementation.md](outbound-implementation.md), not newly verified here.
The final source review also includes in-progress untracked additions:
`outbound_udp_stats_test.go`, `client_lifecycle_edges_test.go`,
`client_resource_rounds_test.go`, `outbound_chain_fault_test.go` and
`outbound_chain_probe_test.go`, plus `outbound_shutdown_test.go` and
`outbound_chain_counts_test.go`, `outbound_user_reuse_test.go`,
`client_malformed_test.go` and `outbound_validation_matrix_test.go`.
The strict Python runner and its validator tests
under testing/ were also read in this update.
Parent-reported completed runs (not rerun by this audit): UDP strengthened
remote-account assertions 10x race PASS (91.252s); lifecycle edges 100x race
PASS (1.817s); native shutdown 100x race PASS (13.683s); client resource rounds
3x race PASS (2.812s); extended chain-layer counts/wire oracle 3x race PASS
(63.4s); canceled-user actual reuse 10x race PASS (33.622s); malformed response
100x race PASS (1.854s); native/JSON validation matrices 10x race PASS (4.153s);
chained probe isolation 3x race PASS (57.3s).
The implementation record also reports a combined focused race PASS (53.121s),
not a new whole-suite run. Chain-fault work remains in convergence: the record
reports a default eight-case PASS (75.230s, opt-in diagnostics skipped), but also
an unexplained earlier 45-second Hy2 protocol-close deadline failure. Subsequent
passes do not resolve that failure; this audit does not declare chain-fault fully
passed or the defect fixed. Source assertions and opt-in diagnostics are not a
strict all-groups acceptance result.
The strict-runner external and idle groups are reported PASS, with required
test events present and no skips. Address/soak completion through this new runner
is not reported.
Source coverage of the runner is recorded separately from completed group runs.
This is a stage snapshot, not a claim that every deep native integration scope
or all acceptance groups have been completed.

## Classification

### Follow-up Overrides After cd5ed598

The following new evidence supersedes stale absence statements in the original
matrix below; it does not promote the umbrella rows to fully covered:

- Separate-process coverage now defines direct plus all four relay protocols
  through both entrances, rather than only AnyTLS relays. Entry, relay and final
  server are separate native Core processes. The `processes` strict-runner group
  requires all nine subtest pass events and a clean process group. The first run
  passed (110.808s). The stronger relay-only cuts exposed raw-reader cancellation
  erasure; after the independently tested fix, strict execution passed all nine
  paths (196.108s), including observed held-flow closure before relay restart,
  first-request recovery, no-bypass and exact final receipt counts. This does
  not cover the combined observer/balancer/Sidecar topology.
- Repeated Hy2 UDP diagnostics now prove Darwin wildcard/IPv4 port collisions
  in six failing cases and preceding-fixture traffic entering a reused port in
  the IPv4-binding control. One request failure remains unexplained; no claim
  of complete Hy2 boundary acceptance is made. See the implementation record's
  three 1000-subtest runs, failed counts and reproduction environment variables.
- Isolating that fixture with loopback IPv4 and explicitly owned UDP socket
  cleanup passed both Hy2 entrances 500 times (75.698s, 1000 subtests). Existing
  Hy2's process-global manager outlives individual Core instances; fixture
  cleanup is not native Hy2 shutdown acceptance. No runtime transport change
  or business retry was added, and historical unexplained evidence remains.
- Native auth-write retirement now has a verified TLS 1.2 application-write
  barrier followed by real HandlerService removal: dispatch, socket and write
  worker join, old-handler rejection without a new dial, and replacement traffic
  surviving old Close. One hundred race rounds passed (6.832s). This covers the
  direct path, not chained inner authentication or native SYN/return barriers.
- UDP download boundaries now cover nine native direct/relay paths, IPv4/IPv6/
  domain endpoints, exact 1/8192-byte replies, explicit rejection of 0/8193-byte
  replies, fresh recovery and once-only request receipts. Thirty race rounds
  passed (69.902s). The controlled server uses the embedded engine; this is not
  independent sing-box/Mihomo malformed-packet acceptance.
- C-04/OT-12: ChainedReplacementDrains now holds a replacement stream across
  old-only drain completion and verifies both continued traffic and new admission.
  Ten race rounds pass. Other native retirement phases/resources remain open.
- C-06/OT-06: exchangeUoT now compares the full endpoint, including the reply
  port. Three race rounds of inbound/native outbound accounting pass. Invalid
  sizes, UDP bypass and the original simple chain helper remain separate gaps.
- OT-02/C-06 external-final cross-product now has an executable 16-path test:
  SOCKS/VLESS/AnyTLS/Hy2 x both entrances x sing-box/Mihomo, exact TCP and UDP,
  concurrency, stopped-server failure, observed-hop teardown and fresh recovery.
  Race PASS 57.105s after fixing independently reproduced shared readv terminal
  error loss. The earlier failures are retained in the implementation record.
  Separate Core processes and external per-layer wire/idle accounting remain.
- External-client -> Core inbound -> native outbound is now covered for both
  independent clients, including trust rejection, exact TCP/UDP user counters,
  endpoint replies and native-handler removal no-bypass. The strict external
  group now requires eight paths, including both reverse clients, and passed.
- Hy2's earlier fixed 45s close assertion ignored late ack-eliciting TLS cleanup
  on an older idle stream. A 10s pool-timer differential moved that send to 10s
  and the observed closure to 40s. The current test uses a derived 100s upper
  bound, requires live context at EOF, and keeps first-new-ID recovery strict.
  Default-timer repeats passed both entrances twice with live contexts and
  first-new-ID recovery (194.676s). Full Core race at 330cd213 subsequently passed
  (AnyTLS 279.214s, existing scenarios 277.103s); see implementation record.
- ChainFaultReceipts now closes its Core fixture and joins backend workers before
  checking receipts, replacing the 100ms sampling sleep. TCP relays x both
  entrances passed three race rounds (20.943s); Hy2 dialerProxy passed a joined
  fixture round (34.686s).
- Shared impact now includes common/buf readv error propagation. Its independent
  reset and mock-poller tests pass 100 rounds; full regression/build evidence must
  be refreshed. A green run preceding this delta cannot validate it.
- Independent Core-process direct/AnyTLS-relay paths now cover both entrances,
  restart recovery, exact TCP/UDP receipts and joined shutdown. The first run
  exposed a TimeoutWrapperReader cancellation defect; independent reproducer
  and forwarding fix are documented in outbound-impact.md. Three race rounds
  passed afterward, with all 33 child processes joined and no forced kill.
  Other relay protocols and the combined OB/stats multi-process topology remain.
- Intact oversized UDP rejection and broken-chain no-direct-bypass now have
  nine native paths with endpoint receipts, legal boundary packets and restored
  handler recovery. Thirty race rounds passed; an earlier single Hy2 EOF remains
  unexplained, with enriched diagnostics retained. Zero-length and oversized
  download integration remain outside this fixture.
- Rejected constructed handlers now have an independent real factory/manager
  test with explicit worker join and close counts, including duplicate and
  already-closed-manager cases (100 race rounds). This does not close the
  remaining native gRPC TLS/open/return retirement-race scope.
- Native gRPC removal during a verified ClientHello stall now passes direct and
  both AnyTLS entrances for 100 race rounds (314.985s), with dispatch/retirement
  completion, peer EOF and replacement survival after old Close. TLS admission
  is covered; remaining native auth/open/return race phases remain separate.
- Chained concurrent user/probe coverage now uses a real UDS for all four relay
  protocols and both entrances (eight paths, three race rounds, 76.147s). Query,
  user counts, failure isolation and shutdown join are asserted together;
  full combined multi-process observer/balancer/Sidecar composition remains.
- Strict ten-minute native soak at 52b618bd passed with 19,984 TCP, 6,792 UDP,
  2,102 OB checks and 119 retirement/RST cycles; no transaction failures and all
  explicitly owned workers/echo connections/HTTP handlers zero after teardown.
  This closes execution of that direct-path gate, not the entire multi-protocol
  fault/resource inventory.
- The strict runner now has POSIX process-group cleanup and a wall-clock bound,
  with independent child/grandchild/listener tests for interruption, timeout,
  parse errors and exit-zero residual processes. Thirteen Python tests pass.
  Supported fixture constructors persist redacted JSON; not all dynamic gRPC
  mutations or fixture-owned workers have a complete persistent inventory yet.

- **Covered**: assertions exercise the specified behavior at the stated scope.
  This does not mean every protocol/platform combination or release gate passed.
- **Partial**: useful assertions exist, but a material required oracle, phase,
  topology or integration boundary is absent.
- **Missing**: no test assertion exercises the stated outbound requirement.
- A successful request is not proof of pool reuse, a wire byte counter is not a
  connection count, and a timeout result is not proof of worker termination.
- `newFixture` normally tests AnyTLS **inbound -> freedom**, whereas
  `outboundFixture` inserts the native AnyTLS outbound and remote inbound.
  Their evidence is not interchangeable. Most chain fixtures share one Core
  instance; they are not isolated multi-process Core topologies.

## Highest-Priority Gaps

| Rank | Requirement | Gap and minimum useful closure |
|---|---|---|
| 1 | C-03 / OT-03 | **Remaining per-layer counting scope.** ChainLayerCounts now includes authenticated Alice cancellation then Bob reuse with exact counters on both AnyTLS entrances; CanceledUserThenReusedPool covers direct and both chains with a real system-dial count. Still missing counted SOCKS/VLESS/Hy2/external/deeper paths and separate final business destination sockets. |
| 2 | OT-12 / OT-08 / O-07 | **Missing remaining native retirement/publication phase races.** Unit barriers now cover auth/SYN/data and late dial; new native shutdown covers old draining pool beside replacement and full instance close. Still barrier-control native gRPC removal during actual TLS/open/return, assert old pool's independent cleanup leaves replacement usable, and inventory rejected/new/old workers and timers. |
| 3 | OT-07 / C-05 / O-05 | **Unresolved chain-fault stability and incomplete fault phases.** ChainFaultReceipts checks established relay cuts with request IDs, but its earlier Hy2 protocol-close deadline failure is unresolved. ClientMalformedResponseDoesNotReplay now covers truncated response header/body after business delivery at adapter scope. Native TLS/chained malformed peers, partially delivered application records, dial/handshake cuts and UDP no-replay still need bounded recovery and owned-resource oracles. |
| 4 | Resource gate / O-04 / OT-12 | **Incomplete native chained/fault resource assertions.** New client resource rounds close the healthy adapter-level owned-socket/pending/active and warmed heap gap. Still repeat native gRPC failure/recreate and chained rounds with owned workers/timers plus bounded FD/goroutine/heap growth. Supplied-dialer healthy client rounds do not cover real TLS/proxyman/relay resources or failed admissions. |
| 5 | OT-09 / O-06 | **Remaining accounting cross-product.** Two-layer TCP now has physical reuse, authenticated user totals and exact client/hop post-TLS wire references, including same-tag recreation. The nine-path UDP user/domain test does not observe physical reuse or exact wire bytes in that same workload; UDP/all-relay wire boundaries and domain history across replacement remain unverified. |

## OT Matrix

Test names below refer to the source indexes at the end of this document.

| ID | Status | Actual tests and assertions | Material remaining scope |
|---|---|---|---|
| OT-01 | Partial | Existing config/JSON/default-duration tests plus `TestAnyTLSOutboundNativeValidationMatrix`: 33 invalid native AddOutbound cases cover account/address/port/minIdle, sender/security/network/mux and dynamic origin/srcip; each rejection leaves the full List registry unchanged and original Alice traffic succeeds. `TestAnyTLSOutboundJSONIdleIntervalBoundaries` builds full outbound config for 22 cases across both timer fields: omitted/zero/minimum/5/6/30/MaxUint32 accepted with exact fields, negative/overflow/fraction/string rejected. | Parameter rejection and JSON interval boundaries are now covered through their actual APIs. Rejected newly constructed instance workers/timers are not inventoried; the JSON boundary test is config construction, not wall-clock execution of every accepted interval. Duplicate ownership behavior is OT-08, not proof of zero rejected-instance resources. |
| OT-02 | Partial | `TestAnyTLSExternalServers`: both independent server binaries, verified CA/SNI, custom padding, exact random TCP 1/8192/65535/65536/1 MiB, IPv4 UDP endpoint/payload checks, barrier-started 32x16 TCP, server stop and same-handler recovery. `TestAnyTLSExternalControlPairs`: independent clients in both directions, TCP/UDP and stopped-server failure. Core fixtures exercise native outbound. | External physical reuse/idle counts are absent; external failures are server stop/restart, not the full TLS/auth/reset/malformed matrix. External final servers are not crossed with chain topologies. Reverse external-client -> Core inbound -> Core outbound composition is absent. |
| OT-03 | Partial | ClientSequentialReuseAndContextIsolation performs 100 exact exchanges/one dial; ConcurrentStreams performs 32x16 exchanges, external concurrency has start barrier. Direct OB resource counts exist. New ChainLayerCounts observes actual native inner/outer AnyTLS: 3 sequential, recreate inner, 3 sequential, 4 held concurrent and 1 final sequential, with exact accepted/session/stream/dispatch counts and joined shutdown. | Unit 100/32x16 fixture bypasses TLS/proxyman; new real chain count workload is smaller and limited to two AnyTLS layers. No final business destination sockets (inner peer echoes inline), other-relay/deeper layer counts, recorded random seed, or sequential reuse-after-peer-idle-expiry oracle. |
| OT-04 | Covered | ClientSequentialReuseAndContextIsolation checks detached metadata and canceled completed streams. `TestAnyTLSOutboundCanceledUserThenReusedPool` authenticates real Alice/Bob ingress clients, joins Alice disconnect, then serves Bob with independent exact user totals while the real system-dial count stays one: direct and both AnyTLS entrances. ChainLayerCounts also cancels authenticated Alice's request before Bob and observes unchanged physical counts at both layers. ProbeTimeoutPreservesBusiness separately covers probe cancellation isolation. | Scope is completed-response cancellation/disconnect followed by a new user on direct/two-AnyTLS paths, not every relay or cancellation phase. Native lifecycle phase gaps remain OT-12. |
| OT-05 | Covered | `TestClientIdleEvictionAndMinimum`: minimum 0/1/3, no pre-dial, aged idle eviction, active exclusion, retirement overrides retention, returned retired session closes, sessions/timer cleared. `TestClientDefaultIdleTimer`: strict-only real 30s/30s expiry, lower bound 30s and upper bound 65s. `TestClientIdleDuration` tests adapter conversion. | Scope is engine pool state plus adapter duration conversion, not a cross-engine idle test. Strict execution is separately documented; a default skipped timer test is not evidence. |
| OT-06 | Partial | `TestClientUDPPacketBoundaries` asserts IPv4/IPv6/domain addresses and both adapter directions for 0/1/8192/8193 using net.Pipe, with explicit size errors. `TestAnyTLSOutboundAddressIPv6` uses real ::1 transport and two UDP sockets in one association with exact endpoint/1/8192 bytes; DNS test preserves domain endpoint; external servers assert IPv4 endpoint and bytes. New UDPUserDomainAccounting checks domain reply identity via exchangeUoT across nine paths and multiple targets per association. | Original RoutingStatsAndChains still discards returned endpoint; new helper checks Fqdn/payload, not reply port. Real unsupported-size test is inbound-only; no remote oversized/zero response through complete native outbound plus clean later recovery. No invalid-size/UDP-no-direct-fallback integration matrix for all chains. |
| OT-07 | Partial | `TestAnyTLSOutboundFailureRecovery`: bad password/CA/SNI and route-reference failures, <=5s failure, good replacement recovery. External stop/restart; soak backend RST/recovery; client/probe write-phase barriers. New `TestAnyTLSOutboundChainFaultReceipts` cuts real relay transports after backend receives held ID, requires before=1/held=1/down=0/after=1 across eight relay paths and same-handler restart recovery. | No outbound malformed-frame injection/recovery or deliberate refused business destination matrix. Receipt oracle is established TCP complete-ID delivery, not partial-record uncertainty, faulted auth/handshake or UDP replay. Snapshot follows 100ms sleep, not a full quiescence barrier; early timeout recovery and per-failure zero-resource coverage remain incomplete. |
| OT-08 | Partial | ControlPlaneValidation enumerates/decodes/rejects/recreates via real gRPC. ChainedReplacementDrains retains old flow across wrong-password/good replacements and relay restart. **New ShutdownOwnsDrainingNativePool** asserts real duplicate AddOutbound error and original handler identity, old draining plus working replacement, full instance shutdown closing old flow without timeout. | Duplicate rejection behavior and native draining shutdown now covered for direct/two AnyTLS chains. Rejected freshly created instance's internal resources are not inventoried; stale native handler admission is client-unit-only. Complete old/new socket/timer inventory still absent. |
| OT-09 | Partial | RoutingStatsAndChains: TCP Alice 31/33, Bob 27/29, four exact domain pairs, eleven paths. New UDPUserDomainAccounting: nine paths, two synchronized users, two domains/association, three rounds, unequal 1/8192 versus 37/512 payloads and remote aggregation. New ShutdownOwnsDrainingNativePool checks wire counters do not fall after same-tag replacement and increase after new traffic, with exact user 11/13 bytes. | UDP identity/domain and same-tag cumulative continuity gaps now covered in source. Physical reuse is unobserved in stats workloads; >32 or monotonic wire counts are not exact same-wrapper references and cannot reject double counting. UDP bucket publication waits for exact contents. |
| OT-10 | Partial | `TestAnyTLSOutboundStatisticsSwitches`: all 16 inbound/outbound/user/domain combinations, exact user/domain TCP counts, presence/absence of tag counts. Sidecar `TestAnyTLSOutboundRuntimeDisplay`: reflected endpoint/email/protocol and no password in constructed public metadata. Sidecar traffic-switch/snapshot/restart and domain aggregation tests cover existing consumers. | Sidecar runtime-display test is a synthetic TypedMessage, not live outbound control API -> actual status endpoint. Sidecar traffic/snapshot fixtures are inbound -> freedom, not native outbound UDP or tag recreation. No native-outbound end-to-end consumer assertion spanning counters, domain paging/sequence and complete credential-free status response. |
| OT-11 | Partial | Native direct/chained routes, address tests, real OB, actual Sidecar UDS, strategy selection, timeout cleanup and replacement tests exist. | Not the full B/C composition. See each B/C row and chain cross-product below; individually passing components do not close this umbrella row. |
| OT-12 | Partial | LifecycleAtWriteBarriers and LateDialCannotPublishAfterRetirement assert unit joins/zero state/late socket close. ShutdownOwnsDrainingNativePool proves native draining-old/new shutdown. New ChainLayerCounts matches both AnyTLS layers' session/stream closes, dispatch completion and peer socket-map emptiness after native shutdown, joining relay copies without success-path forced socket close. Healthy resource rounds supplement these. | Actual gRPC delete during TLS/open/return and replacement survival after old-only cleanup remain. New two-layer healthy shutdown is not complete fault-phase/deeper/all-protocol worker/timer inventory; sequential engine state tests do not exercise every publication race. |
| OT-13 | Covered | ExternalClients, VLESSAfterAnyTLSRemoval, inbound/shared regressions and documented whole-Core/Sidecar runs remain. New strict runner's external group requires six named subtests for both inbound clients, both outbound servers and both independent control pairs; rejects every skip/fail, absent required pass, missing package pass and nonzero process exit. Parent reports external group PASS. | Scope is regression and selected external gate. Direct invocation of ExternalClients still permits skips; use runner for strict acceptance. Selected external PASS does not mean address/idle/soak groups or every behavioral OT/B/C requirement ran; idle is pending at this snapshot. |
| OT-14 | Covered | Implementation record documents approved wrapper, dual-platform build, Darwin version/config test, format/vet/race gates. Read-only `go version -m` during this audit confirms both build artifacts are af1e18eef94f..., Go 1.26.1, vcs.modified=false; Darwin arm64 and Linux amd64. | No new execution claimed. Linux arm64 container test evidence is not execution of the amd64 release binary; OT-14's cross-build gate must not be described as that runtime test. Other OT gaps are not waived by successful builds. |

## Burst Observatory Matrix

| ID | Status | Actual tests and assertions | Material remaining scope |
|---|---|---|---|
| B-01 | Covered | `TestAnyTLSOutboundObservatoryUDS`: real HealthPing/tagged dispatch/native AnyTLS/UDS, GET/HEAD, 204 alive, 200/302/404/503/timeout dead, actual Location and zero redirect-target requests. | This row is HTTP semantics, not actual Sidecar egress semantics; those remain B-03. |
| B-02 | Covered | `TestAnyTLSOutboundObservedBalancers`: registered observer with two groups, default URL and longer-prefix override defeating wrong shorter/default routes, exact escaped RequestURI, 12 requests, one group's failure leaves other group's protobuf result unchanged. | Direct native AnyTLS only; shared-chain composition belongs to C-07. |
| B-03 | Partial | Core `TestAnyTLSOutboundObservatoryUDS`; Sidecar `TestAnyTLSOutboundEgressUDS` calls real generate204Handler/EgressGuards over UDS after AnyTLS, controlled local ASN provider, default/healthy/mismatch/missing names, GET/HEAD and both endpoint forms. | Both use `must-not-resolve.invalid:8080`, not virtual 127.0.0.1:8080 plus an armed local trap with zero-hit oracle. This usefully prevents hostname bypass but does not verify locality of identical loopback destinations. No need to touch production 8080 to implement an isolated trap. |
| B-04 | Covered | `TestAnyTLSOutboundOBResourceCounts`: four keepAlive/endpoint combinations, exact HTTP/logical/session/accepted/closed counts; three HTTP requests share 1 or 3 streams, three real Check calls add three streams, all reuse one TLS session; HTTP idle-close preserves pool, shutdown joins dispatch/peer workers and closes resources. | Counts are for a direct path, not each layer of a chain or concurrent business workload. They do not satisfy C-03. |
| B-05 | Partial | UDS test exercises windows 1/2/3/20, first success, immediate failure and min(3,window) recovery. `TestContractHealthRecoveryCacheMatrix` has empty/FSF/recovery/stale pure-state cases. ObservedBalancers reads actual observation results and expiry for window 20. | Interrupted success streak and stale/empty matrix for each window are not all asserted through the real observer API; UDS tests read HealthPing.Results directly. Pure-state correctness is not the requested full real-API matrix. |
| B-06 | Partial | `TestAnyTLSOutboundProbeTimeoutCancelsDial`, `...CancelsTLS`, `...CancelsAuthWrite` observe dial exit, silent TLS socket close and blocked application-write exit. PreservesBusiness covers HTTP-response wait for direct/AnyTLS entrances. New `TestAnyTLSOutboundChainProbeTimeoutIsolation` extends HTTP stalls to SOCKS/VLESS/Hy2 x both entrances with concurrent Alice/Bob, healthy second probe, exact user counters, dispatch/HTTP joins and fresh recovery. | HTTP-response isolation now spans supported relay types in source. Concurrent business is not crossed with dial/TLS/auth stalls at each relay, or external finals. Early-phase same-pool recovery/resource-round matrix incomplete. Dial/auth use hooks, not real network blackholes. |
| B-07 | Partial | `TestAnyTLSOutboundObserverStopRestart` uses real 3s/2s/20 scheduling, remote entered barrier, bounded Close, HTTP active-zero, no canceled sample and successful restart. `...ConnectivitySuppression` separately asserts network-down creates no sample and exact target/connectivity request counts. Shared scheduler contract tests cover cancellation races. | Native stop is exercised during HTTP-response wait, not pending dial/TLS/auth; no complete native observer/pool worker inventory across repeated stop/start generations. Broader fault phases cannot be inferred from the one phase. |
| B-08 | Covered | `TestAnyTLSOutboundObservedBalancers`: actual observation drives leastPing/weightedLeastPing, alive high RTT, MaxRTT/tolerance, 3:2 rotation and all-dead fallback. `...ProbeTimeoutPreservesBusiness` exact Alice counters exclude probes. Existing classic `TestObserverUpdateStatusPrunesStaleOutbounds` / `...ClearsWhenNoOutboundsRemain` remain regression tests. | Does not claim a classic-observer -> native AnyTLS integration run or combined chained multi-user balancer workload. Those exceed these individual assertions and remain C-07 composition work. |

## Chain Matrix

| ID | Status | Actual tests and assertions | Material remaining scope |
|---|---|---|---|
| C-01 | Partial | RoutingStatsAndChains has A/B markers and client/hop byte counters. New `...ChainFaultReceipts` cuts real SOCKS/VLESS/AnyTLS TCP gates or Hy2 UDP gate for both entrances; final server/backend remain live and down-ID count must stay zero, providing a TCP bypass trap. | Per-hop physical/logical receipt logs remain missing; no negative UDP-business bypass test. New receipt counts cover a particular middle-hop transfer interruption, not every phase or deeper successful topology. |
| C-02 | Partial | `...AddressDNSLocality` has relay.address.test/server.address.test queries, no payload.address.test queries, remote TCP/UDP domain-only routes, SOCKS domain guard for proxySettings versus IP guard for dialerProxy, independent verified SNI. `...AddressIPv6` real ::1 transport/TCP/two UDP endpoints. `...AddressStaticBinding` observes source, rejects mismatched source family, checks kernel interface. JSON and gRPC reject origin/srcip. | DNS role tests cover direct/SOCKS; binding test covers direct only, not the physical relay socket in other chains. No external-final-server/address cross-product. **Dynamic origin/srcip is intentionally rejected and tested, not missing runtime support.** Linux alias coverage is separately documented; macOS default skips unavailable 127.0.0.2 and strict fails. |
| C-03 | Partial | **New TestAnyTLSOutboundChainLayerCounts covers two native AnyTLS outbound layers through both entrances**, using real TLS peers, independent listener accepts, engine session/stream callbacks and completion-only native Dispatch aliases. It proves inner reuse, outer reuse after inner-pool recreation, four concurrently held streams, exact totals inner physical/logical=5/11 and outer=4/5, and complete observed layer shutdown. | Not all-layer coverage for SOCKS/VLESS/Hy2/external or deeper chains. The final peer echoes inline: no separate business destination-socket oracle. Counted requests use tagged dialing, not authenticated Alice/Bob, so first-business-user cancellation and distinct-user identity/accounting over observed reused layers remain missing. |
| C-04 | Partial | ChainedReplacementDrains covers both AnyTLS entrances: old flow survives top/relay removal, wrong-password replacement fails, good replacement and relay restoration succeed, old retirement completes after close. New ShutdownOwnsDrainingNativePool additionally covers native old-active/new-replacement full shutdown for direct/two AnyTLS chains. | No equivalent replacement/retirement matrix for SOCKS/VLESS/Hy2/external finals; relay credentials unchanged. No post-old-drain request against a still-live replacement to detect old-only cleanup affecting it. Whole-instance close intentionally closes both and cannot prove that separate isolation property. |
| C-05 | Partial | FailureRecovery covers missing/self/two-tag and mixed PD/DP/six three-tag cycles with bounded failure/replacement recovery. New ChainFaultReceipts covers real transfer cuts/restart for every supported relay x both entrances, same top handler, held-ID received exactly once and down-ID never received at snapshot. External stop/restart and soak RST add distinct failures. | Relay dial/handshake interruption matrix, malformed frames, partial-payload and UDP replay remain. Receipt snapshot uses 100ms delay, not quiescent final accounting. No simultaneous proxySettings/dialerProxy precedence test with competing markers/traps. Cycle failure lacks owned-zero oracle. |
| C-06 | Partial | RoutingStatsAndChains exercises direct/freedom/SOCKS/VLESS/AnyTLS/Hy2, both entrances, small TCP and 1/512/8192 UDP bytes. New UDPUserDomainAccounting additionally checks domain identities and exact logical accounting over nine paths. Address tests verify IPv6 endpoints; external direct suite verifies IPv4 endpoint. | Domain helper checks Fqdn but not reply port; original all-chain UDP still discards `from`. No negative UDP bypass trap, especially Hy2. Full TCP size/close semantics per topology, external entrance + Core outbound, and chained external finals absent. Wire-layer accounting boundary not exactly measured. |
| C-07 | Partial | ProbeTimeoutPreservesBusiness covers direct/AnyTLS entrances. New ChainProbeTimeoutIsolation covers SOCKS/VLESS/Hy2 x both entrances: Alice/Bob during a stalled probe plus healthy probe, exact ob URI/user bytes, failed then recovered health, joined dispatch/HTTP, probe-context cancellation and full Core shutdown, four HTTP receipts. Direct soak combines TCP/UDP/OB/retirement. | New fixture uses two HealthPing objects and HTTP TCP target, not registered multi-group observer/strategy selection or actual Sidecar UDS. Cancellation occurs after probes join, not scheduler Stop while stalled. Full UDS + balancer + shutdown composition and external-final-server paths remain absent. |

### Topology Cross-Product

| Dimension | Status | Actual scope and missing combinations |
|---|---|---|
| Core final, two entrances | Covered | RoutingStatsAndChains executes SOCKS/VLESS/AnyTLS/Hy2, plus freedom and direct baseline. Coverage is small TCP and UDP payload echo, not all C assertions. |
| sing-box/Mihomo final with intermediate relay | Missing | ExternalServers calls outboundFixture with empty chain mode; independent controls also contain no Core relay chain. Direct interoperability is not this row. |
| External AnyTLS client -> Core inbound -> native Core outbound | Missing | ExternalClients uses newFixture with freedom exits. ExternalServers uses the vendored engine client or core.Dial. No single path combines both independent entrance and native onward outbound. |
| Separate Core processes per relay role | Missing | Existing multi-hop fixtures are serial one-instance listeners/handlers, avoiding concurrent global reinitialization but not implementing the spec's process-isolated topology. |

## Focused Accounting And Resource Oracles

| Subrequirement | Status | Evidence / missing assertion |
|---|---|---|
| Native outbound TCP unequal Alice/Bob + alpha/beta totals | Covered | RoutingStatsAndChains asserts user totals 31/33 and 27/29 and four exact domain pairs: 16/17, 15/16, 14/15, 13/14. |
| Native outbound UDP unequal two-user/two-domain totals | Covered | New UDPUserDomainAccounting queries both directions: Alice=24579, Bob=1647, remote=26226. Domain pairs are Alice alpha=3/beta=24576, Bob alpha=111/beta=1536, remote alpha=114/beta=26112 (same up/down). Reflect.DeepEqual of accumulated selected-user buckets rejects missing/extra domains; nine paths, multiple targets in each association. |
| Remote account does not inherit local UDP identities | Covered | Same new test requires exact remote user and domain aggregation as well as separate local Alice/Bob totals. It tests configured remote identity, not physical-session reuse. |
| Actual shared physical reuse during exact stats workload | Covered | ChainLayerCounts authenticates Alice then Bob through real ingress, cancels Alice before Bob, asserts exact independent user totals and unchanged inner/outer physical counts. CanceledUserThenReusedPool additionally covers direct and both AnyTLS chains with a system-dial counter. Scope is TCP; the separate nine-path UDP/domain workload has no physical-count oracle. |
| Exact wire accounting boundary / no double counting | Covered | ChainLayerCounts measures peer plaintext reads/writes at each TLS boundary and requires exact client/hop gRPC up/down equality over three stable samples, including after same-tag recreation. Includes AnyTLS authentication/padding/framing, excludes that layer's TLS overhead; outer payload legitimately contains inner TLS records. Scope is two-layer TCP through both entrances, not UDP or every relay protocol. |
| Same-tag replacement preserves cumulative stats | Covered | New ShutdownOwnsDrainingNativePool snapshots positive outbound client up/down values, requires neither fall immediately after recreation and both rise after replacement traffic; exact Alice 11/13 logical totals. Direct plus both AnyTLS entrances. This proves continuity, not exact wire-byte correctness or domain history across recreation. |
| Two-AnyTLS-layer sequential/concurrent physical/logical counts | Covered | ChainLayerCounts asserts inner/outer accepts, authenticated sessions, streams, closes and native Dispatch completion for both entrances. Includes two authenticated-user streams. After inner recreation outer remains one physical session with two logical tunnels; four held concurrent flows yield totals inner 5/12 and outer 4/5, then final sequential yields inner 5/13. Shutdown matches all observed closes and joins relay/peer workers. |
| Other-relay/deeper layer counts and final destination sockets | Missing | New counter fixture is specifically two AnyTLS layers with inline inner echo. SOCKS/VLESS/Hy2/external/deeper paths and final business socket counts are not instrumented by it. |
| Outbound malformed-server-frame recovery | Partial | TestClientMalformedResponseDoesNotReplay injects truncated header/body after the peer receives eight business bytes, requires bounded failure, one dial/receipt, joined peer workers, and a fresh independent request succeeding on a second connection. Adapter/engine over net.Pipe is covered; real TLS/Core/chained malformed peers remain untested. |
| No-replay after established relay transfer interruption | Partial | New ChainFaultReceipts records before/held/down/after IDs, eight paths, requires 1/1/0/1 after restart. Held ID is fully received before cut, and final snapshot uses a 100ms wait. Stronger than echo-only recovery, but not a quiescent oracle for arbitrary partial delivery or faults at every phase. |
| No-replay after partial-record delivery / malformed peer / UDP failure | Partial | ClientMalformedResponseDoesNotReplay now asserts one physical attempt and one delivered-business receipt after response header/body truncation. This is not a partially delivered application request, native TLS/chained fault-phase matrix or UDP retry oracle. |
| Healthy client completed-round ownership and retained heap | Covered | New TestClientCompletedResourceRounds executes 24 rounds x 8 workers x 8 streams with exact 32768-byte echoes. Each round explicitly Close/joins, checks owned physical socket count and pending/active maps zero plus client.done. Parent samples after fixture peer joins and two GCs; fourth round is baseline, later heap capped at baseline+8 MiB. Heap failure persists a pprof via os.CreateTemp outside automatic cleanup. |
| Warmed retained heap/FD/goroutine bounds for repeated native outbound faults/chains | Partial | Healthy adapter rounds now assert heap and ownership, not just logging. They use supplied clientTestDialer without native TLS/proxyman/gRPC/chain and inject no faults; no FD/goroutine deltas there. Soak's native samples remain log-only; inbound resource rounds cannot close native chained failure scope. |
| Native physical/stream resource closure for direct OB | Covered | OBResourceCounts joins peer and Dispatch completions, sessions/closedSessions and streams/closedStreams match, owned TLS socket map empty. Scope is that fixture, not every Core background worker or every relay layer. |
| Ten-minute TCP/UDP/OB + retirement/RST workload | Covered | OutboundSoak enforces >=10m only with ANYTLS_SOAK_STRICT=1, records transaction errors without retry/allowance, verifies nonzero coverage and resets==cycles, joins owned workloads/backends. Implementation record retains failed first run and successful isolated rerun. |
| All outbound pool sockets/copy workers/timers zero after lifecycle/fault rounds | Partial | Lifecycle barriers, late dial, healthy client rounds, direct OB and soak give scoped ownership checks. New ChainLayerCounts joins both counted TLS peer workers, outer relay copy directions and native dispatches, checks socket maps empty and all sessions/streams closed after native instance shutdown without forced success-path cleanup. |

The preceding resource row remains Partial: explicit counted healthy two-AnyTLS
shutdown is now present, but not the full native fault-phase/deeper/all-protocol
pool/timer inventory. Observed peer/dispatch completion is not a count of every
internal transport worker or timer.

| Subrequirement (continued) | Status | Evidence / missing assertion |
|---|---|---|
| Late successful dial after client retirement | Covered | TestClientLateDialCannotPublishAfterRetirement releases a context-ignoring dial only after Retire, requires Process error/drain/socket close and zero authentication writes. No TLS/Core manager/chain involved; do not conflate with native gRPC late publication. |
| Client auth/SYN/data write barriers x Retire/Close | Covered | TestClientLifecycleAtWriteBarriers exercises six combinations; Retire preserves admitted data until Close, cancels unfinished auth/SYN admission, then joins Process and asserts empty active/pending sets. |
| Real native shutdown of still-draining removed handler | Covered | New ShutdownOwnsDrainingNativePool keeps old stream open, verifies retired signal not done, serves replacement request, closes instance within 5s, requires old retirement done and old stream error that is not a read timeout. Direct/two AnyTLS chains. |
| Real gRPC duplicate outbound rejection preserves original | Covered | Same new test requires AddOutbound error and manager.GetHandler("client") pointer unchanged while original flow is active. It does not count rejected new instance's hidden workers/timers. |
| SYN before PSH/FIN, blocked-reader wake on remote close | Partial | Reserved/queued-control state tests and SlowReaderBackpressureAndClose cover internal invariants/local close. No recorded outbound wire-order oracle and explicit parked outbound reader + remote close matrix. |
| TCP close and read/write shape matrix | Partial | ClientGracefulEOFPreservesQueuedResponse preserves a 50KB reply; random-size echo and integration A/B banner exercise basic shapes. Reply handler reads five bytes then replies, not a barrier-controlled server waiting for upload EOF before final response. No complete inverse fragmentation/remote FIN with unread buffers matrix across native/external/chained paths. |

## Harness And Execution Gaps

| Gate | Status | Actual evidence and limit |
|---|---|---|
| Loopback/temp certificates, native configs, no public dependency | Covered | Reviewed outbound fixtures use temporary loopback/UDS/CA and local provider fixtures. This is not permission to use production listeners or modify interfaces. |
| Strict capability/executable enforcement | Covered | New runner sets ANYTLS_STRICT=1, ANYTLS_SOAK_STRICT=1 and 10m soak duration; external group requires both executable selectors, resolves paths and reads version output. Every skip/fail in selected group's JSON events is rejected, including nested capability skips. This closes permissive inbound skipping at runner level, not when tests are invoked directly. |
| Nonzero selected test counts and both external directions | Covered | Runner GROUPS enumerate exact required tests; external requires all six directional subtests. validate_events also demands package pass and exit zero. EvidenceTest tests empty selection, missing required external direction, child skip/fail and process/package failure. Coverage is each explicitly selected group, not automatic execution of every group or native OT/B/C test. |
| Reproducible artifacts: binary hash/revision, redacted configs, JSON, exit/cleanup | Partial | Runner retains SHA256 and version of both external executables, Core revision/worktree and Go version, exact commands, per-group JSONL/stderr/exit/errors and report.json in a new external evidence directory. New client rounds retain heap-failure pprof. External fixture configs remain auto-deleted t.TempDir files; a persistent redacted config bundle and explicit fixture-cleanup result inventory are still absent. |
| Deadline/readiness and join on every fixture | Partial | Address/OB-count/soak fixtures explicitly join owned workers. External helpers poll listener readiness and controls perform forwarding readiness; process stop waits are not all bounded. Base echoTCP/echoUDP helpers close listeners but do not join accepted workers. Do not infer universal cleanup from newer stronger fixtures. |
| Barrier races repeated 100x with recorded seed | Partial | Lifecycle and blocked-write barriers exist; implementation record lists 100x selected client/engine/manager/OB-count runs. Concurrent random workload has no recorded seed; repeating sequential state manipulation does not create missing retirement phase races. |
| Full regression and release evidence | Partial | Full Core race at 330cd213 passed after the timer oracle correction; historical failures remain in the implementation record with the differential explanation. Latest isolated Sidecar candidate race passed. New acceptance tests added after that run need separate execution. Local dual-platform builds and existing strict opt-in results do not close the remaining behavioral coverage gaps. |

## Runtime Contract Cross-Check

This maps the additional O requirements in outbound.md without treating the OT
table as a substitute for their lifecycle/accounting semantics.

| Contract | Status | Mapping and residual |
|---|---|---|
| O-01 TCP/UoT, payload/endpoint/domain, no UDP fallback | Partial | OT-06/C-06: payload/adapter limits covered; all-chain endpoint and negative no-bypass matrix missing. |
| O-02 Pool per handler, reuse, concurrent independence, idle policy | Partial | OT-03/04/05/C-03/C-04: direct reuse, engine policy, two-AnyTLS-layer counts and authenticated Alice cancellation then Bob reuse now covered. Other/deeper counted topologies and complete replacement-isolation scope remain incomplete. |
| O-03 Handler-owned pool, detached metadata, cancel/Close joins | Partial | OT-04/12: unit metadata/cancel/close and probe isolation covered; phase-complete native/chain ownership remains. |
| O-04 Preserve selection, cleanup/growth without invented capacity | Partial | Engine selection-state tests and new healthy client resource rounds cover real owned connection closure/retained heap. Native TLS/proxyman/chained repeated-fault growth assertions absent. No test proves a hard pool cap, and none should claim one. |
| O-05 Bounded retries, no replay, broken-peer recovery | Partial | OT-07/C-05: bounded failures/recovery, established-transfer ID receipts and adapter malformed-response no-replay/fresh recovery exist. Native malformed/partial-request/UDP fault phases remain; the historical Hy2 deadline anomaly is unresolved despite the completed eight-default-case race PASS. |
| O-06 Logical versus wire accounting and identity | Partial | Exact TCP/UDP with remote aggregation, probe exclusion and same-tag cumulative continuity covered. Two-layer TCP now proves physical reuse during authenticated-user accounting and exact post-TLS wire bytes. UDP same-workload physical/wire counts and broader relay boundaries remain absent. |
| O-07 gRPC lifecycle, retirement, no old/new pool coupling | Partial | Established-flow retirement, duplicate rejection preserving original and native shutdown of draining old pool now covered. Rejected-instance resource inventory, remaining phase races and replacement survival after old-only cleanup incomplete. |
| O-08 Existing routing/OB/balancers and Sidecar safe metadata | Partial | B-01/02/04/08 and Sidecar reflection checks useful; live outbound consumer/accounting and full C-07 composition remain. |

## Source Index

- [Native authentication-write retirement](../../proxy/anytls/outbound_retirement_auth_test.go)
- [Native UDP response boundary matrix](../../proxy/anytls/outbound_udp_response_test.go)
- [Native TLS admission retirement](../../proxy/anytls/outbound_retirement_phases_test.go)
- [Redacted persistent fixture evidence](../../proxy/anytls/evidence_test.go)
- [Independent Core processes and restart receipts](../../proxy/anytls/outbound_process_test.go)
- [Native UDP oversize/no-bypass matrix](../../proxy/anytls/outbound_udp_boundaries_test.go)
- [Rejected handler construction cleanup](../../core/outbound_cleanup_test.go)
- [Timeout wrapper cancellation regression](../../common/buf/timeout_interrupt_test.go)
- [Native integration, config APIs, TCP counters and chains](../../proxy/anytls/outbound_integration_test.go)
- [Native chained removal/replacement](../../proxy/anytls/outbound_lifecycle_test.go)
- [New UDP user/domain/remote-account accounting](../../proxy/anytls/outbound_udp_stats_test.go), [new client lifecycle barriers](../../proxy/anytls/client_lifecycle_edges_test.go)
- [New healthy client resource rounds](../../proxy/anytls/client_resource_rounds_test.go)
- [New native shutdown, duplicate rejection and cumulative counters](../../proxy/anytls/outbound_shutdown_test.go)
- [New two-AnyTLS-layer counts and shutdown](../../proxy/anytls/outbound_chain_counts_test.go)
- [Authenticated canceled-user actual reuse](../../proxy/anytls/outbound_user_reuse_test.go)
- [Malformed response no-replay and fresh recovery](../../proxy/anytls/client_malformed_test.go)
- [Native gRPC validation and JSON interval matrices](../../proxy/anytls/outbound_validation_matrix_test.go)
- [Strict selected-group acceptance runner](../../testing/anytls_outbound_acceptance.py), [runner event-validator tests](../../testing/anytls_outbound_acceptance_test.py)
- [New real relay cuts and backend receipts](../../proxy/anytls/outbound_chain_fault_test.go), [new chained concurrent-user/probe isolation](../../proxy/anytls/outbound_chain_probe_test.go)
- [Adapter client lifecycle/reuse](../../proxy/anytls/client_test.go), [native config](../../proxy/anytls/client_config_test.go), [JSON config](../../infra/conf/anytls_client_test.go)
- [Engine pool/state/time tests](../../proxy/anytls/internal/engine/client_pool_test.go), [engine lifecycle](../../proxy/anytls/internal/engine/lifecycle_test.go)
- [UDP adapter boundaries](../../proxy/anytls/client_udp_test.go), [inbound UDP accounting/resources](../../proxy/anytls/udp_resources_test.go), [inbound acceptance](../../proxy/anytls/acceptance_test.go)
- [Address, DNS, binding](../../proxy/anytls/outbound_address_test.go)
- [External servers](../../proxy/anytls/outbound_external_test.go), [independent control pairs and process helpers](../../proxy/anytls/outbound_external_controls_test.go), [external inbound clients](../../proxy/anytls/clients_test.go)
- [Independent clients through native outbound](../../proxy/anytls/outbound_external_clients_test.go): sing-box/Mihomo, verified TLS and trust rejection, exact TCP/UDP payload and endpoint, user counters, native-handler removal no-bypass; separate Core processes remain untested here.
- [HTTP/UDS](../../proxy/anytls/outbound_ob_test.go), [OB resource counts](../../proxy/anytls/outbound_ob_counts_test.go), [balancers/stop/connectivity](../../proxy/anytls/outbound_balancer_test.go), [probe phase cancellation](../../proxy/anytls/outbound_timeout_test.go)
- [Native soak](../../proxy/anytls/outbound_soak_test.go), [UDP differential diagnostics](../../proxy/anytls/outbound_soak_udp_test.go), [inbound resource rounds](../../proxy/anytls/resources_test.go)
- [Manager fake-handler retirement](../../app/proxyman/outbound/retirement_test.go), [burst contract tests](../../app/observatory/burst/contracts_http_test.go)
- [Candidate Sidecar outbound display/egress](../../../xray-sidecar/anytls_outbound_test.go), [Sidecar inbound traffic/restart](../../../xray-sidecar/anytls_traffic_restart_test.go), [consumer domain aggregation](../../../xray-sidecar/domain_traffic_test.go)
