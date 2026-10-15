# Hy2 Listener Retirement and Recovery

Runtime frozen **2026-10-15 02:47:29 CST**. Candidate only; no commit, deployment
or pin update by this agent. Scope: transport/internet/hysteria/hub.go, conn.go,
and listener_retirement_test.go. Existing UDP closed atomic changes are retained.

## Contract Review

AnyTLS outbound_chain_fault_test.go does NOT restart a closed handler. It reads
the hop configuration, RemoveInbound closes/unpublishes the old handler, and
AddInbound(config) constructs a fresh handler/proxy/workers via core.CreateObject.
Manager.AddHandler starts the new object before publication. AddInbound failures
are fatal in the fixture, before recovery requests. Thus repeated recovery EOFs
are not evidence of the new worker's repeated-Start rejection.

The real change in observation is that accepted-stream retirement promptly
produces a stream EOF. Previously that barrier could be delayed by orphaned
connections and idle expiry. Stream EOF does not establish QUIC connection
retirement at the peer. Existing Hy2 Listener.Close called Transport.Close
without first closing its accepted QUIC connections. quic-go transport.go:475
explicitly documents that Transport.Close aborts locally WITHOUT sending
CONNECTION_CLOSE. The Hy2 client can therefore retain an apparently active
cached connection to the old listener, even after a replacement listener starts.

## Minimal Provider Fix

- Listener owns accepted QUIC connections under an admission gate. Close stops
  publication, joins the accept loop, calls CloseWithError on owned connections
  while the packet transport is still open, joins ServeQUICConn handlers, then
  closes transport and UDP socket. Concurrent Close is idempotent.
- No registry lock is held across QUIC close, HTTP3 join, or packet I/O.
- UDP feed publishes the session and enqueues its first datagram under lock,
  then invokes addConn outside the lock. A synchronous callback can reject and
  Close its InterConn without reentering the same manager lock.
- Both feed lookup/publication paths reject a closed manager. If Close wins
  after publication but before callback completion, the callback observes a
  retired connection and cannot revive it.
- No AnyTLS retry, fixture timeout, acceptance assertion, protocol selection,
  global cleanup or worker contract was changed.

## Red / Green Evidence

Original unchanged two-entry fixture, normal:
`/tmp/core-hy2-worker-recovery-before.log`, FAIL 12.705s. Both
migratedProxySettings and dialerProxy/hysteria-close-barrier report EOF for
after and after-second. Also matches Harvey's v6 targeted JSONL.

Independent new tests against old hub/conn via external overlay:
`/tmp/core-hy2-retirement-baseline-red.log`, FAIL 3.570s:

```text
peer still treats retired listener as live; no CONNECTION_CLOSE
feed invokes callback while holding manager lock
synchronous rejection did not retire session
```

The concurrent Close test also exposes the old duplicate UDP socket Close error.
Overlay: `/tmp/core-hy2-retirement-red-overlay.json`. Original source snapshots
are `/tmp/core-hy2-{hub,conn}-before.go`; no checkout/reset was used.

New tests prove remote ApplicationError.Remote (actual received CONNECTION_CLOSE,
not local timeout), HTTP3 handler cancellation/join before Close returns,
concurrent Close, synchronous feed callback Close, publication/late-callback
shutdown and refusal of new sessions after manager Close.

```sh
GOTOOLCHAIN=go1.27.0 go test -mod=readonly -race ./transport/internet/hysteria \
  -run '^TestUpgradeHy2(ListenerClose|UDPFeed|UDPConcurrentWriteAndClose)' \
  -count=20 -timeout=60s
```

PASS **1.894s**, `/tmp/core-hy2-retirement-race20.log`.

```sh
GOTOOLCHAIN=go1.27.0 go test -mod=readonly -race -count=3 -timeout=90s -v \
  ./proxy/anytls \
  -run '^TestAnyTLSOutboundChainFaultReceipts/(migratedProxySettings|dialerProxy)/hysteria-close-barrier$'
```

PASS **14.119s**, `/tmp/core-hy2-worker-recovery-race3.log`. Both first and second
new recovery IDs succeeded each round. Receipts show before/held/after/
after-second exactly once, no down receipt. Initial patched normal run also
passed 4.840s, `/tmp/core-hy2-worker-recovery-after.log`.

Tests were run serially and narrowly due to the reported local port/load
environment. Full Sidecar race/churn, whole-Core validation, long AnyTLS soak,
official builds and U007 final-binary checks belong to the subsequent coordinated
serial acceptance. No new full-suite claim is made here.

Main review additionally enables the production `acceptDone` wait branch in the
first fixture and asserts that the accept loop has exited when Close returns.
The fixture now trusts only its local httptest certificate through RootCAs and
checks its `example.com` identity. The first strict-TLS attempt omitted the QUIC
client ServerName and failed explicitly; it was corrected without disabling TLS
verification. The full targeted race x20 passes in 1.973s with these additional
checks in `/tmp/core-hy2-retirement-verified-race20-r2.log`.
