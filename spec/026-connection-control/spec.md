# Feature Specification: Connection Control

- Feature ID: 026-connection-control
- Created: 2026-11-10
- Status: Approved 2026-11-10 (user: "做个 Core API，按用户断连接，Hy2 也能断；你用akile详细测试在搞负载情况下关闭是否会导致死锁，
  panic等稳定性问题，以及工作是否符合预期，如果没问题，就加到控制面，暴露到 portal 上，我可以手工点击一键清除连接"; "另外
  xray-core能不能顺带展示出当前alive的连接数，h2 的 session 数之类的统计数据，方便我看清除效果"; on the cross-component design
  xray-config draft 008, "看懂了 … 你就可以开始开发刚才讨论的内容了", which took its recommended decisions D-1–D-6).
- Baseline: Core `1f4d2ff7` on `fork/2026-11-07/0888beb38a9b` ([spec023](../023-fork-lines/spec.md)). No library change.
- Depends on: [spec013](../013-sing-mux-inbound/spec.md) (sing-mux carriers), [spec021](../021-quic-loss-recovery/spec.md)
  (`QuicStatsSet`, `GetQuicStats`), [spec024](../024-hy2-relay-session/spec.md) (relay users on an owner's connection).
- Consumers: Sidecar spec022 (`/xray-debug` `connections`, the tokened close endpoint) and xray-config
  [spec034](../../../xray-config/spec/034-connection-control/spec.md) (the Akile gate `connclose-check`, Auth, Portal,
  Grafana and the rollout).

## Problem and Goal

Moving users off a node means moving their live connections. DNS or Portal changes only steer new connections; the
open ones stay until the client drops them. The operator's tool today resets inbound TCP in the kernel (`ss -K` by
port and client address), which cannot select a user, and cannot reach QUIC at all: every Hy2 client shares one UDP
socket, so only a Core restart moves Hy2 users, and it moves every user of the node.

What Core keeps today:

| Protocol | Where the authenticated user is | `RemoveUser` and live objects |
|---|---|---|
| SS-2022 (multi-user) | TCP: `session.Inbound.User`, written before dispatch; UDP: each session is bound to its user | closes the user's UDP sessions; TCP connections intentionally keep their lifetime |
| VLESS, Trojan | `session.Inbound.User`, written before dispatch | none |
| AnyTLS | the connection's user, copied into each stream's inbound | closes the user's connections |
| Hy2 | each QUIC connection authenticates once; every TCP stream and UDP session is a connection of the inbound's stream worker and carries its user | none; an authenticated connection can open new streams |
| Hy2 relay (spec024) | the QUIC connection belongs to the session owner; relayed streams carry their own route user | the relayed user's next stream is refused |
| Mux.Cool, sing-mux | the carrier is one authenticated connection; sing-mux records the carrier's email and streams | sing-mux refuses new streams; Mux.Cool does not check |

- The stream worker already tracks every accepted connection with its cancel function
  (`app/proxyman/inbound/worker.go`), and closes them all only when the inbound is removed. The entries carry no user.
- `session.Inbound.User` is written by the protocol goroutine without a lock; reading it from another goroutine is a
  data race. User objects are copied (Hy2, AnyTLS), so users are matched by email, case-insensitively, as SS-2022
  lowercases its keys.

Goal: Core closes a user's live objects on chosen inbounds, Hy2 included, and reports the live objects per inbound and
per user, so that an operator sees what a closure removes and what reconnects.

## Contract

### R1 Registry and user binding

- Every connection the stream worker accepts records its kind: `tcp` for a stream-transport connection that is not
  Hy2, `hy2-stream` for a Hy2 TCP stream, `hy2-udp` for a Hy2 UDP session (both are connections of the worker).
- Each entry gets its user's email **once**, never changed afterwards:
  - For Hy2, at accept, from the connection's user (the route user on a relayed stream, spec024).
  - For every other protocol, at the entry's first dispatch. The worker hands the protocol a per-connection dispatcher
    that reads `session.Inbound.User` from the dispatching context on the goroutine that dispatches, which is the one
    that wrote it (or holds a copy made before), and binds the email before it forwards the call. No protocol changes.
  - The same first dispatch marks the entry a Mux.Cool or sing-mux carrier when its destination is one that this
    inbound's multiplexing server serves.
