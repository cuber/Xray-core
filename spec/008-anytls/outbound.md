# AnyTLS Outbound Extension

Status: locally implemented and verified. Baseline: Core 4d9abecd. Final local
acceptance: [outbound-final-acceptance.md](outbound-final-acceptance.md).
Chronological evidence: [outbound-implementation.md](outbound-implementation.md).
Deployment and production dependency-pin updates remain separate release work.

## Scope and Source Evidence

Extend this spec with a native Xray outbound. Retain the implemented inbound and
its wire protocol. Use the existing pinned internal engine, not a second AnyTLS
implementation or a sing-box subprocess. Existing licensing restrictions remain.

Local references inspected on 2026-09-27:

| Checkout | Revision | Relevant files |
|---|---|---|
| sing-box | 39e4476e3be6335a6ae9527467ffe3c8a32eeb22 | protocol/anytls/outbound.go |
| Mihomo | f103639c808d93a2c34cae56757b458862871b22 | adapter/outbound/anytls.go, transport/anytls/ |
| Core | 4d9abecd | proxy/anytls/internal/engine/client.go, app/proxyman/outbound/handler.go, proxy/trojan/client.go |

Both reference clients use native AnyTLS sessions and UoT v2. Sing-box clears
business metadata for multiplex dialing, but its helper does not detach parent
cancellation. Mihomo exposes
disable-reuse; neither client's field naming replaces Xray's configuration model.
Core's existing engine already implements client framing and idle reuse. Adapt
that engine and test it independently against external implementations.

## Configuration Contract

Use a single endpoint per outbound, like current Trojan. Multiple servers belong
in separate outbounds and existing routing balancers. Canonical JSON proposal:

```json
{
  "tag": "example-anytls",
  "protocol": "anytls",
  "settings": {
    "address": "server.example",
    "port": 443,
    "password": "replace-me",
    "level": 0,
    "idleSessionCheckInterval": 30,
    "idleSessionTimeout": 30,
    "minIdleSession": 0
  },
  "streamSettings": {
    "network": "raw",
    "security": "tls",
    "tlsSettings": {"serverName": "server.example"}
  }
}
```

- Add ClientConfig protobuf using protocol.ServerEndpoint and existing Account.
  Do not renumber ServerConfig or Account fields. Settings use camelCase.
- Address, port 1..65535 and nonempty password are required. JSON and native
  protobuf creation must enforce equivalent validation, including null accounts.
- RAW/TCP plus TLS only; reject cleartext, Reality, Vision, incompatible transports
  and enabled outer Xray mux. Native reuse needs no mux.enabled configuration.
- TLS verification/SNI/fingerprint and socket options remain in streamSettings.
  Dial through the supplied Core dialer, preserving chaining and outbound stats.
- Idle values are uint32 seconds. Omitted/0..5 select 30 seconds, matching the
  reference engine; 6..4294967295 are literal seconds without duration overflow.
  Negative/overflowing JSON values fail. minIdleSession defaults zero and accepts
  0..2147483647 (portable signed-int range). It retains existing idle sessions,
  not pre-dialing; it does not cause that many sessions to be created.
- Optional email is local account metadata, not a transmitted username. Level is
  local policy selection and does not overwrite the inbound user's stats identity.
- Password is opaque; never parse it as username/password or log it. Local inbound
  user identity remains independent of the remote account selected by this outbound.

## Required Runtime Behavior

O-01: Implement proxy.Outbound.Process for TCP and UDP. TCP copies business bytes;
UDP uses UoT v2 over AnyTLS, preserving packet boundaries and reply source. No UDP
direct fallback. Match inbound's current 8192-byte payload limit; reject oversized
packets explicitly rather than truncating them. Preserve domain destinations.

O-02: One engine pool per outbound handler. Sequential streams reuse healthy idle
sessions. Concurrent active requests must follow the engine's actual policy, not
claim all traffic uses one socket. A finished logical stream cannot close another
request's session. Idle means no active streams. Check/timeout defaults are 30s;
peer closure may end a session sooner. No invented heartbeat or TLS 0-RTT promise.

O-03: Pool lifetime belongs to the handler. Clone mutable session metadata for
physical dialing; never retain the first user's mutable outbound/inbound objects.
Request cancellation must stop that stream and pending dial, but not an already
pooled session used by another request. Handler Close cancels pending operations,
closes active and idle sessions, stops timers, and is idempotent.

O-04: Preserve the reference engine's idle-session selection. Do not introduce a
global capacity, waiting queue or active-session packing algorithm without separate
evidence and approval. Pool locks protect state transitions, not network dialing
or teardown callbacks. Test cleanup and growth across completed load rounds; do
not claim a configured connection cap exists. Xray mux MaxConnection is a worker's
cumulative stream retirement threshold, not a global socket limit.

