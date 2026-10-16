# SS2022 Safety Verification

## Reading Results

This is the current requirement/oracle map for existing tests. No runtime tests
were executed for the 2026-10-16 documentation split. Actual dated results are
indexed in [validation.md](validation.md) and
[SAFETY.md](../../proxy/shadowsocks_2022/SAFETY.md); test existence alone is not PASS.
IDs below are Core011-local. Old IDs in the last column remain historical IDs
in their original specs and evidence, not renamed experiments.

| ID / requirements | Existing fixtures | Required oracle | Original mapping |
|---|---|---|---|
| V-001 / FR-001, NFR-001 | `TestSafetyPartialHandshakeAbsoluteBudget`, `TestSafetyHandshakeResourceWaves` | Single/multi/relay: silent, salt-only, short fixed header, wrong key, variable-header trickle, zero policy; absolute budget, server-side close, Process return, no dispatch, joined handlers and exact socket ownership; 200 silent connections in ten waves per variant | Core010 FR-003, V-003 |
| V-002 / FR-002 | `TestSafetyLongTCPAndDeadlineErrors` | Finite then cleared underlying deadline, clear before dispatch; setup/clear errors close without dispatch; exact early/late bytes after three seconds, data-with-EOF, level 0 then authenticated level 7 | Core010 FR-003/004, V-004 |
| V-003 / FR-003 | `TestNativeSSExternalFirstReadContract`, `TestRelayTCPHandshakeForwarding` | Real sing-box/Mihomo whole-header positive and forced one-byte first-read negative; correct identity/exact bytes or raw reset, zero dispatch/response; relay preserves inner handshake rather than terminating downstream authentication | Core010 V-005; later independent first-read acceptance |
| V-004 / FR-004 | `TestSafetyUDPRevocationReplayAndExpiry`, `TestSafetyUDPActiveRPCRevocation`, `TestMultiUserManager` | Production HandlerService add/list/count/remove/re-add, duplicate rejection, old/new keys; A session/link retirement, no revival, B continuity; distinguish later UDP admission from TCP/in-flight I/O | Core010 U-001, V-005 |
| V-005 / FR-005 | `TestSafetyInvalidUDPAllocationWaves`, `TestSafetyUDPInvalidEIHAndAEADWaves`, `TestSafetyUDPRevocationReplayAndExpiry`, `TestSlidingWindow` | Invalid single/multi-user waves retain zero sessions/windows; 32 concurrent duplicate decodes admit exactly one; window behavior preserved; do not claim relay authenticates downstream AEAD | Core010 U-002 and U-003 replay portion, V-005 |
| V-006 / FR-006 | `TestSafetyUDPContextIsolation`, `TestSafetyUDPTransportIsolationAndRoamingWaves`, `TestRelayUDPSessionStabilityAndDispatch`, `TestClientUDPSessionMultiDestination` | A/B saved contexts remain stable, exact decoded reply bytes/destination, shared transport and source roaming do not cross identities; relay keeps stable original session ID/destination and forwards decodable packets | Core010 U-003, V-005 |
| V-007 / FR-007 | `TestSafetyUDPCloseDuringDispatch`, `TestSafetyUDPExpiryWithoutTrafficAndManagerClose`, revocation/reply-worker joins in V-004/006 | Close cancels blocked Dispatch; quiet timer expiry without new traffic, explicit cleanup tick, closed manager/session cannot recreate cipher/link; response workers and Process workers join | Core010 U-001..003, V-005 |
| V-008 / FR-002, FR-008 | `TestOutboundFailureClosesConnection`, `TestTransportTCPHalfClosePolicy` | Successful Dial result closed on injected early TCP/UDP error, normal/TimeoutOnly; exact remaining-direction bytes then correct shorter half-close timeout, idle expiry and reader retirement | Core007 V-032 SS portion; Core010 V-004/005 |
| V-009 / NFR-001, NFR-002, FR-001, FR-002, FR-008 | `TestSS2022ResourceSoak`, executable `safety_interop.py` baseline | Exact valid bytes and user counters, invalid-user bytes zero, owned sockets/workers/native copy stacks drain each wave; FD/post-GC heap plateau after warm-up; exact old/upstream/candidate silent-peer comparison attributed separately | Core010 V-003/004 and local-resource part of V-014 |