- An entry that never dispatches with a user stays **unbound**: a connection in its handshake, refused, falling back,
  or used for a VLESS reverse bridge. Unbound entries are counted and never closed by user.
- Hy2 QUIC connections already sit in the inbound's `QuicStatsSet` with the user they authenticated as (spec021 R3).
  A connection without a user (unauthenticated, masquerade traffic) counts as unbound.
- SS-2022 multi-user UDP sessions are bound to their user already.
- The registry adds per connection one pointer-sized write at accept and one atomic store at bind. Reports and
  closures hold the worker lock only to copy entries.

### R2 `HandlerService.GetConnections` (read-only)

- Request: `tags` (empty: every inbound), `user_rows` (adds one row per user with a live object), `users` (with
  `user_rows`, only these users; case-insensitive).
- Per inbound, `counts` (`ConnectionCounts`):

| Field | Counts |
|---|---|
| `tcp` | accepted stream-transport connections that are not Hy2 (bound or not) |
| `mux_cool_carriers`, `sing_mux_carriers` | those of the `tcp` connections that are carriers |
| `hy2_connections` | live Hy2 QUIC connections of the inbound |
| `hy2_streams`, `hy2_udp_sessions` | the worker's Hy2 TCP streams and UDP sessions |
| `udp_sessions` | SS-2022 multi-user UDP sessions |

  and besides: `unbound` (entries and QUIC connections without a user), `mux_cool_streams`, `sing_mux_streams` and
  `sing_mux_protocols` (active carriers by stream protocol: `smux`, `yamux`, `h2mux`) from the inbound's multiplexing
  server, the same values as `GetInboundMuxStats`.
- Per user row: `user` (email as bound) and the same `ConnectionCounts` for that user's objects.
- Bounds: rows are distinct bound emails, at most 4096 per inbound, then `truncated`. No secrets: emails, counts and
  tags only. Inbounds are sorted by tag, rows by email.
- Unknown tag: `NotFound`. An inbound that is not a stream or Hy2 inbound reports zeros.

### R3 `HandlerService.CloseConnections` (mutating)

- Request: `users` (emails, case-insensitive) or `all_users: true`, exactly one of them; `tags` (empty with `users`:
  every inbound; required with `all_users`, so that no call clears a whole node by omission); `exclude_users`;
  `reason` (logged).
- What closes, per selected inbound, for a selected user U:

| Object | Closure |
|---|---|
| A `tcp` entry bound to U (SS-2022, VLESS, Trojan, AnyTLS, Mux.Cool or sing-mux carriers included) | its context is canceled, then the connection is closed: the path that removing an inbound takes. A carrier's streams end with it. |
| A Hy2 QUIC connection whose user is U | `CloseWithError(0x100, "closed by control plane")`: every stream on it ends, relayed users' included |
| A Hy2 stream or UDP session bound to U on a connection that is not U's (a relayed user) | that stream is reset both ways (`CancelRead` and `CancelWrite`), the session closes; the owner's connection and its other streams stay |
| An SS-2022 UDP session of U | closes; the client's next packet starts a new session |

- `all_users` selects every bound user not in `exclude_users`. Unbound objects are never selected.
- A Hy2 stream is aborted rather than closed: its `Close` only queues a FIN behind data already written, so a `Write`
  waiting for the flow control of a peer that stopped reading would stay blocked, and with it the stream's goroutine
  and its entry (found by the Akile gate on 2026-11-10). Closing a socket releases its blocked calls, and closing a QUIC
  connection releases its streams'.
- Entries are picked under the worker's lock and marked closing, so a closure counts each object once even when calls
  overlap; closing happens outside the lock, concurrently, with a two-second bound. A close still running at the
  bound is counted as `pending` and finishes on its own (a TLS close can wait for its close-notify write). The call
  returns within three seconds.
- Closing an entry that ends on its own at the same time, or after the inbound was removed, is harmless: the worker's
  retirement is unchanged and repeats the close.
- Response: per inbound, `closed` (`ConnectionCounts` of the objects selected) and `pending`.
- Invalid arguments (`users` and `all_users` both or neither, `all_users` without `tags`): `InvalidArgument`. Unknown
  tag: `NotFound`, before anything closes.
