# Shared UDP Loopback Source Selection

## Authorization And Scope

On 2026-10-15 the coordinator extended the candidate-only V015 agent's ownership to
`transport/internet/system_dialer.go`, independent
`system_dialer_loopback*_test.go` regressions and this report. Canonical develop,
dependencies, unrelated runtimes and production services remain unchanged.
No commit, checkpoint/release build, push or deployment was performed. The main
agent owns the eventual clean-tree official build after its unified checkpoint.

Runtime change: direct `DefaultSystemDialer.Dial` UDP calls select `127.0.0.1`
only when source is nil and the supplied destination is a literal IPv4 loopback.
Explicit source (including wildcard/IPv6), TCP, unresolved domain destinations,
non-loopback destinations, interface-bound sockets and dialerProxy are excluded.
The original context, socket options, controllers, descriptor ownership and
PacketConnWrapper are otherwise unchanged. This is not global ForceIPv4, not an
IPv6-loopback change, and not an adjustment to protocol buffering, timeouts,
application retries or SS2 sendThrough configuration.

## Natural Failure

The V015 SS2/SS2 matrix first failed user1 UDP31 after both hops authenticated.
A short repeat passed, so a separately authorized, unchanged-source diagnostic
ran up to 50 repetitions with native-dialer delegation and UDP tuple/read/write
hashes. It stopped on round33 after 33 successful rounds.

Evidence: `/tmp/core-protocol-chain-v015-ss2-udp-diagnostic50-r5-20261015.jsonl`.
Complete topology, payload hashes and user counters are retained in
[the V015 ledger](../010-upstream-main-upgrade/protocol-chain-network-20261015.md).

The live relay IPv4 socket `127.0.0.1:56399 -> 127.0.0.1:50726` coexisted with a
later wildcard dual-stack outer SS2 socket `[::]:56399 -> 127.0.0.1:54346`.
The final backend received the 31-byte request exactly once. Both layers had
complete response user-byte counters, but the older IPv4 socket read the
147-byte encrypted reply intended for the new dual-stack socket. The new socket
had no response read before its unchanged five-second request context closed it.
This demonstrates return-path misdelivery, not failed authentication or an
unobserved assumption about port exhaustion. No reuse socket option was injected
into that natural diagnostic. The original uninstrumented r3 failure is only
same-cause inference; its tuple was not recorded.

## Independent Wire Regression

`TestSystemDialerLoopbackUDPWire` calls the actual DefaultSystemDialer and an
owned real IPv4 UDP echo endpoint. It creates a competing IPv4 socket on the
client's actual source port. To establish the collision deterministically, a
temporary test controller adds SO_REUSEPORT **only when the real dialer chose a
wildcard bind**; the competing test socket also requests reuse. A concrete-source
client never gets the injected option, reserves its port, and must receive the
exact response. No LocalAddr is falsified and no synthetic response is returned.
The controller is restored after owned sockets close; the test is not parallel.

The fixture does not pretend that this injection reproduces Darwin's natural
ephemeral allocation algorithm. The separately observed natural run supplies
that evidence. The wire test establishes the same misdelivery condition without
SS2, AnyTLS, Core config generation, protocol pools or the scenario harness.

Against the unchanged runtime, the competing socket received all 33 response
bytes with exact=true, while the actual client was `[::]:52050` and the competing
socket was `127.0.0.1:52050`. The regression failed immediately. After the narrow
source guard, the unchanged fixture passed twenty race repetitions.

The first baseline attempt (without deterministic reuse injection) failed its
unreserved-source assertion but did not establish stolen payload; its log is
retained separately as `/tmp/core-system-udp-loopback-baseline-red-20261015.jsonl`.
The stronger wire evidence is the `baseline-red-r2` log, not that first attempt.

## Policy And Compatibility

`TestSystemDialerLoopbackUDPSourcePolicy` has twelve pure cases: canonical and
other IPv4 loopback, non-loopback, IPv6 loopback, domain, TCP, explicit IPv4,
explicit IPv6, explicit wildcard, bound interface, dialerProxy and unrelated
socket mark. It asserts unchanged source identities outside the guard.
`TestSystemDialerLoopbackExplicitSource` also opens/closes real UDP sockets with
nil and explicit loopback source. Neither introduces a detached context.

The full `transport/internet` race package also ran, rather than substituting
the focused cases for existing redirect/cancellation/ownership regressions.
Unresolved domain input to this system dialer is unchanged; if an upstream
caller already supplied a resolved literal, that literal is the input boundary.
This does not claim to fix non-loopback/mapped-host/interface UDP collisions.

## Executed Evidence

| Command / Gate | Result | Events / Time |
|---|---|---|
| Independent unchanged-runtime wire, race | FAIL as required | `/tmp/core-system-udp-loopback-baseline-red-r2-20261015.jsonl`, stolen exact payload, package 0.610s |
| Fixed wire + policy + explicit source, race x20 | PASS, zero skips | `/tmp/core-system-udp-loopback-fixed-race20-20261015.jsonl`, 300 test/subtest events, 1.612s |
| Complete internet package, race | PASS, zero skips | `/tmp/core-system-udp-loopback-package-race-20261015.jsonl`, 25 events, 1.321s |
| Same SS2/SS2 diagnostic, fixed race x50 | PASS, zero skips | `/tmp/core-protocol-chain-v015-ss2-udp-diagnostic50-race-r6-20261015.jsonl`, 51 events, 5.205s |
| Frozen-source two-hop 16 + three-hop 64 | Normal and race PASS, zero skips | Full per-group values in V015 ledger |
| Frozen-source verified TLS 9 + explicit VLESS Mux 4 | Normal and race PASS, zero skips | Full per-group values in V015 ledger |
| Scoped vet and strict upstream formatter | PASS | `go vet ./transport/internet ./testing/scenarios`; `infra/vformat/main.go -mode dryrun` |

Exact focused commands, run serially:

```sh
go test -race ./transport/internet -run '^TestSystemDialerLoopbackUDPWire$' -count=1 -timeout=30s -json > /tmp/core-system-udp-loopback-baseline-red-r2-20261015.jsonl 2> /tmp/core-system-udp-loopback-baseline-red-r2-20261015.stderr
go test -race ./transport/internet -run '^TestSystemDialerLoopback' -count=20 -timeout=2m -json > /tmp/core-system-udp-loopback-fixed-race20-20261015.jsonl 2> /tmp/core-system-udp-loopback-fixed-race20-20261015.stderr
go test -race ./transport/internet -count=1 -timeout=2m -json > /tmp/core-system-udp-loopback-package-race-20261015.jsonl 2> /tmp/core-system-udp-loopback-package-race-20261015.stderr
```

The baseline command was executed before the runtime edit, against the unchanged
candidate source. Re-running it on the fixed source is expected to pass.

Freeze manifest: `/tmp/core-protocol-chain-v015-freeze-r6-20261015.sha256`, covering
the runtime, both independent regression files and all three scenario files.
After the 16/64 normal/race gates, the agent returned the Mac socket-heavy window
to Sidecar. Darwin Outage/Cancellation/ActiveClose/Resource/invalid-identity and
size-boundary wire gates remain explicitly pending. Subsequent isolated
Linux/arm64 execution, including all those gates and strict checkptr, is recorded
in [the Linux ledger](../010-upstream-main-upgrade/protocol-chain-linux-20261015.md).
It is not native amd64 or Linux race evidence. No strict-checkptr execution is
inferred from ordinary/race runs.
