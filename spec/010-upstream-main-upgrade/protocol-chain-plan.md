# Protocol Chain Matrix

## Authorization And Ownership

This supplements spec010's chain gate on the isolated candidate only:
`/Volumes/Linux/opensource/cuber/xray-core-rebase-20261014`, branch
`rebase/main-20261014`. The initial increment was test-only; the subsequently
authorized narrow runtime repair is recorded in spec002 below. It does not modify
canonical develop, dependencies, shared scenario harnesses, consumer pins or
deployment inputs.
The coordinator delegated this additional test work alongside the existing parallel
Core/Sidecar/Config verification. No commit, push or deployment is implied.

Owned files:

- `testing/scenarios/protocol_chain_fixture_test.go`: private PKI, JSON builders,
  real Core instances, receipt backends, per-user stats and owned shutdown.
- `testing/scenarios/protocol_chain_matrix_test.go`: parser, healthy matrices,
  explicit VLESS Mux and TLS trust controls.
- `testing/scenarios/protocol_chain_lifecycle_test.go`: outages, cancellation,
  active Core close and warmed resource rounds.
- `testing/scenarios/protocol_chain_listener_test.go`: physical listener capture,
  original RawConn retirement and live/missing/foreign ownership controls.
- `testing/scenarios/protocol_chain_domain_test.go`: independent domain-preservation
  matrix and deny-route controls; no DNS configuration or resolver replacement.
- This document and [verification handoff](protocol-chain-verification-20261015.md).

Execution updates and retained failures are in
[the Darwin network ledger](protocol-chain-network-20261015.md) and
[the isolated Linux ledger](protocol-chain-linux-20261015.md). Current Core76
Darwin results, retained original reds and additive domain coverage are in
[the final Darwin ledger](protocol-chain-darwin-20261016.md), with
[filtered persistent events](evidence/protocol-20261016/README.md). Platform/revision
and instrumentation are separate gates, not interchangeable PASS evidence.

## Topology And Protocol Boundaries

Names are in wire order: `ss2_vless` means client -> SS2 relay -> VLESS exit ->
backend. The client selects the last protocol outbound as its default; that
handler's `streamSettings.sockopt.dialerProxy` selects its predecessor recursively.
Each hop is an independent real Core instance listening on IPv4 loopback. Relay
freedom handlers explicitly allow loopback destinations only. There is no client
freedom outbound, fallback, alternate client dialer, synthetic dispatcher response,
external DNS request, or mocked protocol engine. A missing hop cannot be treated
as a successful direct route. Every healthy hop must account for the authenticated
user, independently of destination receipts.

| Protocol | Fixture Parameters | Deliberately Not Substituted |
|---|---|---|
| SS2 | Native `shadowsocks`, `2022-blake3-aes-128-gcm`, TCP/UDP, two different user keys and a third distinct server key per hop | No shared multi-user key, legacy SS cipher, SS relay-user shortcut or TLS wrapper |
| VLESS | RAW/TCP + verified TLS, `encryption/decryption: none`, separate UUID per user/hop | Not REALITY, Vision, new VLESS Encryption or a claim of their acceptance |
| Hy2 | Hysteria version 2 over QUIC/TLS, `h3`, distinct auth per user/hop | No SS replacement, outer TCP Mux, changed ChromeParrot/QUIC idle/pool budgets or protocol retries |
| AnyTLS | RAW/TCP + verified TLS, distinct passwords per user/hop, native Core-owned pool with default idle/admission parameters | No outer Xray Mux, engine replacement, mocked pool or shorter idle parameters |

The original 80 healthy combinations use **literal IP application targets only**.
An independent test now repeats all 16 pairs and 64 triples with two business
domains, both users and TCP/UDP. Its final hop has domain-only `AsIs` routes to
network-specific freedom redirects at owned literal loopback endpoints; the
first/default outbound is blackhole. Four representative triples separately
reject wrong-domain and literal-IP business targets with zero denied-backend
receipts. This proves domain preservation and remote routing, **not DNS resolution
or query ownership**. It does not change the original 292-event test selection.

Related but non-equivalent DNS-site coverage is
[`TestAnyTLSOutboundAddressDNSLocality`](../../proxy/anytls/outbound_address_test.go)
and Sidecar's
[`TestCandidateDialerProxyActualSocksChain`](../../../xray-sidecar-rebase-20261014/probe_chain_runtime_candidate_test.go).
The earlier DNS-resolution extension remains deferred: empty DNS server config
implicitly creates a localhost resolver, and freedom's final-rule domain
precheck has system/global lookup paths. The coordinator excluded expanding
that runtime/DNS design in this increment. The additive route fixture redirects
to IP before that precheck and neither changes global/system DNS nor claims a
DNS PASS or an instrumented zero-query count.

