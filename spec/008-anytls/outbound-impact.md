# Shared-Code Impact and Review Gates

Status: review checklist, 2026-09-27. Read with [outbound tests](outbound-tests.md).
Runtime changes and executed review evidence are in
[outbound-implementation.md](outbound-implementation.md); remaining gates stay open.

## Scope discipline

Keep protocol implementation in proxy/anytls. Use Core dialer, policy, dispatcher
and statistical wrappers. Do not replace shared mux or routing algorithms with an
AnyTLS-specific scheduler. Do not modify singbridge, TLS or dispatcher merely to
make an adapter convenient. If a shared defect blocks acceptance, first write its
independent reproducer, identify affected callers and add their regressions.

| Surface | Existing behavior at risk | Mandatory review/test |
|---|---|---|
| Outbound manager | Default handler, tagged cache, concurrent selection, reverse proxy | Optional retirement only; unchanged legacy handlers. Remove/add races, missing and duplicate tag, defaults and cache invalidation. app/reverse and VLESS reverse also call AddHandler/RemoveHandler. |
| Creation and Start | Failed object or timer remains reachable | AddOutboundHandler closes a newly constructed handler rejected by the manager. Independent TestRejectedOutboundClosesConstructedResources verifies duplicate/closed-manager rejection, joined worker and original/default identity preservation (100 race rounds). Failed Start publication is tested separately by TestFailedStartDoesNotPublishOutbound. |
| Removal and shutdown | Deadlock, abandoned draining pool | Manager must not wait for network close or flow completion under its registry lock. Track retired objects through full instance shutdown, then release references when drained. Idempotent transitions; stale pointer cannot admit new work. |
| AnyTLS engine | Existing server authentication, framing, padding, revocation and budgets | Retain all inbound tests, external clients, user generation revocation and UDP budgets. Client retirement cannot change server FIN or admission semantics. |
| Dial context | Chained cancellation, DNS and source binding | proxySettings retains context; dialerProxy redirect uses WithoutCancel. Preserve required routing metadata without sharing mutable session objects. Do not apply unconditional Background/WithoutCancel as a universal fix. |
| Stream copy and policy | EOF truncation, wrong timeout, goroutine leaks | task.Run may return before copy workers exit. Test late responses and explicit cleanup. Level applies local policy, not remote identity or shared physical-session timeout. |
| singbridge/UoT | SS2022 plain, multiuser, relay, UoT | Test all affected users if wrappers change. Existing packet bridge includes fixed timeout/read goroutine and buffer handling; do not assume reuse automatically satisfies cancellation/size requirements. |
| VLESS/mux/Hy2 | Shared connection teardown and routing changes | Preserve mux worker concurrency/cumulative-retirement thresholds and Hy2 stream-only close. Optional retirement must not force-close legacy protocols on removal. |
| Stats/domain traffic | User leakage, double counting, reset and bucket drift | Exact two-user/two-domain deltas; switches; same-tag cumulative counters; spec006 sequence/bucket/snapshot regressions. AnyTLS outer accounting is not NIC-byte accounting. |
| Observatory/router | False health, stale samples, wrong fallback | B-01..08 and existing spec004/005 suites; forced tag, group overrides, URL/query, cancellation, classic/burst and balancer rules. |
| Proto/Sidecar | Old message decoding, address/user display, secrets | Add message/field IDs without changing old ones. Sidecar uses reflected type/address/user fields; pin new Core revision and verify runtime decoding. Outbound password remains opaque and is not a displayed username. |
| Listener/UDS | Standard admin path, multiport, protocol coexistence | Existing spec003 tests, TCP AnyTLS and UDP Hy2 same port, remote virtual 8080 -> UDS. No production listener or certificate migration. |

## Required review artifacts

For each shared-code hunk record: purpose, existing callers, preserved invariant,
new test exercising the change, unaffected-protocol regression, and result. Reject
unrelated cleanup and dependency churn. Use an opt-in retirement capability, not
an AnyTLS protocol-name branch or a change to every handler's Close semantics.

