# AnyTLS Outbound Verification

Status: locally accepted; see [final mapping](outbound-final-acceptance.md) and
[chronological evidence](outbound-implementation.md).
This is the detailed acceptance procedure for
[outbound.md](outbound.md). Historical inbound PASS is not outbound evidence.

## Reference methods

- sing-box's pinned sing-anytls dependency has test/scenario_test.go (exact echo,
  reuse counted through DialOut, external-engine interoperability), stress_test.go
  (32 workers x 16 exchanges, aborted streams, concurrent readers), and wire_test.go
  (SYN ordering, blocked-write shutdown, pending-read wakeup and peer FIN draining).
- Mihomo listener/inbound/anytls_test.go constructs real client/server adapters,
  verifies TLS and delegates sequential/concurrent HTTP work to common_test.go.
  Borrow its fixture structure, not unrelated ShadowTLS/Restls/JLS feature scope.
- Core proxy/anytls/integration_test.go already queries real HandlerService and
  StatsService and checks exact per-user/domain bytes. clients_test.go runs external
  binaries; extend that approach in the reverse direction. Existing burst HTTP
  contract tests substitute tagged.Dialer: useful unit coverage, not an end-to-end
  AnyTLS acceptance substitute.

## Harness and evidence

Run isolated Core processes for multi-hop tests: Core has process-global dialer
state. Allocate loopback ports, temporary CA/server certificates and directories.
Use reserved .test names with a controlled resolver. Keep production 8080 and
LaunchAgents untouched. A virtual 8080 destination may route to a temporary UDS.

Retain ANYTLS_SINGBOX and ANYTLS_MIHOMO executable selectors. Add an explicit strict
acceptance mode: both executables and required network capabilities must exist,
otherwise fail acceptance rather than skip. Normal developer runs may skip external
tests with a clear reason. Record executable hashes/revisions, generated redacted
configs, commands, JSON results, exit codes and cleanup results. Never commit binaries
or real credentials. New test names below are planned, not existing runnable tests.

Use readiness handshakes rather than arbitrary startup sleeps. Each fixture has a
deadline and cleanup that waits for child exit, closes listeners and joins owned
workers. Failure retains diagnostics in an ignored artifact directory. Do not run
process-global hook tests concurrently with unrelated fixtures.

## Interoperability matrix (OT-02, OT-13)

| Client | Server | Required coverage |
|---|---|---|
| Core | Core | Full configuration, control, routing, stats, OB and chains |
| Core | sing-box | TCP/UDP, TLS, reuse, concurrency, failure/recovery |
| Core | Mihomo | TCP/UDP, TLS, reuse, concurrency, failure/recovery |
| sing-box | Core | Existing inbound plus authentication, revocation, routing and stats regression |
| Mihomo | Core | Existing inbound plus authentication, revocation, routing and stats regression |
| sing-box | Mihomo | Basic TCP/UDP fixture control |
| Mihomo | sing-box | Basic TCP/UDP fixture control |

Use real processes and native configs, not only Core's vendored client connecting
to its own server. A failed control group invalidates the fixture, not the product.
Keep certificate verification enabled; test wrong CA/SNI separately. Include custom
padding and large writes, so matching implementation mistakes cannot pass loopback.

## Core acceptance methods