TLS uses a generated ECDSA CA and signed `protocol-chain.test` leaf, explicit
fixture roots, SNI verification and `disableSystemRoot: true`. No `allowInsecure`
is set. Separate positive, wrong-CA and wrong-SNI wire tests cover VLESS, Hy2 and
AnyTLS. SS2 intentionally has no TLS because its production protocol boundary
does not require it. Hy2 and freedom use loopback source binding. No global
system dialer is replaced by the ordinary matrices. The opt-in diagnostic wraps
and delegates to DefaultSystemDialer only to record real UDP tuples/read/write
hashes; it does not substitute protocol behavior.

## Executable Layers

All network tests require `XRAY_PROTOCOL_CHAIN_NETWORK=1`. Without it they report
SKIP with a window-reservation reason, never network PASS. With
`XRAY_PROTOCOL_CHAIN_STRICT=1`, missing network opt-in is FAIL, never SKIP.
They do not call
`t.Parallel`. Tests run real traffic once per request, without application retries.

`Core76 x3` below means ordinary, race and strict checkptr2 on clean
`76b17aa2`, all PASS with zero skips in the dedicated selection. `76+domain x3`
is the separately compiled additive file snapshot recorded in the final ledger.
Historical Linux evidence retains its own revision and earlier listener oracle;
it is not promoted to Core76 acceptance by these Darwin runs.

| Test | Cases | Work Per Case | Recorded Execution |
|---|---:|---|---|
| `TestProtocolChainConfigMatrix` | 16 double + 64 triple | Build JSON configs for every hop, both users and complete client chain; no Core.Start/listener | Core76 x3 |
| `TestProtocolChainTwoHop` | All 16 ordered pairs including homogeneous pairs | Both users, TCP/UDP, two destinations per network; sizes 1/31/512/1200, plus TCP 8192/65536; 40 business requests | Core76 x3 |
| `TestProtocolChainThreeHop` | All 64 ordered triples, including repetitions | Both users, TCP/UDP, two destinations per network; sizes 31/1200; 16 business requests | Core76 x3 |
| `TestProtocolChainVLESSMux` | VLESS first hop x all four exit protocols | Explicit Xray TCP Mux concurrency 8 and XUDP concurrency 8 on VLESS only; same traffic as double-hop | Core76 x3 |
| `TestProtocolChainTLSVerification` | 3 TLS protocols x 3 trust controls | Trusted root succeeds; wrong CA/SNI fail before local timeout and deliver zero business requests | Core76 x3 |
| `TestProtocolChainUDPEncodingBoundary` | SS2/bare VLESS/VLESS XUDP/Hy2, max and max+1 | Native mode-specific encoder result and output bytes, no sockets | Core76 x3 |
| `TestProtocolChainUDPBoundary` | All 4 protocols, independently single-hop | Actual destination-specific max and max+1, exact payload or zero receipts | Core76 x3 |
| `TestProtocolChainInvalidIdentity` | 4 protocols x 3 positions | Valid TCP/UDP control on destination1, wrong wire identity at first/middle/last position, zero receipts on destination0 | Core76 x3 |
| `TestProtocolChainListenerCapture` | 4 protocols + doko TCP/UDP | Exactly one original RawConn per expected physical listener; live before and closed after Core.Close | Core76 x3 |
| `TestProtocolChainListenerOwnershipOracle` | TCP + UDP | Live original rejected; closed original accepted despite foreign same-port rebind; foreign stays live | Core76 x3 |
| `TestProtocolChainListenerMissingCapture` | Pure negative control | Zero capture and invalid fixture inbound list rejected | Core76 x3 |
| `TestProtocolChainSS2UDPDiagnostic` | Up to 50 repetitions of SS2/SS2 | Native dialer delegation with tuple/read/write hashes; stop on first failure | Historical only; excluded from final 292 |
| `TestProtocolChainOutage` | All 16 pairs x each failed hop (32 cases) | Cold stop, failed TCP/UDP, restart and first fresh requests; then delivered/response-held TCP stop, failed TCP/UDP, restart and first fresh requests | Core76 x3 |
| `TestProtocolChainCancellation` | All 16 pairs | Cancel response-held TCP and UDP after backend receipt; join request, then fresh same-user and other-user requests without replacing handlers | Core76 x3 |
| `TestProtocolChainActiveClose` | All 16 pairs | Real doko entry, accepted TCP EOF/local listener retirement after client Close; explicit caller cleanup for UDP; backend worker join only after closing all service owners | Core76 x3 |
| `TestProtocolChainResourceRounds` | Warmup + 3 rounds of 4 triples | Real traffic and shutdown each round; final warmed process FD/goroutine bounds | Core76 x3 |
| `TestProtocolChainDomainPreservation` | All 16 pairs + 64 triples | Both users, TCP/UDP, two domains mapped to distinct owned destinations, sizes 31/1200, exact receipts and per-hop/exit stats | 76+domain x3 |
| `TestProtocolChainDomainRouteControls` | SS2/VLESS/each final protocol | Both users and TCP/UDP: valid domain control, wrong domain and literal-IP negatives, denied destination0 total receipts zero | 76+domain x3 |

TCP uses a four-byte length prefix plus exact payload; UDP preserves datagram
boundaries. Each destination owns a receipt ledger. Wrong destinations, damaged
bytes, truncation, unknown data, duplicate complete requests, missing successes
and failed IDs arriving after recovery fail the final accounting. All producers
and backend workers are stopped/joined before final receipt assertions.

