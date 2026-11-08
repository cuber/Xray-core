# Plan: Hy2 Relay Sessions

- Spec: [spec.md](spec.md)

## Core

- **Proto.**
  - `transport/internet/hysteria/config.proto`: `Config.session` (message `Session`: `min_connections`,
    `max_connections`, `streams_per_connection`, `scale_up_streams`).
  - `proxy/hysteria/config.proto`: `ClientConfig.relay` (message `Relay`: `session`, `auth`).
  - `proxy/hysteria/account/config.proto`: `Account.relay` (repeated email).
  - `app/proxyman/command/command.proto`: `QuicHandlerStats.session` (`QuicSession`), `QuicHandlerStats.relay`
    (`QuicRelay`), `QuicConnection.state`, `streams_open`, `relay` (repeated `QuicRelayBinding`: index, user, status).
    Additive field numbers only; regenerated with the files' protoc header.
- **JSON** (`infra/conf`):
  - `hysteria.go`: the client's `relay` and the server clients' `relay`, with the R1 checks that one handler can see.
  - `transport_internet.go`: `hysteriaSettings.session` and the keepalive rule against `quicParams`.
  - `xray.go` (`Config.Build`, which sees every outbound): the session references, chains, duplicate credentials, the
    255 limit and the absence of `streamSettings` on relay outbounds.
- **Relay protocol** (`transport/internet/hysteria`):
  - `relay.go` (new): the header encoding (base64url, `index=value` lists), the `0x402` frame, the UDP ID layout and the
    binding table. Pure functions, unit tested.
  - `dialer.go`: `client` becomes a pool owner (`pool.go`, new) holding `poolConn`s; each `poolConn` is today's
    connection state (`conn`, `tr`, `pktConn`, `udpSM`) plus its state, stream count, bindings and progress clock. The
    client key stays `dialerConf`, so an owner keeps exactly one pool.
  - The authentication request carries `Hysteria-Relay` when the owner has relay outbounds; the response's statuses become
    the `poolConn`'s bindings.
  - `tcp(ctx)` and `udp(ctx)` read the relay index from the context (`ContextWithRelayIndex`), place the stream (R4, R6),
    and write `0x402` + index instead of `0x401` for an index above 0.
  - `hub.go`: `AuthHTTP` evaluates the request header against the validator and the connection user's `relay` list and
    answers it; `StreamDispatcher` accepts `0x402` for `ok` indexes and re-checks the user (R3); the server's
    `udpSessionManager` takes each session's user from its index.
- **Relay outbound** (`proxy/hysteria/client.go`): with `relay` set, `NewClient` skips the stream-settings check; `Process`
  resolves the owner through the outbound manager, takes its `StreamSettings()` (spec021) and calls the Hy2 transport
  `Dial` with the owner's settings and the relay index in the context. The index is the relay outbound's position in its
  session, fixed when the configuration is built and shared with the owner's negotiation list.
- **Library** (quic-go fork spec002, commit on the line, then the `replace`): `ConnectionStats.PacketsAcked` and
  `BytesInFlight`; `internet.QuicConnStats` carries them through `quicrecovery.Conn`.
- **Pool** (`transport/internet/hysteria/pool.go`): placement, scale up and down with a clock interface, stream credit
  (`OpenStream` falling back to `OpenStreamSync` with the handshake timeout), health (closed, suspect from
  bytes in flight without acknowledged packets), redial with backoff and jitter, the drain and spare timers. The 25%, 60 s,
  5 min, 1 s and backoff constants live there, named and documented.
- **Observatory** (`app/observatory/burst`): probe dials carry a context mark (`session` package, next to the other probe
  markers); `track.follow` marks a relay outbound dead with its reason when its binding is `unknown`, `denied` or
  `unsupported`, read through an interface the Hy2 client implements (`RelayStatus() (status, reason string)`), so the
  observatory does not import Hy2.
- **Report** (`app/proxyman/command/quic_stats.go`): the owner's `session` and connection fields from the pool under its
  lock, relay outbound entries from their clients, server bindings from the listener's connection table.
- **CLI** (`main/commands/all/api/quic_stats.go`): the new fields in the text view.

## Tests

- `infra/conf/hysteria_relay_test.go`: FR-002.
- `transport/internet/hysteria/relay_test.go`: FR-003 encoding, statuses, `0x402`, UDP IDs and wrap.
- `transport/internet/hysteria/pool_test.go`: FR-005 and FR-006 with a fake clock and fake connections.
- `proxy/hysteria/relay_session_test.go`: FR-004 and the FR-008 contract end to end on two started instances (routing by
  user, per-user stats, the owner and four relay outbounds, TCP and UDP, `RemoveUser`, GetQuicStats through real gRPC).
- `app/observatory/burst/track_relay_test.go`: FR-007.
- `main/commands/all/api/quic_stats_test.go`: FR-009 extended.
- `transport/internet/hysteria/netem_test.go` and `testing/netem/run.sh`: FR-010.
- The full suite: `go test ./...`, `-race` on the touched packages, `testing/netem/run.sh -test.count=3`.

## Gate and rollout

- The change reaches the Hy2 send and receive path and the observatory, so `xrayctl gate plan` is expected to pick the
  Hy2/QUIC tier: `migration hy2-check --duration 600` and `quic-check` (xray-config spec025) with and without a session,
  plus the singmux A/B if the plan names the dispatcher.
- Rollout: xray-config spec026, isz5 then tgz.lite; vgz.ecs keeps its own connections. The gate does not deploy.

## Rollback

- Removing `session` and `relay` from a node's configuration restores one connection per outbound with a config-only
  deployment; the relayed users' credentials stay valid for their own connections.
- `switch revert` restores the previous Core.
