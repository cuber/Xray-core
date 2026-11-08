# Feature Specification: Hy2 Relay Sessions

- Feature ID: 024-hy2-relay-session
- Created: 2026-11-08
- Status: Approved (2026-11-08; user: "有没有办法能够让流量走同一个 hy2 session，然后不同outbonds也就是不同user能够复用同一个
  session"; "还是要用方案B，叠加我刚才说的建议"; "凭据应该还是 session 级别的，只是 user 路由的差异，这里 outbonds 可以考虑加一种
  relay 的模式"; "N 默认1吧；……可能还是限制最大上限就好了，例如256*4"; "1 没问题，2 一起做，3 是的，4 是的"; "凭据机制是不是还可以
  保留？相当于可以复用，增多一层密钥认证"; "开始吧，把草案变成正式的 spec"). Rollout is a separate step (xray-config spec026).
- Baseline: Core `31a83508` on `fork/2026-11-07/0888beb38a9b`, quic-go `0162af63` on `fork/2026-11-07/184d081eef3e`.
- Depends on: [spec021](../021-quic-loss-recovery/spec.md) (`GetQuicStats`, `quicrecovery`), spec014 (burst observatory
  `track`), and the quic-go fork's [spec002](../../../quic-go/spec/002-ack-progress/spec.md) (`ConnectionStats.PacketsAcked`
  and `BytesInFlight` for R5; user, 2026-11-08: "为啥不能改 fork，明显可以改啊").
- Consumers: [Sidecar spec020](../../../xray-sidecar/spec/020-hy2-relay-session/spec.md) (`/xray-debug` and metrics);
  rollout in [xray-config spec026](../../../xray-config/spec/026-hy2-relay-session/spec.md).

## Problem and Goal

An entry board that sends several route users to one relay over Hy2 has one outbound per user, and therefore one QUIC
connection per user:

- The Hy2 dialer caches clients by destination and the outbound's `MemoryStreamConfig` pointer
  (`transport/internet/hysteria/dialer.go`), so every outbound dials its own connection.
- The user is fixed at the connection's HTTP/3 authentication (`hub.go` `AuthHTTP` sets `h.user`); every stream and UDP
  session of the connection inherits it. A connection cannot carry two users.

On tgz.lite → isz5 (xray-config spec023) this gives eight independent connections. The burst observatory probes one of them,
which carries almost nothing, so it never sees the congestion window, pacing or queueing of the connections that carry
the traffic, and spec021's recovery parameters show in the probe only for a lone packet. Connections close after 30 s
idle without a keepalive: a low-traffic route opened 103 connections in six hours on 2026-11-08, each with a new
handshake, a new RTT estimate from `initialRttMs` and a new BBR startup. Eight BBR flows compete on one path.

Goal, all opt-in and off when unset:

1. a **session owner**: a Hy2 outbound that holds the session's credential, its transport settings and a pool of
   connections that grows and shrinks with load;
2. **relay outbounds** that send their traffic through the owner's pool as another user of the same server inbound,
   proving that user with its own credential once per connection;
3. the burst observatory's probe of the owner rotating over the pool's connections, so it measures the connections
   that carry the traffic and keeps them alive;
4. the pool, the relay bindings and the relay outbounds reported read-only.

## Contract

### R1 Configuration

Session owner, on a Hy2 outbound's `streamSettings.hysteriaSettings`:

| Field | Range | Meaning |
|---|---|---|
| `session.minConnections` | 1–16 | connections kept open |
| `session.maxConnections` | `minConnections`–16 | upper bound, draining and spare connections included |
| `session.streamsPerConnection` | 16–4096 | target streams per connection for scaling decisions; not a refusal limit |
| `session.scaleUpStreams` | 1–`streamsPerConnection` | average open streams per active connection at which the next connection is dialed |

An owner needs `quicParams.keepAlivePeriod` greater than 0 and smaller than its idle timeout (`maxIdleTimeout`, 30 s when
unset); otherwise the configuration fails.