Review lock ordering explicitly: registry state, client pool state, session stream
state and write serialization. Network writes/closes and callbacks must not run
under new broad registry/pool locks. Reuse existing synchronization where it owns
the relevant invariant; a separate atomic flag is insufficient for admission races.

Distinguish protocol-stream termination from application worker completion: a peer
FIN can leave buffered response data. Retiring the physical session must not discard
already received bytes or declare cleanup complete while copy workers still run.

## Release decision

### Redirect connection ownership follow-up

The final chain admission oracle demonstrated a separate shared defect after
the earlier review: closing dialerProxy's pipe-backed connection did not cancel
a relay still dialing/handshaking. `transport/internet/dialer.go` now wraps that
connection with its own cancelable child of the existing WithoutCancel context.
The wrapper forwards net.Conn and buf.Reader/buf.Writer unchanged. Its Close
cancels only this relay dispatch; original caller cancellation, deadline and
runner return still cannot invalidate a pooled connection. No global dispatch
helper, TLS, route selection or mux algorithm was changed.

Independent `TestRedirectCloseOwnsPendingContext` (TCP/UDP),
`TestRedirectRemainsUsableAfterRequestCancel` and
`TestRedirectRunnerReturnDoesNotCancelConnection` cover these boundaries.
The reproducer failed before repair; 100 Darwin race rounds passed after repair
(1.676s). Native AnyTLS admission tests passed ten rounds (43.604s), and Linux
focused tests and strict external chains passed as recorded in the current audit.
Whole-Core regression covers non-AnyTLS redirect callers; final execution status
belongs in outbound-final-acceptance.md rather than the earlier revision table.

### Shared-diff reconciliation at fca957f5

The runtime diff against `cuber/develop` was re-read by file/hunk, rather than
inferred from the AnyTLS package result. No additional production change was
needed in this review. The following mapping records the shared surfaces;
protocol-local lifecycle acceptance remains a separate open gate.

| Changed files | Review conclusion and independent evidence |
|---|---|
| `app/proxyman/outbound/{handler,outbound}.go`, `features/outbound/outbound.go`, `core/xray.go` | Validation and retirement are optional capabilities. Existing handlers retain their removal path; newly rejected objects remain caller-owned and are closed. Start succeeds before publication. Registry locks are released before retirement/Close, so chained dispatch may still read the registry. Duplicate/default/cache/Start failure and legacy removal regressions are in the manager/core suites; native ownership overlap remains separately tracked. |
| `common/buf/{io,reader,readv_reader}.go` | Interrupt forwards to the existing underlying resource without acquiring ordinary Close ownership, mutating buffers or counters. Independent blocking TCP/pipe/UDP and timeout-reader fixtures exercise forwarding; the general buf suite covers existing read behavior. |
| `common/buf/readv_{posix,unix,windows}.go` | Only would-block returns control to the poller; EINTR is retried on Unix and terminal errors propagate. Buffer cleanup remains owned by ReadVReader. Runtime evidence is Darwin/Linux; historical Windows/illumos checks remain compilation-only, not claimed runtime verification. |
| `app/observatory/burst/ping.go` | Each returned connection owns its dispatch cancellation. Observer cancellation propagates, but closing one connection does not cancel other connections or their shared physical pool. Error paths stop callbacks and close returned connections. Independent burst contract tests and classic/burst/router regressions cover the shared callers. |
| `infra/conf/{anytls,xray}.go`, `proxy/anytls/config.proto` | Registration adds one protocol; optional sender validation applies consistently through JSON and handler construction. Existing proto messages and field numbers are unchanged; ClientConfig is additive. Candidate Sidecar decoding exercises actual registered metadata without publishing credentials. |
| `proxy/anytls/internal/engine/{client,session}.go` | Cancelable writes opt in for the native adapter. Stream/control reservations prevent early idle publication; cancellation watchers join before unlock/reuse. Pool Close releases its lock before network close and waits for reader workers. Server sessions have no client pool; existing server framing/FIN remains covered by inbound tests and both cancelable/reference wire tests. |
| `app/dns/nameserver_fixture_test.go` | Test-only explicit IPv4 socket ownership; diagnostic wildcard mode remains opt-in. This is not a production DNS or QUIC retry-policy change. Earlier diagnostic failures remain in the evidence log. |