| ID | Procedure and oracle |
|---|---|
| OT-01 | Table-drive JSON and protobuf creation with valid defaults, null/mistyped account, missing address/password, bad port, unsupported transport/security/mux and invalid timers. Compare rejection and proto round-trip. Real gRPC rejects invalid add without registering a tag or leaking resources. |
| OT-03 | 100 sequential exact echo exchanges, waiting for upload write completion and prior stream release; healthy local fixture must dial once. Then 32 synchronized workers x 16 exchanges with recorded seed and sizes. Count physical sessions separately from logical streams and destination sockets. Do not demand one socket for concurrent streams. |
| OT-04 | User A completes/cancels a request; user B uses the pool afterward. Assert B response and identity remain correct and A cancellation does not terminate B. Test both direct and chained dialing. |
| OT-05 | Controlled cleanup times prove idle expiry, active-session preservation, minimum idle retention without pre-dial, and retirement overriding retention. Also run default 30s/30s against real time with a bounded scheduling allowance. Do not change production timing to make tests pass. |
| OT-06 | UDP echo to IPv4, IPv6 and domain targets, multiple destinations in one association, payloads 1/8192/8193 and zero bytes. Supported packets must match exactly with correct reply endpoint; unsupported ones fail explicitly. This is Core's current adapter limit, not the AnyTLS wire maximum. |
| OT-07 | Wrong credentials/CA/SNI, refused destination, RST, silent peer, blocked writes and malformed frames. Require bounded failure and a successful later fresh request. Record backend receipt to detect unintended replay. |
| OT-08 | Real gRPC add/list/remove/re-add; decode native ClientConfig. Maintain an active transfer while deleting: old transfer finishes, stale handler rejects new flow, replacement uses new credentials/pool. Duplicate tag and failed creation leave no hidden pool. |
| OT-09 | Alice/Bob send known unequal lengths to alpha.test/beta.test. Assert exact user and user+domain gRPC totals and confirm actual reuse. Count outbound bytes at the same abstraction layer as Core's wrapper, not NIC bytes. Recreated tags preserve existing cumulative-stat semantics. |
| OT-10 | Independently enable/disable user, inbound, outbound and domain statistics; check Sidecar runtime decoding and domain aggregation. Credential fields must not appear in ordinary status output. Existing bucket/sequence/snapshot behavior stays unchanged. |
| OT-11 | Execute all B and C cases below using real dispatch and observation. |
| OT-12 | Barrier-controlled delete during dial/TLS/open/return/Close; no late publication to retired pool. Old pool cleanup cannot close replacement. Full instance shutdown also cleans draining pools. Join all copy workers rather than equating task.Run return with completion. |
| OT-13 | Execute impact-matrix regressions and both external-client inbound suites; record whole-suite failures/skips explicitly. |
| OT-14 | Format check, vet, race and approved Darwin/Linux build wrapper; execute local version/config checks. Do not bypass clean-tree release requirements. |

TCP payload sizes: 1, 8192, 65535, 65536 and 1 MiB; compare full contents or hash
in both directions. Exercise small-write/large-read and inverse, server-first banner,
delayed final response after upload EOF, peer FIN with unread buffered bytes. Do not
promise unsupported TCP half-close semantics: document actual protocol behavior.

## OB and egress (OT-11)

| ID | Procedure and oracle |
|---|---|
| B-01 | Real burstObservatory -> tagged dialer -> Dispatcher -> AnyTLS -> HTTP target. Only 204 succeeds; 200/302/404/503 fail. Redirect target must receive zero requests. TLS/session establishment alone is not health. Test GET and HEAD. |
| B-02 | Default URL and longest-prefix overrides in multiple groups; capture exact RequestURI including ob query and escapes. Probe A failure does not alter B samples. |
| B-03 | Remote virtual 127.0.0.1:8080 routes to temporary Sidecar UDS. Local trap endpoint receives zero requests. Real Sidecar with controllable local provider/QoS fixtures covers healthy/failed/named-missing/default egress. No public ASN API dependency. |
| B-04 | Cross HTTP keepAlive on/off with ordinary and keepalive endpoints. Count HTTP requests, logical streams and physical sessions independently; closing HTTP idle connections must not be mistaken for closing the native AnyTLS pool. Do not assume HTTP reuse across scheduler batches. |
| B-05 | Windows 1/2/20: empty dead, first clean success alive, failure immediately dead, consecutive min(3,window) successes recover, interrupted streak resets, stale results dead. Assert real API output and existing pure-state oracle. |
| B-06 | Inject delays at dial, TLS, auth-write and HTTP-response phases. Timeout must end the probe's logical work without killing unrelated business. Probe timeout result alone does not prove underlying cleanup. |
| B-07 | Stop with delayed/in-flight checks; no late publication or leaked workers; restart probes successfully. Configure connectivity check separately: network-down suppression must not be mistaken for a healthy sample. Real scheduling uses 3s/2s/20 without claiming exact cadence. |
| B-08 | Feed actual observations into leastPing and weightedLeastPing; verify existing alive/tolerance/fallback rules and distinguish dead from high RTT. Probe traffic cannot inherit an earlier business user's identity. Preserve classic observatory regression as well as burst. |