Relay outbound: `{"protocol": "hysteria", "settings": {"version": 2, "relay": {"session": "<owner tag>", "auth": "<the user's
credential>"}}}`. The credential decides the user. `run -test` fails when:

- a relay outbound has `streamSettings`, an `address` or `port`, or no `auth`;
- `relay.session` names no outbound, an outbound that is not Hy2, or one without `session`;
- the owner is itself a relay outbound, or relay references form a chain;
- two relay outbounds of one session carry the same credential, or one carries the owner's;
- a session has more than 255 relay outbounds.

Server, on a Hy2 inbound client: `relay: ["<email>", ...]`, the users this client may relay for. Each email must be
another client of the same inbound; otherwise the configuration fails.

Without `session`, `relay` and the client `relay` list, configuration and behavior are exactly today's.

### R2 Relay negotiation and wire format

- **Negotiation in the authentication request, no extra round trip.** An owner whose session has relay outbounds adds
  `Hysteria-Relay: 1=<cred>,2=<cred>,...` to the HTTP/3 authentication request: an index from 1 to 255 per relay
  outbound, each credential base64url-encoded without padding. The order and the indexes follow the configuration.
- **The server answers each index** in the same header of the authentication response: `1=ok,2=denied,...`.
  - `ok`: the credential is a client of the inbound (authentication), and that user is on the connection user's `relay`
    list (authorization).
  - `unknown`: the credential is not a client of the inbound.
  - `denied`: the user is not on the connection user's list.
- **An absent response header** means the server does not relay (an older Core or another Hy2 implementation). A server
  that does not support relaying ignores the request header, so the connection still serves the owner's own traffic.
- **TCP.** A relayed stream starts with frame type `0x402`, a varint index, then the body of `0x401` (address length,
  address, padding length, padding). The response is the `0x401` response. The owner's own streams keep `0x401`. A server
  accepts `0x402` only on a connection that negotiated relaying, only for an index it answered `ok`; it resets any other
  such stream, so the client's request fails.
- **UDP.** A relayed UDP session's 32-bit ID carries the index in its top 8 bits and a sequence number in the low 24;
  index 0 is the connection user. On a relay connection the server takes each UDP session's user from its index.
  Sequence numbers skip IDs still in use when they wrap.

### R3 Server identity

- A relayed stream's or UDP session's `inbound.User` is the relayed user: routing `user` rules, per-user traffic
  statistics and `VlessRoute` behave as for that user's own connection. The QUIC connection's user stays the
  connection user.
- At each stream or UDP session opened, the server checks that the index's user is still a client of the inbound, so a
  user removed at runtime (`HandlerService.RemoveUser`) is refused at once on live connections.
- Relaying a user needs the connection user's credential, that user's credential, and the user on the connection
  user's `relay` list.

### R4 Connection pool

- **Placement.** Each TCP stream and UDP session chooses a connection when it opens and stays on it. It takes the healthy
  active connection with the fewest open streams.
- **Scale up.** When the average open streams per active connection reaches `scaleUpStreams` and the pool is below
  `maxConnections`, the owner dials one more connection in the background. New streams use the existing connections until
  it is authenticated.
- **Stream credit.** The hard limit is the server's advertised stream limit per connection (`maxIncomingStreams`, 1024 by
  default), not `streamsPerConnection`. A connection without stream credit is skipped. When none has credit, opening waits
  (`OpenStreamSync`) up to the handshake timeout, then fails.
- **Scale down.** When the open streams stay at or below (active − 1) × `streamsPerConnection` × 25% for 60 s, the active
  connection with the fewest streams becomes draining: it takes no new streams. It becomes spare when its last stream
  closes, and closes after 5 minutes as a spare. A rise in load reactivates a draining or spare connection before dialing
  one. Active connections never drop below `minConnections`.
- Flows never move between connections (non-goal); a long-lived stream keeps its draining connection open.
- A pool's connections all use the owner's settings, credential and relay list, and count into the owner's `QuicStatsSet`.

### R5 Failure handling

- A closed connection (idle timeout, stateless reset, CONNECTION_CLOSE, authentication failure) leaves the pool. The owner
  redials with exponential backoff (100 ms doubling to 5 s) and full jitter while below `minConnections` or under load.