Executed again at this runtime revision:

```sh
go test -race ./app/proxyman/outbound ./app/reverse ./proxy/vless/... ./common/buf ./app/observatory/... ./app/router ./core -count=1 -timeout=3m
```

Exit 0: outbound manager 11.752s, reverse 2.017s, VLESS encoding 2.495s,
buf 3.119s, classic observatory 3.364s, burst 4.493s, router 4.385s and core
41.572s. VLESS subpackages reporting no test files are not standalone protocol
acceptance; the earlier whole-Core scenarios and chained VLESS tests supply
that complementary evidence. The whole-Core Darwin run at `b6769bbb` has the
same production runtime and is recorded in outbound-current-audit.md.

Sibling Sidecar's four `TestAnyTLSOutbound*` tests also passed three race rounds
(16.458s) using `ANYTLS_STRICT=1` and the candidate local-Core modfile. This proves
candidate egress/control/display/aggregation integration, not advancement of its
production Core pin or deployment.

### Registry and construction review (2026-09-28)

`core.AddOutboundHandler` retains caller ownership until successful publication.
On manager rejection it now invokes Close. The independent cleanup fixture uses
the real object factory and proxyman handler wrapper, with a protocol-owned
worker and explicit close count. Duplicate tag and closed-manager rejection
both join the newly constructed worker exactly once without replacing/closing
the original handler; instance shutdown closes that original. This passed 100
race rounds (2.197s), separate from AnyTLS native duplicate-accounting tests.

`Handler.ValidateSender` is optional and checked before protocol construction;
existing protocol configs without that method keep their construction path.
The optional Retirement interface preserves historical removal behavior for
legacy protocols. Manager publication now follows successful Start, and Close
snapshots handlers including draining retirees before releasing the registry
lock to close them. No network close is added under that lock. Existing reverse,
mux and non-AnyTLS protocol suites remain necessary regression gates; the new
fake-protocol cleanup test does not replace native AnyTLS lifecycle races.

### Shared readv terminal errors (2026-09-28)

The expanded Core -> relay -> sing-box/Mihomo test exposed stalled relay
shutdown and failed first requests after external-server restart. A standalone
TCP SetLinger(0) reproducer (`common/buf/TestReadvReturnsConnectionReset`)
failed before the fix: the reset became EOF instead of ECONNRESET. Relay stacks
also remained in RawConn.Read. A readv-disabled differential passed three times;
disabling readv is not the fix or the acceptance configuration.

The internal multiReader interface now returns a terminal error separately from
the negative would-block sentinel. POSIX/illumos retry EINTR immediately and only
return the sentinel for EAGAIN; Windows uses WSAEWOULDBLOCK. ReadVReader returns
other errors without sending them back to the poller and clears/releases its
buffers on the error path. No copy, routing, pooling or retry algorithm changes.

Affected callers are all transports selected by buf.NewReader's readv path,
including freedom and relay protocols, not only AnyTLS. Preserve successful
adaptive reads, clean EOF, transient readiness polling and statistics. The
independent mock-poller contract covers data/EOF/reset and would-block followed
by data/reset; the real TCP reset plus existing readv tests pass 100 race rounds.
Windows/amd64, Linux/amd64 and illumos/amd64 test compilation passed; these are
compile checks, not runtime claims. Full Core race regression remains required
after this shared delta. Do not characterize earlier full-suite runs as testing it.
The existing isolated Linux/arm64 container also ran the real reset and readv
contract suite for 100 race rounds (4.493s, exit 0, no network/host changes).

### Timeout reader cancellation forwarding (2026-09-28)