- No configuration changes; new connections are admitted as before. There is nothing to roll back: closed clients
  reconnect wherever their configuration or DNS now sends them.
- Each call logs one warning line with the reason and the totals.

### R4 Command line

- `xray api conns [-users] [-user <email>]... [tag]...` prints R2; `-json` prints the response.
- `xray api connclose (-user <email>... | -all -tag <tag>...) [-exclude <email>]... [-reason <text>]` prints R3.
- Both name the RPC as unavailable on an older Core (`Unimplemented`).

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | Every accepted stream-worker connection is registered with its kind, and a Hy2 stream or UDP session with its user at accept | `TestRegistryKinds`, contract |
| FR-002 | Other protocols bind the email at the first dispatch through the per-connection dispatcher, without protocol changes and without a cross-goroutine read of `session.Inbound.User` | `TestBindAtFirstDispatch`, race suite |
| FR-003 | Mux.Cool and sing-mux carriers are marked only where the inbound serves them | `TestCarrierMarks`, contract |
| FR-004 | `GetConnections` reports R2's counts per inbound and per user, bounded, sorted, without secrets | contract `TestGetConnectionsContract` |
| FR-005 | `CloseConnections` closes exactly R3's objects; unbound objects and other users stay | contract `TestCloseConnectionsContract` |
| FR-006 | A relayed Hy2 user's closure leaves the owner's connection; the owner's closes the connection | contract (Hy2 relay case) |
| FR-007 | Overlapping closures, natural ends, inbound removal and server close race without deadlock, double count or panic; the call returns within three seconds | `TestCloseRaces` with `-race -count=3`, the Akile gate |
| FR-008 | Argument validation and `NotFound` happen before anything closes | `TestCloseConnectionsArguments` |
| FR-009 | `xray api conns` and `xray api connclose` | `TestConnsCommand` |
| FR-010 | Stability under load on real hosts (xray-config spec034 `connclose-check`, with the hot-path gates) before any deployment | xray-config spec034 |

## Control Plane

- Placement (CP-4): `HandlerService`, which owns inbounds, their users and `GetInboundMuxStats`/`GetQuicStats`. Nodes
  already enable it in `api.services`; no new service.
- CP-3: `GetConnections` has no side effects. `CloseConnections` has its own requirement (R3), its authorization is
  the API socket's (mode 0600 UDS, the Sidecar its only caller), and it has nothing to roll back (R3).
- CP-5: emails, tags and counts; rows bounded by bound users and 4096; the worker lock is held only to copy entries.
- CP-6: an older Core answers `Unimplemented` to both; consumers show "unavailable", never zeros.
- CP-7: `app/proxyman/command/connections_contract_test.go` drives both RPCs through a real gRPC server on started
  instances, with a default configuration (empty reports) and the feature's state.
- CP-8: Sidecar spec022 adds `/xray-debug` `connections` and the tokened `POST /xray-control/connections/close`;
  xray-config spec034 adds the Portal node "连接" tab and the user page's action through Auth, a Grafana panel and the
  `connections` acceptance item.

## Non-goals (2026-11-10)

- Refusing reconnection. A closed client may connect again at once; steering is DNS's and Portal's job.
- Closing by client address (`xrayctl conns` does it in the kernel) and automatic triggers.
- Unix-socket inbounds (`dsWorker`), raw-UDP inbounds other than SS-2022's sessions, and the SS-2022 relay inbound's UDP
  sessions, which carry no user binding. Outbound connections.
- Mux.Cool's XUDP sessions keep their one-minute expiry after their carrier closes.

## Edge cases

- A user removed with `RemoveUser` keeps its bound TCP entries until closed or ended, and they still report under its
  email; `GetConnections` therefore shows what a removal left behind.
- An AnyTLS connection binds at its first stream; later streams carry the same user.
- An entry that a protocol dispatches with an empty email (a fallback, a static inbound without users) stays unbound.
- A Hy2 connection's user is set when it authenticates; `GetConnections` taken during authentication counts it as
  unbound.
- `CloseConnections` naming a user with no live object returns zeros: closing is idempotent.