- A connection with bytes in flight and no packet acknowledged for 1 s (quic-go spec002's `BytesInFlight` and
  `PacketsAcked`) is suspect: it takes no new streams while another healthy connection exists. Received packets are no
  sign of health: when only the owner's direction is lost, the server keeps retransmitting. When every connection is suspect, placement ignores the mark; the path-level
  observatory decides.
- With no live connection, opening a stream dials synchronously, as today.
- Streams on a connection that closes fail; nothing retries them.

### R6 Observatory

- The burst observatory marks the dials of its probes. The owner places a marked stream on the next active connection in
  turn, not on the least loaded one, so every connection is measured and kept in use.
- A relay outbound is reported by `track` like any tracked outbound, except that it is dead, with the reason in the
  observation and in `GetTrack`, while its binding is `unknown`, `denied` or `unsupported`. A balancer then falls back as
  for a dead probe. Before the first connection, the binding is `no_connection` and the relay outbound follows its probe.

### R7 Runtime report

`HandlerService.GetQuicStats` (spec021) gains, additively:

- on an owner's entry, `session`: `min`, `max`, `active`, `draining`, `spare`, `suspect`, `streams_open`;
- on each owner connection row: `state`, `streams_open`, and the relay bindings (index, user email, status);
- an entry per relay outbound (direction `outbound`, transport `hysteria`): `relay` with `session`, `user` (email) and
  `status` (`bound`, `unknown`, `denied`, `unsupported`, `no_connection`), and no counters of its own;
- on a server connection row, the relayed users it accepted.

No credential is ever reported; users appear by email. `xray api quicstats` prints the new fields; `-json` prints the
response as is.

## Measurements (2026-11-08)

`xrayctl hy2-overhead --throughput` (xray-config), one Hy2 connection, BBR, path MTU discovery off, no Chrome parrot,
Core `31a83508`:

| Case | Goodput | Client / server Core, share of one core | Per core |
|---|---|---|---|
| hk.akile.lite → la.akile.lite, 512 MiB up | 157 Mbps | 22% / 24% | about 720 / 640 Mbps |
| same, 512 MiB down | 153 Mbps | 21% / 20% | about 745 / 780 Mbps |
| same, SS-2022 ingress, 256 MiB each way | 187 Mbps | 31% / 26% | about 610 / 730 Mbps |
| hk.akile.lite over loopback, up | 469 Mbps | 40% / 39% (host 88%) | about 1180 / 1210 Mbps |
| same, down | 545 Mbps | 37% / 39% (host 86%) | about 1490 / 1390 Mbps |
| same, SS-2022 ingress, both ways | 408 Mbps | 44% / 36% (host 90%) | about 920 / 1140 Mbps |

