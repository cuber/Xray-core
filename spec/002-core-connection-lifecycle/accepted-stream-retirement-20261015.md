# Accepted TCP / Unix Connection Ownership

Runtime freeze: **2026-10-15 02:18:16 CST**. Candidate only; uncommitted and
not deployed. Ownership scope: inbound worker.go, always.go, new stream tests.
Other agents' UDP worker, timer, SS and AnyTLS changes are preserved.

## Reproduction and Attribution

Before this change an authenticated SOCKS TCP or Unix connection could complete
a fresh echo round trip after `core.Instance.Close()` returned. This is not the
intentional policy of keeping authenticated TCP alive after removing a user.

Original external Sidecar probe, race x10: both TCP and Unix failed 10/10.
`/tmp/sidecar-resume-close-owner-both.log`. A fresh pre-edit run is preserved at
`/tmp/sidecar-worker-before-red.log`:

```text
--- FAIL: TestDiagnosticTCPInstanceCloseOwnership
    --- FAIL: TestDiagnosticTCPInstanceCloseOwnership/tcp
        closed Core instance still forwards accepted TCP traffic
    --- FAIL: TestDiagnosticTCPInstanceCloseOwnership/unix
        closed Core instance still forwards accepted TCP traffic
```

The tracked Core equivalent is
`app/proxyman/inbound/stream_close_contract_test.go`:
`TestStreamInstanceCloseStopsForwarding`. It uses real Core, SOCKS and echo
sockets, not just configuration shape or mocked dispatch.

Per-test sampling after fixture cleanup attributed the prior Sidecar FD250/g566
baseline to chain TCP/UDS callbacks: 16 pure-TCP pairs added 192 FDs, four UDS
pairs added 48, explicit UDS/VLESS/Hy2 added four, plus one transient UDP FD.
Stacks showed Freedom copy tasks, pipes and inbound callbacks. An isolated
SOCKS/SOCKS pair retained 12 FDs after Close, then retired under half-close
policy by one second. The one-second observation was NOT added as a test fix.
Details: `/tmp/sidecar-resume-resource-handoff.md` and per-pair/profile logs there.

## Ownership Contract

- Each TCP/Unix worker owns its listener and accepted connections. Registration
  occurs synchronously before spawning Process. Map keys are entry pointers,
  never the dynamic Connection value (which may be non-comparable).
- Admission, Start and listener publication share a gate. Duplicate/concurrent
  Start is rejected. Close racing Listen waits for the late listener to be
  closed, not published. No blocking I/O or Wait executes under the gate mutex.
- Stop detaches accepted entries, cancels all their contexts, closes sockets
  even when Process ignores cancellation, and closes the listening hub.
- AlwaysOnInboundHandler owns the shared proxy. It stops ALL workers, calls
  protocol Close exactly once, then joins stream callbacks. UDP worker still
  closes its owned active pipes/hub/checker; it no longer closes the shared proxy.
- Partial listener Start failure uses the same handler-owned rollback. Concurrent
  handler Close callers wait for the same completion. Worker Close alone never
  takes ownership of the shared protocol.
- TPROXY/Redirect self-loop detection still compares the original destination
  to the LISTENER address. It never substitutes accepted conn.LocalAddr, which
  can itself be the legitimate original target. Listener publication is awaited.
- Request context and Unix network classification are preserved. RemoveUser is
  unchanged; the Sidecar live SS usersync gate checks established TCP survives
  user removal while subsequent authentication is rejected.

## Lock and Reentrancy Review

Core.Close continues to hold statusLock; this patch does not change that global
lifecycle design. The new waits invoke no Instance.IsRunning/Start/Close method.
Built-in paths reviewed: SOCKS/Trojan use socket/pipeline task termination;
SS2022 closes protocol UDP sessions; VLESS closes decryption/validator and
reverse outbound handlers; AnyTLS closes its connection set then joins its own
workers. Mux server Close is a no-op. These paths do not reenter Core.statusLock.
Protocol Close precedes Process joins to avoid waiting on protocol-owned work
which needs Close to terminate. Calling handler.Close recursively from its own
protocol.Close is not supported; no such built-in path was found. This is not a
claim about arbitrary third-party Process implementations which ignore both
context cancellation and closed I/O indefinitely.

Transparent tests cover the shared Redirect predicate and the actual TPROXY
worker processing branch with a socket reporting an original LocalAddr. They
do not claim privileged Linux REDIRECT/TPROXY kernel interception coverage on
Darwin.

## Validation

`stream_retirement_test.go` covers non-comparable connections, context retention,
ctx-ignoring reads, callback join, concurrent Close, late admission, late Listen,
duplicate Start, TPROXY/Redirect addresses and shared TCP/Unix/UDP/unstarted-worker
proxy Close count exactly one. A protocol which requires Close before Process
returns verifies stop/protocol-close/join ordering.

```sh
GOTOOLCHAIN=go1.27.0 go test -race ./app/proxyman/inbound -count=20 -timeout=180s
```

PASS **23.509s**, `/tmp/core-worker-owner-race.log`, on frozen runtime.
Earlier focused x20 PASS 2.217s and package x3 PASS 4.643s predate the shared-owner
refinement. First synctest attempts timed out because the fake proxy returned a
close error, starting the process-global logger inside its virtual-time bubble;
the fake now returns nil after observing socket retirement. No runtime timeout
or assertions were relaxed.

Sidecar full race and chain/churn on final owner ordering are being recorded
separately. An earlier post-fix full race passed 46.210s and churn baseline
dropped to FD5, but churn race x3 detected an independent UDP dispatcher
`connEntry.closed` data race (terminate write :39 vs getInboundRay read :80).
Evidence `/tmp/sidecar-worker-churn-race.log:2206`; that failure remains a gate,
not waived by a subsequent green run. No UDP dispatcher change was made here.

Final unified commit, official dual-platform wrapper build and final binary
compatibility retest remain with the coordinated release gate, not this source
freeze signal.

Subsequent integration exposed a Hy2 feed callback lock cycle and premature
stream-EOF barrier relative to QUIC retirement. The provider-owned correction,
baseline-red tests and unchanged AnyTLS recovery fixture results are recorded in
[Hy2 listener retirement](hy2-listener-retirement-20261015.md). That increment
does not alter worker/always source and still requires final serial acceptance.