## Identity And Resource Oracles

- Each user has distinct wire credentials on every hop and a distinct client
  Core/pool. The real receiving Core publishes that hop's authenticated email.
- Exit user uplink/downlink counters must equal exact application wire bytes:
  payload plus four-byte framing for TCP, payload only for UDP. Publication is
  awaited up to three seconds, rather than read immediately after a response.
- Outer counters must increase independently for each user in both directions;
  they include inner protocol/TLS/framing bytes, so they are not compared with
  logical payload length. Before the second user runs, its counters must remain
  unchanged. During the second user's workload only exit isolation is asserted
  against the first user's prior exact value, because first-user outer pool
  control frames can legitimately finish asynchronously.
- Every `Core.Close` must finish within five seconds. All backend accept/UDP
  loops and accepted TCP workers must join. On the success path, backends never
  forcibly close accepted connections: real Core/client closure must produce
  EOF. A five-second drain failure remains a test failure even if forced cleanup
  subsequently succeeds. Core listener retirement uses the original captured
  `syscall.RawConn`, not an FD integer or subsequent port availability: exactly
  one nonnil capture must be live at Start; after Close, `Control` must return
  `errors.Is(err, net.ErrClosed)` without invoking its callback. Missing captures,
  live originals and unexpected physical listeners fail. Physical ownership is
  SS2 TCP+UDP, VLESS/AnyTLS raw TCP, Hy2 UDP, and the actual doko network; a
  never-owned protocol/port pair is not tested. Negative controls also prove that
  a foreign same-port socket neither causes a leak verdict nor gets closed.
  `Core.Close` completion is not an exact all-Core-worker retirement gauge.
- Canceled request workers must join within five seconds; other/same-user new
  requests must still succeed. Active-close accepted TCP must return `io.EOF`
  within five seconds after Close and before its fifteen-second safety context
  expires, without test-driven cancellation. UDP
  has no remote EOF and explicitly requires caller cancellation/Close. Backend
  natural EOF/join follows closing every service Core owner, not client-only
  instantaneous teardown across UDP/QUIC.
- Resource rounds separately enumerate RLIMIT_NOFILE with F_GETFD (Darwin),
  aligned with the existing SS2/AnyTLS oracle, or `/proc/self/fd` minus its
  enumeration FD (Linux),
  and goroutines after warmup. Final allowance is baseline +4 FDs / +8 goroutines
  within ten seconds. These are bounded process-growth checks, not exact internal
  AnyTLS admission/physical-session gauges, heap/RSS/THP measurements or proof
  that every unrelated process-global goroutine vanished. Lifecycle/resource
  cases are build-tagged Darwin/Linux, not a claim of other-platform acceptance.
  On the target Darwin/Linux strict run the resource case contains no skip.
- An established outage's held TCP request must end by protocol closure, not
  local cancellation. A 100-second safety budget preserves the existing native
  AnyTLS timeout + scan + QUIC idle bound (30+30+30 seconds plus margin). Failed
  outage requests get two seconds each; restoration gets one new five-second
  request per network, with no readiness traffic, replay or retry loop.

## Coverage Boundaries

The full 16/64 matrices are healthy transport composition, not all imaginable
protocol options, failure phases and concurrency schedules multiplied together.
The following are unimplemented/unexecuted here, not waived or labeled PASS:

- Three-hop outage/recovery, three-hop Mux, Mux outage/cancellation/resource rounds,
  every Mux position, and sustained concurrent Mux business saturation. The Mux
  layer configures real explicit TCP Mux; its healthy workload is sequential.
- Cuts during dial, TLS/QUIC authentication, AnyTLS SYN, partial application frame,
  partially delivered response, blocked physical write, RST/FIN injection or
  packet-loss/reordering. Current stop tests cover no prior dial and complete
  destination receipt with withheld response only. An outage stops the real
  middle or exit Core, not a mocked error or a TCP-listener-only close.
- Immediate cancellation retirement of all remote UDP target links. UDP idle
  dispatch and AnyTLS native pooled sessions may intentionally outlive one logical
  request; this gate proves request join, subsequent work and whole-Core shutdown.
- Exact post-TLS physical/session/logical counts at every heterogeneous layer,
  domain traffic buckets or dynamic user revocation. Invalid identity and exact
  protocol-size negative controls now have the separate tests listed above.
- Independent client/server interoperability, native Linux amd64 execution, live production
  routes, Akile long-running probes, and VLESS Encryption/REALITY/Vision matrices.
  These remain separate agents' or existing owning fixtures' gates; their status
  is not inferred from this test source or parser-only success.

The original test-only ownership was explicitly extended after the observed
shared UDP collision. The independently scoped repair and its baseline-red/
fix-green evidence live in
[spec002](../002-core-connection-lifecycle/system-udp-loopback-source-20261015.md).
Further runtime changes still require the main agent's coordination; neither
these matrix passes nor that repair authorize a release/deployment.