The complete contract also includes session-ID collision rejection, pointer-
conditional replacement removal and late-successful-Dispatch interruption
(FR-006/007). The existing isolation/retirement tests and SAFETY.md cover the
surrounding behavior; do not describe each of those three branches as having
an independently injected negative control without a recorded execution.

## Existing Local Rerun Recipes

From the Core candidate root, only when a future test window is authorized:

```sh
go test -race ./proxy/shadowsocks_2022 -count=1 -timeout=3m
go test -race ./proxy/shadowsocks_2022 -run 'TestSafetyUDP|TestOutbound' -count=10 -timeout=90s
go test -race ./proxy/shadowsocks_2022 -run '^TestTransportTCPHalfClosePolicy$' -count=10 -timeout=3m
go vet ./proxy/shadowsocks_2022
```

Package race does not imply execution of disabled opt-ins. For the existing
short soak recipe:

```sh
XRAY_SS2022_SOAK=1 XRAY_SS2022_SOAK_DURATION=10s go test -race ./proxy/shadowsocks_2022 -count=1 -timeout=3m
```

The default opt-in duration is one hour. Use the exact source/platform/command
in SAFETY.md when interpreting its recorded one-hour Linux/arm64 race run;
do not rerun it as part of a document check or treat ten seconds as an hour.
The failed first attempt remains failed and has separate provenance.

The independent first-read test requires `XRAY_SS2022_FRAGMENT_INTEROP=1`
and explicit `XRAY_SS2022_SINGBOX`/`XRAY_SS2022_MIHOMO` binaries. Record their
versions/hashes; missing binaries fail an opted-in run. An ordinary skip is not
acceptance. The recorded eight-case race execution is in SAFETY.md's
Coordinated First-Read Acceptance, not its earlier pending-execution paragraph.

## Resource And Failure Oracles

- Count exact owned resources and join Process, response and copy workers.
  Server closure, not client cleanup, establishes the authentication bound.
- Invalid packet waves cannot publish cached session/window/cipher state;
  replay admissions and user removal must be observed through real identity
  and link behavior, not only a manager-map size.
- Compare exact bytes/destinations and independent user counters. The soak uses
  production SizeStatWriter/Stats counters but does not prove full Stats RPC or
  the whole production routing pipeline. Paired consumer tests remain Core010.
- Keep the half-close timeout-selection mutation's failure as negative evidence;
  no mutation is applied by this split. The outbound regression proves Close
  on injected failure, not every possible timer branch by itself.
- FD and goroutine diagnostics use platform-specific sampling; unsupported
  platforms are not silently equated to Darwin/Linux. Retained heap tolerance
  follows warm variance and the documented runtime floor, not an RSS guarantee.

## Integration Boundary

Core010 retains [V-005 cross-client compatibility](../010-upstream-main-upgrade/tests.md#v-005-ss-control-plane-udp-and-native-engine-compatibility-fr-004005009),
U-004 chain migration, U-006/007 Stats/proto consumer integration, V-013 official
build/whole-Core gates, V-014 whole-server THP/canary, and V-015 multi-hop/remote
acceptance. Core011 supplies SS safety results to those gates, not an alternate
release checklist. Old/new executable interop and independent clients' tested
ciphers/directions remain the exact sets recorded in the original JSON files.

## Documentation Validation

Check relative Markdown targets/anchors in this scope, requirement-to-test and
task mapping, `git diff --check`, and pre/post hashes for protected evidence.
Do not edit or regenerate a dated log, source manifest, `.gz`, SHA256SUMS or
manifest.json to make a current documentation link or source match.
