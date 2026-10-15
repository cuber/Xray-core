# Hy2 Loopback UDP Source Binding

Candidate-only follow-up to the final `aa6ca3ed` churn failure, executed serially
on Darwin/arm64 with Go 1.27.0. Runtime baseline is `aa6ca3ed`; later main-owned
HEAD changes include documentation. No build, commit, pin update or deployment
was performed here. The only runtime edit is
`transport/internet/hysteria/dialer.go`; the previous listener/UDP retirement
changes remain intact. Temporary tracing has been removed from runtime.

## Failure and Attribution

The original unchanged Sidecar 39-batch/1000-chain race gate timed out after four
seconds at `batch36/hysteria/socks`, with zero via and target uplink. Evidence:
`/tmp/sidecar-final-aa6c-churn-1000.log`, lines 8834-9237. Client uTLS was waiting
for ServerHello and server TLS was waiting for ClientFinished. Both QUIC run
loops were waiting for events. The cleanup goroutine waiting for the client
lock was a consequence of pending authentication, not a demonstrated deadlock.

An independent real authentication test,
`TestHy2ChromeParrotFreshListenerHandshake`, failed naturally on repetition 148
(test 4.01s, package 5.887s). Its bounded QUIC trace is retained in
`/tmp/core-hy2-handshake-focused-trace.log`, lines 738-836:

| Side | Local endpoint | Remote endpoint |
|------|----------------|-----------------|
| Client | `0.0.0.0:53920` | `127.0.0.1:53920` |
| Server | `127.0.0.1:53920` | `127.0.0.1:53920` |

The wildcard UDP client had been allocated the same port as the specific
loopback listener. The server received every ClientHello retransmission and
sent Initial/Handshake replies. The client recorded no received packet. Both
sides' PTO timers fired repeatedly, disproving a stalled retransmission timer
in this reproduction. Reply delivery to the specific listener instead of the
wildcard socket explains the stalled client handshake.

**Inference boundary:** the original batch36 log did not record its client UDP
local endpoint. Matching symptoms, the independent endpoint trace, deterministic
collision injection and unchanged-gate success support the same-cause inference;
they do not retrospectively prove the original local port value.

A separate control drops exactly the first Server Initial. With ChromeParrot
and trusted fixture TLS, authentication recovered in about 230ms. One ordinary
lost Initial therefore does not explain the observed four-second stall:
`/tmp/core-hy2-lost-initial-baseline.log`.

## Narrow Repair and Contracts

Before a direct system dial to a literal loopback IP, choose its canonical
loopback source (`127.0.0.1` or `::1`) when no explicit Gateway is present. This
makes ephemeral allocation check the same concrete local address as the
listener. Copy the outbound slice and its final Outbound before setting Gateway;
do not modify shared request metadata.

- Explicit Gateway, including IPv6, is preserved.
- A configured dialerProxy bypasses this source adjustment for both IP families.
- Non-loopback destinations and unresolved domains are unchanged. This is not a
  repair for every domain-resolved or non-loopback local-address collision.
- Request cancellation and Core owner remain inherited from the original
  context; no detached context or lifecycle operation is introduced.
- No retry, timeout increase, ChromeParrot disable, global cleanup or change to
  Sidecar fixtures is part of this repair. Windows runtime remains deferred.

`loopback_dial_test.go` checks source selection, preservation of explicit
IPv4/IPv6 Gateway, parent metadata, owner and cancellation, and unchanged
IPv4/IPv6 dialerProxy contexts. The source-selection test fails against the
original runtime for IPv4/IPv6: nil source instead of concrete loopback.

## Deterministic Wire Injection

`loopback_collision_darwin_test.go` fixes the endpoint condition with two real
UDP sockets: wildcard and concrete loopback on one port. **SO_REUSEPORT is used
only by this fixture to establish that collision deterministically.** It does
not claim to reproduce the kernel's natural ephemeral-allocation sequence and
does not falsify LocalAddr. The original unforced repetition-148 failure is
separate evidence that the condition also occurs without this injection.

The alternative dialer returns the actual colliding wildcard socket when the
caller supplies no source; with a selected source it delegates to the real
DefaultSystemDialer. Both branches retain ChromeParrot and use a dedicated
RootCAs pool with the httptest certificate and verified `127.0.0.1` identity.
The fixed branch requires exactly one actual HTTP/3 auth request, a concrete
loopback source, and a client port distinct from the listener's port.

The same wire fixture with the original dialer failed at four seconds with zero
auth requests. The fixed source passed in about 20ms. Baseline execution used
`/tmp/core-hy2-loopback-baseline-overlay.json` and the original source exported
to `/tmp/core-hy2-loopback-aa6c-dialer.go`; no worktree checkout/reset occurred.
The overlay blanks only the newly added helper-specific test file, which refers
to a helper absent from the original runtime. Existing tests and the wire test
are unchanged by the overlay.

## Executed Validation

| Gate | Result | Log |
|------|--------|-----|
| Natural independent auth reproduction, baseline | FAIL on repetition 148; package 5.887s | `/tmp/core-hy2-handshake-focused-trace.log` |
| Source contract, baseline | IPv4/IPv6 FAIL; package 0.525s | `/tmp/core-hy2-loopback-source-baseline-red.log` |
| Source contract, fixed race x20 | PASS 1.540s | `/tmp/core-hy2-loopback-source-fixed.log` |
| Previously unexecuted context test, race | PASS 1.591s | `/tmp/core-hy2-loopback-context-final.log` |
| Same-port wire injection, original runtime | FAIL 4.519s; deadline, auth requests=0 | `/tmp/core-hy2-same-port-wire-baseline-red.log` |
| Same-port wire injection, fixed | PASS 1.610s; test about 20ms | `/tmp/core-hy2-same-port-wire-fixed.log` |
| Real fresh-listener handshake, fixed race x1000 | PASS 9.929s | `/tmp/core-hy2-handshake-focused-fixed.log` |
| Unchanged Sidecar 39-batch/1000-chain race | PASS 22.081s | `/tmp/sidecar-hy2-loopback-fixed-churn-1000.log` |
| Complete Hy2 directory race, including latest tests | PASS: Hy2 13.191s, BBR 2.018s, Brutal 1.459s | `/tmp/core-hy2-loopback-package-race.log` |
| Hy2 directory vet | Exit 0, no diagnostics | `/tmp/core-hy2-loopback-vet.log` |
| gofmt on all four changed/new Go files; scoped diff check | No output, exit 0 | Executed locally |

Final package commands, run serially:

```sh
GOTOOLCHAIN=go1.27.0 go test -mod=readonly -race -count=1 -timeout=90s \
  ./transport/internet/hysteria/...
GOTOOLCHAIN=go1.27.0 go vet -mod=readonly ./transport/internet/hysteria/...
```

The complete package race run includes both latest IPv6 Gateway and dialerProxy
cases, the new wire test and existing authentication/UDP/retirement tests.
The existing 1000-handshake and 1000-chain passes were not repeated in this
follow-up. Earlier diagnostic attempts that passed did not close the failure:
single-pair x300 `/tmp/sidecar-hy2-handshake-repro-aa6c.log`, debug 39-batch
`/tmp/sidecar-hy2-churn-diagnostic-aa6c.log`, and two bounded trace runs
`/tmp/sidecar-hy2-churn-{bounded,thin}-trace.log`.

Go source is frozen after these tests. Main owns review, final commits/pins,
official builds and subsequent broad acceptance. No full scenarios, AnyTLS or
additional Sidecar root suite was started in this follow-up. FD5 and a warmed
goroutine plateau alone are not a proof of absence of resource retention.