Separate Core processes exposed a shutdown deadlock with active UDP dispatches:
Client.Close correctly waited for its copy workers, but the dispatcher's
TimeoutWrapperReader erased the underlying reader's Interrupt/Close capability.
An independent four-case test reproduced this without AnyTLS: normal blocked
read and already-timed-out read, each with Interrupt-only and Close-only readers.
All four failed before the fix; 100 race rounds passed afterward (1.895s).

TimeoutWrapperReader now forwards Interrupt using common.Interrupt, matching
BufferedReader's existing convention. It does not touch buffered data, counters,
timeout channels or read state from the cancellation goroutine. No new Close
method, lock, timeout extension or protocol-specific unwrapping is introduced.
Affected wrappers are dispatcher user-statistics readers and TUN readers, not
only AnyTLS. Read/statistics behavior must remain unchanged, and cancellation
must reach the same underlying resource. The independent-process shutdown gate
retains its five-second bound; whole-Core regression must cover this shared fix.

### Byte and packet reader interruption (2026-09-28)

The expanded independent-process relay-cut fixture exposed the same cancellation
erasure below TimeoutWrapperReader: dokodemo's DispatchLink supplies SingleReader,
ReadVReader or PacketReader, which previously exposed neither Interrupt nor Close.
AnyTLS joins its copy workers, so after peer failure the uplink worker could remain
blocked on a client that sends no more bytes. Merely extending a local timeout is
not a fix.

All three wrappers now forward explicit Interrupt to the wrapped reader, matching
BufferedReader. They still do not implement Close and their constructors do not
take resource ownership. No read state, buffers or counters are modified by the
cancellation method. This shared change affects explicit cancellation by other
protocols too; whole-Core regression is required before release acceptance.

Independent fixtures cover real TCP/readv, net.Pipe single reads and UDP packet
reads, each with a Close-capable or Interrupt-only underlying reader. All six
failed before the fix (4.633s for SingleReader/ReadVReader, 2.524s PacketReader).
Afterward 100 race rounds passed (1.637s), plus common/buf race (1.459s) and vet.
No read deadline is set; timeouts cannot satisfy the cancellation oracle. Even
failed tests close and join their own readers, and explicitly check that generic
Close on the wrappers does not acquire ownership.
ReadV's readiness barrier observes an actual would-block syscall result; the
single/packet fixtures signal entry just before calling the underlying Read.
The latter prove forwarding and termination, but not the exact scheduler instant
at which the OS read became blocked. Independent review found no new read-state
race or concrete shared-listener ownership regression; dokodemo UDP interruption
targets its per-flow reader, not the listening UDP socket.

### Burst connection cancellation review (2026-09-28)

`app/observatory/burst/ping.go` now wraps each tagged connection with a child
dispatch-context cancellation on Close. A real AnyTLS silent-TLS reproducer
showed the HTTP timeout finishing while protocol admission stayed pending until
the two-second Core handshake timeout. Pipe close alone cannot interrupt an
admission that has not reached the copy loop. The child uses transport dial
context values and an observer-parent cancellation callback; closing it neither
cancels sibling probes nor the observer nor AnyTLS's physical pool. No global
tagged dialer, dispatcher or shared CNC connection was changed.

Affected callers are all burst-observatory protocols. The independent
`TestContractProbeConnectionOwnsDispatchContext` passes 100 race repetitions;
existing Observatory/Router race suites and real AnyTLS UDS/timeout/scheduler/
balancer tests pass. Classic observatory is unchanged. Final whole-Core and
Sidecar regressions must include this delta before release acceptance.

Require OT-01..14, B-01..08 and C-01..07 evidence and both external interoperability
directions. Existing Core specs002..007 remain regression contracts, not rewritten
as AnyTLS behavior. Full-suite failures/skips require explanation; a narrow green
package does not establish whole-repository correctness. Public distribution remains
outside the accepted GPL-engine internal-pilot boundary. Deployment is separately
authorized and follows configuration-repository SOP, never implied by test success.