O-05: Retry only before business payload is handed off, with a bounded attempt
count. Never replay application writes after an uncertain failure. Broken idle
sessions are discarded. Wrong credentials, TLS failure, malformed peer frames,
timeout and remote reset must not leak sockets or poison subsequent good requests.

O-06: Preserve existing accounting boundaries. Outbound tag counters measure the
connection passed through Core's statistical dialer, including AnyTLS framing.
Inbound/user/user+domain counters retain logical-flow semantics. Do not add manual
payload counters that double-count. Two users sharing one outbound remain distinct
locally; the remote server sees the configured outbound account, not both users.

O-07: Native HandlerService AddOutbound/RemoveOutbound and runtime enumeration
must accept/decode ClientConfig. Invalid creation leaves no published handler.
Removing/recreating a tag cannot reuse the old pool. Existing inbound user APIs
remain unchanged; outbound credentials are not managed through AddUser.

Removal retires rather than forcibly closes the pool: stop new admissions, cancel
unestablished operations, close idle sessions, and let admitted business streams
finish. Returned sessions close instead of becoming idle; minIdleSession does not
retain retired connections. A late dial cannot publish into a retired pool.
Implement an optional retirement capability without changing other protocols'
removal behavior. Full Core shutdown must also close still-draining old handlers.
Handler Close remains an immediate, idempotent shutdown operation, not retirement.

O-08: Existing routing, balancers and observatory can select this outbound without
protocol-specific route changes. Sidecar must identify anytls and read counters
and control-plane config without credentials appearing in normal status output.

## Implementation Sequence

1. Add protobuf/client validation/JSON registration and invalid-config tests.
2. Add isolated pool/dialer/lifecycle adapter and TCP tests, including chain context.
3. Add UoT adapter with packet/source/boundary tests.
4. Exercise real gRPC management, dispatcher statistics and observatory integration.
5. Test external-server interoperability, run regressions and build both platforms.
6. Record exact commands, revisions, results and residuals here. Deploy separately.

## Acceptance Matrix

Every row is required; current evidence and residuals are recorded separately. Tests must assert results, not merely
successful construction. Use local temporary certificates/ports and no production
8080, LaunchAgent, routing or credentials. External binaries are test-only.

| ID | Scenario | Required evidence |
|---|---|---|
| OT-01 | JSON/proto defaults, bad ports/accounts/security/mux, round trip | Table tests; same rejection via gRPC |
| OT-02 | Core outbound to Core, sing-box and Mihomo servers | Exact random TCP upload/download hashes; independent external-server evidence |
| OT-03 | 100 sequential streams, concurrent streams, peer idle expiry | Accepted socket counts prove reuse; no cross-stream corruption |
| OT-04 | First request cancellation then second user's request | Surviving pool/stream works; first user's context and counters not inherited |
| OT-05 | Idle timeout, active stream, minIdleSession | Controlled-clock tests; active traffic not reaped; no pre-dial |
| OT-06 | IPv4/IPv6/domain UDP, multiple targets, 1/8192/8193-byte packets | Exact payload and reply endpoint; explicit oversize behavior, no truncation |
| OT-07 | Wrong password/cert/SNI, RST, malformed frames, slow peer | Bounded failure then successful fresh request; no replay or resource growth |
| OT-08 | Add/list/remove/re-add outbound using real gRPC | Correct protocol/config; old sockets/timers terminate; rejected add leaves no entry |
| OT-09 | Two users/two domains, reused session, TCP and UDP | Exact logical-byte deltas per user/domain; outbound wire deltas nonzero and not doubled |
| OT-10 | Stats toggles, domain traffic disabled/enabled, Sidecar reads | Existing API semantics unchanged; optional statistics really optional |
| OT-11 | Chained outbound/socket binding/observatory and balancer | Correct actual path/probe result, no bypass, no first-request context capture |
| OT-12 | Concurrent admission/retirement/cancel/Close, repeated reconnect | Race detector; no post-retirement admission, zero owned workers after shutdown |
| OT-13 | Existing inbound and other protocols | Targeted regression plus full-suite report, explicit skips/failures |
| OT-14 | Darwin/Linux builds and format/vet | Approved build wrapper, local executable version, recorded artifact identities |

Do not call the extension complete while any required row lacks evidence. Add
concrete test names/commands as implementation lands; historical inbound tests
alone cannot satisfy reverse-direction outbound interoperability.

Detailed execution methods and OB/chaining matrices: [outbound-tests.md](outbound-tests.md).
Shared-code review and regression gates: [outbound-impact.md](outbound-impact.md).
Design audit and current unresolved implementation gates: [outbound-audit.md](outbound-audit.md).
