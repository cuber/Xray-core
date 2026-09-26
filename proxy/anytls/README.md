# AnyTLS Inbound

Local fork implementation. Inbound only; no AnyTLS outbound or subscription
renderer is registered. Production rollout is tracked separately in
`xray-config/spec/003-anytls-inbound/`.

Use standard Xray inbound fields and `streamSettings.network: "raw"`,
`security: "tls"`, and normal `tlsSettings.certificates`. Plaintext, REALITY and
non-TCP transports are rejected, including control-plane creation.
Creation also validates the configured TLS certificate/private-key pairs before
publishing a listener. Empty or malformed server credentials fail both startup
and native gRPC AddInbound without leaving a handler or listening socket.

Protocol settings:

```json
{
  "clients": [{"email": "example", "password": "<unique-secret>", "level": 0}],
  "maxSessions": 256,
  "maxSessionsPerUser": 0,
  "maxStreamsPerSession": 128
}
```

An empty client list is valid but accepts no user until HandlerService adds one.
Email and password must both be nonempty and unique within the inbound.
`paddingScheme` optionally supplies validated protocol padding lines. Zero or
omitted session/stream limits use finite defaults. `maxSessionsPerUser: 0`
(or omitted) disables only the per-user limit; the inbound session cap still
applies. Explicit positive per-user limits remain enforced. Per-user session limit rejections emit a
warning at most once per five seconds per inbound, including the user, session
limit and idle session count, but no credentials. A separate inbound-wide limit of 128 active
handlers reserves 256 KiB per handler for protocol buffering. It is not a process
RSS limit and excludes dispatcher/TLS/runtime allocations. Admission credits are
released only after handlers exit.

AnyTLS clamps each dispatcher pipe's policy threshold to at most 32 KiB,
including unlimited policies; smaller nonnegative limits are preserved. This
does not change other protocols. UoT targets share a separate 128-link limit per
inbound, in addition to the per-association limit. Failed dispatch releases its
credit immediately; established links release it only after the response worker
exits and both pipes are interrupted, not when the target map entry is removed.
At most 128 TCP links plus 128 UoT links yield a conservative 16 MiB aggregate
pipe threshold. Pipes may exceed their threshold by a write batch; this number
is not a hard memory/RSS limit and excludes outbound teardown, TLS and runtime
overhead. Saturation rejects the new association, without closing other ones.

The existing HandlerService supports Add/Remove/ListInbounds and dynamic user
Add/Remove/Get/List/Count with `anytls.ServerConfig` and `anytls.Account` typed
protobuf messages. Removal invalidates the account generation and closes its
existing sessions; reusing an email or password never reactivates old sessions.
These API operations do not persist configuration.

Each authenticated connection uses a 30-second Mux-style idle monitor. It retires
the connection only when consecutive observations have zero active stream
handlers and the cumulative stream creation count has not changed (normally
30-60 seconds after the last handler exits). Heartbeats/padding do not count as
business activity. Retirement blocks new streams under the admission lock;
credits are returned only after protocol and handler teardown. Active streams
retain their normal policy inactivity timeouts. FIN behavior is unchanged.

Each multiplexed stream has independent routing state and authenticates as its
user's email. Existing inbound, user and domain traffic statistics apply without
protocol-specific counters. Inbound counters observe post-TLS protocol bytes;
user/domain counters observe dispatcher payload. Default freedom private-address
blocking applies to AnyTLS as it does to the other proxy inbounds.

UoT v2 carries UDP over the TCP session. Each target receives its own dispatcher
link, with at most 64 active targets per association and policy-driven idle
reclamation. Payloads of 1..8192 bytes are supported; zero and larger datagrams
are rejected rather than silently truncated by the existing UDP stack.

## Tests

```sh
go test -race ./proxy/anytls/... ./infra/conf ./app/proxyman/inbound ./proxy/freedom
go test ./proxy/anytls/internal/engine -run '^$' -fuzz FuzzServerFrames -fuzztime 60s -fuzzminimizetime 1s
```

Set `ANYTLS_SINGBOX` and `ANYTLS_MIHOMO` to independent client binaries to enable
`TestExternalClients`. Optional `ANYTLS_STRESS_DURATION=10m` runs 50 streams per
client, against distinct users of one Core. The same fixture verifies Hy2 UDP
and AnyTLS TCP on the same numeric port. Test listeners bind only to loopback;
temporary client files use mode 0600 and are removed on exit.

`TestAnyTLSUoTResourceRounds` exercises 128 real UDP targets across two users,
overflow rejection, revocation and full credit recovery over warmed rounds with
an explicit amd64-equivalent policy. `TestAnyTLSUoTSlowConsumerRevocation` checks
unread responses with an unlimited user buffer policy. These isolated IPv4
fixtures bind outbound source to loopback to avoid macOS dual-stack ephemeral
port overlap with unrelated local UDP applications. External-client stress uses
different exit markers and verifies exact per-user uplink/downlink deltas.

Sniffed IP requests retain the dispatcher's existing attribution boundary: the
first upload can be recorded as `unknown` before sniffing resolves a domain;
subsequent data uses that domain. AnyTLS does not retroactively move those bytes.

The pinned protocol engine retains its original GPL-3.0-or-later license and
source notice in `internal/engine/`. It must not be represented as MPL-only.