The original ping.go DialContext passed the observer context rather than the
transport callback's context. B-06 reproduced unfinished TLS admission after HTTP
timeout. The candidate now gives each probe connection a cancelable dispatch
context, retaining transport values and observer shutdown cancellation; see
outbound-impact.md and outbound-implementation.md for the focused fix and evidence.
HTTP timeout alone still does not prove blocked dial cleanup.

## Chained proxy matrix (OT-11)

Topology rows: direct baseline; AnyTLS over SOCKS, VLESS, Hy2, and another AnyTLS
outbound; external AnyTLS client -> Core inbound -> Core AnyTLS outbound. Exercise
both proxySettings and sockopt.dialerProxy for supported combinations. Use Core,
sing-box and Mihomo as final AnyTLS servers. A unsupported combination needs explicit
validation/documentation, never silent direct fallback.

| ID | Procedure and oracle |
|---|---|
| C-01 | Unique backend marker plus per-hop connection logs prove each path. Disable the intermediate hop and arm a direct trap: request fails and trap stays untouched. |
| C-02 | Controlled DNS distinguishes relay, AnyTLS server and business-target resolution. Verify server SNI/CA independent of relay hostname. Cover IPv4/IPv6 and actual configured source/interface binding, including dynamic binding semantics. |
| C-03 | Record each layer's physical/logical counts under sequential and concurrent workload. First-request cancellation cannot contaminate later requests or close other users' shared tunnels. |
| C-04 | Delete top outbound during active traffic, delete/recreate relay tag, change credentials, then create fresh connections. Distinguish existing established tunnels from subsequent dialing; old cleanup cannot affect replacements. |
| C-05 | Break/restart each hop at dial/handshake/transfer, test subsequent recovery and no payload replay. Missing/self/cyclic relay references must terminate with bounded failure, not runaway recursion or sockets. Preserve established configuration precedence. |
| C-06 | TCP exact payload and UDP-over-TCP packet identity across every topology; outer Hy2 does not authorize bypassing AnyTLS for UDP. Per-layer outbound counters need not equal each other; never sum them as user traffic. |
| C-07 | OB concurrently with real users across shared chains; correct ob URL and remote UDS, independent failure groups, valid health/balancer results, and cleanup after observer stop and instance shutdown. |

## Resource and execution gates

Use barriers/channels for races, not probabilistic sleep scheduling. Repeat critical
race cases 100 times with a recorded seed. Borrow wire-recording tests to assert no
PSH/FIN before SYN and wakeup of parked readers on peer close. Run a ten-minute mixed
TCP/UDP/probe workload including resets and retirement/recreation.

Assert owned sockets/workers/timers are zero after teardown; track explicit fixture
counts. Supplement with warmed-up FD/goroutine samples and retained heap after two
GC cycles across repeated rounds. Global RSS or a loose goroutine threshold alone
cannot establish absence of leaks. Preserve pprof diagnostics when a bound fails.

From Core, relevant existing package gates (new test names must be added to evidence):

```sh
go test -race -count=1 ./proxy/anytls/... ./infra/conf ./app/proxyman/... ./app/dispatcher ./app/stats/... ./app/observatory/... ./app/router/... ./common/mux
go vet ./...
go test -json -count=1 ./...
```

External strict-mode runner must explicitly select both directions and check nonzero
test counts. Sidecar tests execute in its own repository. Release builds execute via
`../xray-config/scripts/xrayctl.py build core` from the configuration repository using
its approved clean-tree workflow. Record all actual cwd/commands; do not paste a
planned command as executed evidence.

Every acceptance row remains open until fully linked to named tests and actual results.
The 2026-09-27 baseline run of engine, common/mux and proxyman/outbound (normal and
race) passed, but does not cover the new outbound or prove any row above complete.