The trans-Pacific rows are path-bound and extrapolated; the loopback rows saturate a 1 vCPU host (both Cores and the
test scripts on one core). One connection carries about 0.9–1.5 Gbps per core on a 2.5 GHz Xeon Gold 6133. The rollout
path's peak is about 107 Mbps (5-minute average), so the pool scales for streams and failure only, never for throughput.
At about 1.7 proxied flows per inbound TCP connection, the path's weekly peak is about 250 concurrent flows, which one or
two connections hold.

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | Without `session`, `relay` and a client `relay` list, the dialer key, the wire format and the server's behavior are today's; the existing Hy2 suites pass unchanged | existing suites; unit |
| FR-002 | R1 parsing, ranges and every refusal in `run -test`; the fields come back through `ListOutbounds` and `ListInbounds` | infra/conf unit |
| FR-003 | R2 negotiation: `ok`, `unknown` and `denied` per index; an older server (no header) gives `unsupported`; `0x402` accepted only for `ok` indexes; UDP IDs carry the index and wrap without reuse | transport unit |
| FR-004 | R3 on a started server: two relayed users and the owner's own user over one connection, TCP and UDP, each matched by its own `user` routing rule and counted in its own user statistics; `RemoveUser` refuses that user's next stream on the live connection | integration |
| FR-005 | R4 placement, scale up at `scaleUpStreams`, stream credit and the bounded wait, scale down after 60 s at 25%, draining and spare lifetimes, reactivation before dialing, `minConnections` and `maxConnections` honored | unit with a fake clock; integration |
| FR-006 | R5: a closed connection is replaced with backoff and jitter; a stalled connection is suspect and skipped while another is healthy; streams of a closed connection fail and new streams succeed at once | unit; integration |
| FR-007 | R6: probe dials rotate over active connections; a relay outbound with `unknown`, `denied` or `unsupported` is dead in the observation and in `GetTrack` with its reason | unit; started instance |
| FR-008 | R7 through a real gRPC server on a started instance: an owner with two connections and two relay outbounds, a server with relayed users; no credential in the response | CP-7 contract test |
| FR-009 | `xray api quicstats` text and `-json` with the new fields, against a fake server and an older Core | CLI test |
| FR-010 | netem (`testing/netem/run.sh`): 0 → 600 → 0 concurrent streams scale the pool to 4 and back to 1 with no stream failing for credit; closing one connection keeps the probe alive; a restarted server is reconnected within one RTT plus 500 ms; during a bulk transfer on the session, the probe's delay rises with the queue | netem |

## Control Plane

- **Configuration (CP-2).** `session`, `relay` and the client `relay` list come back through `ListOutbounds` and
  `ListInbounds`. The owner's `session.relay_auth` repeats the relay outbounds' credentials, which `ListOutbounds`
  already returns in their settings; the runtime report (R7) carries none.
- **Runtime state (CP-1, CP-2, CP-4).** The pool, its connections and the relay bindings through
  `HandlerService.GetQuicStats` (R7); the relay outbounds' observatory state through `ObservatoryService.GetTrack`.
  Both services exist and are enabled where spec021 and spec014 run, so no `api.services` change.
- **Read only and bounded (CP-3, CP-5).** No side effects; at most 16 connections per owner, 255 bindings per connection,
  256 connection rows per handler as in spec021. Users by email only.
- **Compatibility (CP-6).** Additive proto fields. An older Core reports neither `session` nor `relay`; consumers show
  them as unavailable, never as an empty pool. An older server is visible as `unsupported` on every relay outbound.
- **Tests (CP-7).** FR-008.
- **Consumers (CP-8).**
  - Sidecar spec020: `/xray-debug` `quic` passes the new fields through and exports pool and relay metrics.
  - xray-config spec026: renderer, the `hy2_relay` acceptance item and the Grafana session panel where deployed.
  - The Portal and xray-tui do not show sessions (dated non-goal below).

## Non-goals (2026-11-08)

- Moving a flow between connections or surviving a connection's loss mid-flow. It would need a reliable layer above QUIC
  on both ends, because QUIC's acknowledgment means the peer's stack has the data, not that it reached the destination,
  and the server would have to keep orphaned destination connections. The common loss of a connection is a server
  restart, where the destination connections end with the process anyway. Draining replaces it for scale down (user
  agreed, 2026-11-08).
- Scaling by throughput (Measurements).
- Relaying for other Hy2 implementations, and relaying over other transports.
- A default pool or relay for unconfigured outbounds.
- Portal and xray-tui presentation: Grafana and `/xray-debug` carry it. Revisit when an operator needs it per node in the
  Portal.

## Edge cases

- An older server: every relay outbound is `unsupported` and dead, so balancers fall back; the owner's own traffic
  works.
- One relayed user refused: only its relay outbound is dead.
- A configuration reload restarts Core: the pool and the bindings are rebuilt.
- Every connection without stream credit: the bounded wait, then the stream fails; the report shows the streams at the
  limit.
- The server restarts: every connection closes; the owner redials with jitter, and a probe round may be missed.
- More than 2^24 UDP sessions under one index over a connection's life: sequence numbers wrap and skip IDs in use.
- A credential rotated on both ends: the next connection negotiates with the new credential; a connection that still
  sends the old one gets `unknown` for it.
