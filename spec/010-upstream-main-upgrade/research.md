# Source Review and Priorities

## R-001: What Upstream Actually Fixes for pgz.luck

The incident separated silent unauthenticated sockets from large RSS/THP. The
observed server was `1e0c6a3`, while this fork comparison uses `2074c223`.
The newer local fork still calls sing's initial SS authentication reader without
setting a handshake deadline in all three inbound variants. A generic tcpWorker
only closes after Process returns. That mechanism can retain a silent socket
indefinitely, irrespective of traffic throughput or a configured handshake policy.

Cached upstream `3519dfec` retains the same pre-auth omission. Its
`d878fc83` #6162 changes invalid destination handling and **post-auth** activity
timers in singbridge. `2776ea6d` #6698 closes an **outbound** server connection
on an early return. Neither is the missing pre-auth deadline.

Newer [#6831](https://github.com/XTLS/Xray-core/commit/65e853e) and
[#6866](https://github.com/XTLS/Xray-core/commit/0086362) replace the SS engine.
The [web-observed multi-user source](https://raw.githubusercontent.com/XTLS/Xray-core/main/proxy/shadowsocks_2022/inbound_multi.go)
uses the Xray policy handshake timeout before reading a TCP header, and owns
connection closure in the handler. Static evidence supports a directly relevant
fix; it is not a candidate-version runtime pass. This main URL is unpinned and
must be replaced with a fetched full-SHA blob before implementation.

The initial rewrite changes 25 files and deletes common/singbridge. Our AnyTLS
imports its destination conversions in server.go, udp.go, client.go and
client_udp.go, and its engine/UoT code still uses sing v0.8.14. Removing all sing
dependencies would therefore break local functionality. This describes the fixed
research baseline, not the separately approved 2026-10-05 refactor. The
[spec008 address adapter](../../spec/008-anytls/address-adapter.md) moves only
AnyTLS's conversion helpers into its own package; SS-2022 still needs singbridge
and AnyTLS still needs its pinned Sing engine/UoT dependencies.

The [follow-up discussion](https://github.com/XTLS/Xray-core/pull/6866) documents
old-client large-buffer and multi-user UDP response-key fixes. It also notes that
cached UDP sessions can outlive user removal. Treat UDP revocation as an explicit
contract review, not a claim that upstream solved it. TCP live-session revocation
and UDP packet authorization are distinct. Our usersync does dynamic removals.

The follow-up moves handshake setup into InitServerStream/TransportTCP helpers;
the available page removes an inline deadline-clear call. Complete helper source
was not retrievable. **Successful handshake deadline clearing is still unverified**;
test a valid stream past the handshake deadline before considering either rewrite.

Recommendation: P1 urgent narrow SS policy/deadline adaptation with deterministic
tests on existing engine, followed by a separate native-engine proposal. Its
protocol-native read/framing behavior is not a small isolated patch.

## R-002: QUIC Sniffer, P0

[8f15190c #6471](https://github.com/XTLS/Xray-core/commit/8f15190c230fe6975c9b18e31316c6bc494f3863)
checks that a packet contains the full header-protection sample before slicing it.
The local common/protocol/quic/sniff.go lacks this check. The commit references
GHSA-xqmr-94vq-cxvx and GHSA-9h6x-9r9m-5qw2; advisory pages were not retrievable,
so no CVSS/exploitability claim is invented. Public sniffing paths justify urgent
defensive backport. QUICv2 feature support is a separate change, not a prerequisite
for the length guard. Test truncated samples at every boundary and fuzz panic freedom.

## R-003: DNS Name Panic, P1

[a2cec2e5 #6207](https://github.com/XTLS/Xray-core/commit/a2cec2e580e8ea1900701b11b425e0351de2ec71)
replaces a panic-producing DNS name constructor with error propagation. The fork
still uses MustNewName. The change covers UDP/TCP/DoH/DoQ and multiple requested
address families: error fan-out must match outstanding questions or queries can
hang. Preserve our injected trusted DoQ fixture and do not disable TLS verification.
No attack or production crash from this path has been demonstrated here.

## R-004: Stats Registration, P1

[0bafca94 #6468](https://github.com/XTLS/Xray-core/commit/0bafca94868a4ec38cd3dba0f2ba7ec6f44bbf18)
atomically gets/creates Counter, OnlineMap and Channel in the manager. The fork
still has helper Get then Register calls with separate critical sections. Concurrent
first-use may race; upstream interface changes affect dispatcher, handlers and
test/mock managers. This is counting/concurrency correctness, not established RSS
root cause. Keep optional DomainTrafficManager and ordinary StatsService semantics;
run exact-byte two-user TCP/UDP and cursor/restart contracts after adapting.

## R-005: Buffered Writer, P1

[6ce8dc53 #6723](https://github.com/XTLS/Xray-core/commit/6ce8dc53e79842af71f0b4a360cb65d9eaa1e8f7)
continues after a partially accepted write fills the buffer, instead of treating
ErrBufferFull as a terminal payload-write error. The fork file lacks it. Small
shared fix with a broad byte-integrity gate: boundary sizes, partial progress,
flush errors and exact ordering/counters. It is not a socket-timeout fix.

## R-006: Router Snapshot Publication, P1

[598bde74 #6678](https://github.com/XTLS/Xray-core/commit/598bde74122f5f0207c7c6a56ccb74fbbbe33fca)
builds replacement rule/balancer state privately and atomically publishes it.
Our mutation/ListRule paths lock, but pickRouteInternal iterates r.rules without
that lock while ReloadRules/RemoveRule can change it. Static race risk is real;
no new runtime reproducer was executed in this review.

Adapt the snapshot idea while preserving full ListRule details, protobuf field
numbers, user-domain matching, weightedLeastPing, balancer overrides and webhook
cleanup. Rules/balancers must be a coherent generation; copying two separate atomic
stores blindly is not an atomic pair. Review failed-build cleanup of newly created
webhooks/resources as well as close-versus-in-flight readers.

## R-007: Geodata Memory, P1

[56bb6366 #6139](https://github.com/XTLS/Xray-core/commit/56bb63668c391987e409fa12df42685fcf00d6c1)
weakly references shared matchers; [be8009c6 #6342](https://github.com/XTLS/Xray-core/commit/be8009c62509322682299bfbe969a62cee03f4d5)
also removes strong registry retention. The fork has neither. Paired adoption
avoids an incomplete cache-only fix; registry lookup, cleanup identity and callback
locking all need tests. The latter also changes domain case normalization, which
must be accepted explicitly. Go 1.26 already supplies weak pointers/AddCleanup;
do not upgrade to Go 1.27 just for this pattern.

These improve release of **unused** matchers after reload/instance churn. Static
unchanged rules may remain legitimately live; do not attribute pgz.luck's 720 MiB
AnonHugePages to matcher caches without a profile.

Newer [2953d44 #6821](https://github.com/XTLS/Xray-core/commit/2953d44) compacts built
MPH patterns/values into contiguous storage rather than per-rule objects.
[6243d2a #6867](https://github.com/XTLS/Xray-core/commit/6243d2a) additionally targets
matcher memory and regex filtering. These may reduce the steady-state rule heap,
not just retired matcher retention. Both require fetched-source semantic/differential
tests and pinned-DAT benchmarks; headline memory benefits are not local measurements.

## R-008: Equivalent vs Conditional Work

| Upstream change | Local source evidence | Decision |
|---|---|---|
| SS early outbound Close #6698 | outbound.go defer Close; TestOutboundFailureClosesConnection | Keep equivalent local fix; no duplicate backport |
| WaitReadCloser race #6694 | client.go mutex/Once; wait_reader_test.go | Keep local lifecycle protection |
| leastLoad tolerance #6156 | strategy_leastload.go and tolerance tests | Keep local behavior |
| Go formatting #6090 | .github/check-gofmt.sh | Existing gate, not missing feature |
| XHTTP leak #6095 | OpenStream failure/non-200 paths still lack explicit body Close | Missing despite WaitReadCloser fixes; conditional priority P2 |
| XHTTP races #6665 | Local c.closed remains unsynchronized; WaitReadCloser part protected | Partial overlap; audit, do not claim whole commit present |
| XHTTP/3 QUIC/UDP Close #6332 | Different from DNS DoQ TLS fixture changes | Conditional P2, separate client-resource lifecycle |
| Normal Observatory empty selector #6669 | observer.go lacks empty-set sleep | P2; production primarily uses burst, not same module |
| Burst close/cancel #6106/#6221 | Fork has shutdown waits/cancellation/HTTP cleanup | Partial; preserve fork 3s probing and health contracts |

Our burst interval code also compares duration to literal 10; upstream changes
that to 10 seconds **and clamps intervals to at least 10 seconds**. Production/local
requirements include 3s intervals. Fixing units does not authorize importing that
behavior change. Review positive intervals and overlapping rounds independently.

Hy2 sendThrough #6063, dynamic UUID #6395, v2.12.2 #6565 and ChromeParrot datagram
fix #6788 are a dependency-aware second phase. Check isz5 binding/WG paths and
our Hy2 manager lifecycle tests. Masquerade Unix path #6705 is not our Freedom
UDS Sidecar redirect. Kernel WireGuard is not Core WireGuard proxy code.

## R-009: Full-Merge Blockers

| Surface | Required migration/review |
|---|---|
| Freedom ipsBlocked -> finalRules | 44 current objects/39 nodes; empty list presently bypasses private blocking; upstream retains parsing only with warning. Verify loopback, UDS, admin/metrics and egress before any upgrade |
| Block delay/default resolution | 30-90 second blackhole defaults and finalRules resolution; preserve failure timing and private-IP intent |
| SS2022 native engine | Keep AnyTLS sing helpers/dependencies; preserve old clients, master/user keys, user management and UDP semantics |
| SS UoT removal | No current node field found, but do not remove AnyTLS UoT v2 or client compatibility accidentally |
| users/method renaming | clients/network are retained aliases at cached origin, not immediate breaking removals; don't mass rewrite configs without reason |
| TLS removals/REALITY default | No removed fields in node scan; still validate generators/client fixtures and minClientVer default across supported clients |
| Public raw VLESS/Trojan ban | Distinguish actual plaintext from our VLESS native random encryption; no blanket assertion all security:none is invalid |
| Transport Finalmask refactor | Retain dispatchConn ownership, multi-IP listeners, unix-socks auth, Unix redirects, certificate control API and chaining |
| Go/dependencies | Cached upstream requires Go1.27; local sing0.8.14 vs upstream0.5.1 then removed by native SS; preserve required APIs and test both platforms |

## R-010: Unmerged Reports Are Not Available Fixes

Upstream [issue #6870](https://github.com/XTLS/Xray-core/issues/6870) reports a
Mux.Cool dispatch/close race; it is distinct from AnyTLS's own session pool and
from UDP destination reuse reports. The web search shows the issue open at its
observed snapshot. Treat it as an additional regression/audit candidate, not a
merged origin fix, confirmed local reproduction or authorization to modify mux.

## R-011: Memory Acceptance Is Multi-Layered

The incident's live allocations (~53-60 MiB), RSS (~761 MiB) and huge pages
(~720 MiB) are not interchangeable. No upstream commit in this review establishes
a THP fix for that environment. [Go GC guidance](https://go.dev/doc/gc-guide#Linux_transparent_huge_pages)
supports investigating process-local THP behavior, but the runtime flag is not a
permanent universal contract. Compare same binary/config/workload with and without
the mitigation, then compare candidate code separately. Never use a restart-only
RSS drop to claim leak elimination.
