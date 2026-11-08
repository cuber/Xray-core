# Plan: Hy2 Padding Switch

- Spec: [spec.md](spec.md)

## Core

No library change; the quic-go pin stays.

- **Proto.**
  - `transport/internet/hysteria/config.proto`: `Config.disable_padding = 14` (bool).
  - `app/proxyman/command/command.proto`: `QuicEffective.padding = 10` (string: `random`, `off`, or empty from an
    older Core).
  - Regenerated with the files' existing protoc header. Both changes are additive.
- **JSON.** `infra/conf/transport_method.go`: `HysteriaConfig.DisablePadding` (`json:"disablePadding"`), copied by
  `Build` into `Config.DisablePadding`. No new check: spec024's refusal of a relay outbound's `streamSettings` already
  keeps the field off relay outbounds.
- **Authentication headers** (`transport/internet/hysteria`):
  - `config.go`: `setPadding(h http.Header, p padding, c *Config)` sets `Hysteria-Padding` to `p.String()` unless
    `c.GetDisablePadding()`, and otherwise leaves the header out.
  - `dialer.go` (`client.dial`): the authentication request builds its header without the literal padding entry, then
    calls `setPadding` with `AuthRequestPadding` and the client's `config`.
  - `hub.go` (`httpHandler.AuthHTTP`): both answers, the repeated and the first authentication, call `setPadding` with
    `AuthResponsePadding` and `h.config`.
- **How the proxy learns the choice: the stream carries it** (spec R3).
  - `conn.go`: `interConn` gains `noPadding bool` and `HysteriaPadding() bool` (`!noPadding`).
  - `dialer.go` `tcpOpen` sets `noPadding` from `c.config.GetDisablePadding()`. Every client stream is built there:
    a plain outbound, each member of an owner's pool, and a relay outbound's streams, which `dialRelay` opens with the
    owner's stream settings and so on the owner's `client` and `Config`.
  - `hub.go` `StreamDispatcher` sets it from `h.config.GetDisablePadding()` in both the `0x401` and `0x402` cases.
  - Considered and rejected: the proxy reading its own stream settings in `NewClient` and `NewServer`, and a relay
    reading its owner's through `sessionOwner()`. That duplicates the owner lookup, and it could drift from the
    connection that actually carries the stream.
- **Writers** (`proxy/hysteria/protocol.go`):
  - `WriteTCPRequest(w, addr, padding bool)` and `WriteTCPResponse(w, ok, msg, padding bool)`. Each keeps one encoder
    that takes the padding string.
  - `true` draws it exactly as today (`hysteria.TcpRequestPadding.String()`, `TcpResponsePadding.String()`): the same
    ranges and the same `math/rand` calls.
  - `false` passes `""`, which writes a one-byte length 0 and nothing after it.
  - The readers do not change.
  - The one caller outside the package, `proxy/anytls/outbound_hy2_counts_test.go`, passes `true`.
- **Proxy** (`proxy/hysteria`): `sendsPadding(conn)` unwraps the stats wrapper (`stat.TryUnwrapStatsConn`) and asserts
  `interface{ HysteriaPadding() bool }`, as `server.go` already reads `User()`; it returns `true` when the method is
  absent. `client.go` (TCP request) and `server.go` (TCP response) pass its result to the writers.
- **Report** (`app/proxyman/command/quic_stats.go`):
  - `quicHandlerStats` sets `Effective.Padding` from `mss.ProtocolSettings`, through an assertion on the generated
    getter `interface{ GetDisablePadding() bool }`: `off` or `random`. The command package does not import the Hy2
    transport.
  - Settings without the getter (a future non-Hy2 QUIC transport) leave the field empty.
  - `quicRelayStats` is unchanged: a relay entry has no `effective`.
- **CLI** (`main/commands/all/api/quic_stats.go`): `formatQuicEffective` appends `padding <value>`, and `unavailable`
  for an empty value.

## Tests

- `proxy/hysteria/padding_test.go` (new), FR-001:
  - golden encodings for a fixed padding string;
  - 10 000 headers per message within today's ranges, reaching within 5 of both ends;
  - zero-length encodings, and the readers on both.
- `transport/internet/hysteria/padding_test.go` (new):
  - FR-001 and FR-003 for the authentication headers: a real dialer against a raw `http3.Server` that records the
    request header, and a real listener against a raw HTTP/3 client, first and repeated authentication;
  - `HysteriaPadding()` on plain, pool and relay streams (FR-005).
- `proxy/hysteria/padding_wire_test.go` (new), FR-003, FR-004 and FR-005:
  - started instances built from JSON;
  - a raw Hy2 server, modeled on the fake server in `proxy/anytls/outbound_hy2_counts_test.go`, that reads each
    request's padding length and, for FR-005, answers `Hysteria-Relay` with `1=ok` and reads the `0x402` body;
  - a raw client that reads the response's padding length;
  - TCP and UDP round trips in all four on/off combinations.
- `infra/conf/hysteria_padding_test.go` (new), FR-002: parsing, the default, a non-boolean value, and
  `ListOutbounds`/`ListInbounds` on a started instance.
- `transport/internet/hysteria/loss_recovery_test.go`: FR-006 on its existing loss relay, which gains a size record and
  a drop-first-instance mode.
- `app/proxyman/command/quic_stats_contract_test.go`: FR-007, with an inbound, an outbound and an owner set to `off`
  beside the existing default handlers and relay outbounds.
- `main/commands/all/api/quic_stats_test.go`: FR-008.
- The full suite of a Hy2 change: `go test ./...`, `-race` on the touched packages, `.github/check-gofmt.sh`, and
  `testing/netem/run.sh -test.count=3` (its tests run with padding on, the default).

## As implemented (2026-11-09)

- The writers keep one encoder each (`writeTCPRequest`, `writeTCPResponse`, taking the padding string), and
  `sendsPadding` sits beside them in `proxy/hysteria/protocol.go`.
- FR-002's started-instance part (`ListInbounds`, `ListOutbounds`) is in FR-007's contract test, whose instance already
  has `HandlerService`; `infra/conf/hysteria_padding_test.go` covers the parsing and the handlers' stream settings.
- FR-005 runs end to end on started instances in `proxy/hysteria/padding_wire_test.go`: a raw server reads the owner's
  `0x401` and its relay's `0x402` streams on both connections of the pool. `transport/internet/hysteria/padding_test.go`
  covers `HysteriaPadding()` on a plain client stream and on the listener's streams.
- FR-006: the loss relay also wraps the server's socket in that mode, and the server turns path MTU discovery off as the
  fixture's client already does (an MTU probe is no datagram of the measured exchange). The padding-on run counts the
  datagrams above 512 bytes without drops: dropping the first instance of a datagram that has no copy loses it every time.

## Gate and rollout

- The change reaches Hy2's per-stream write path (`proxy/hysteria`, `transport/internet/hysteria`), its configuration
  and the control plane. `xrayctl gate plan` is expected to pick L1 with the Hy2/QUIC suites. The tier it prints
  decides, including L2 if it finds a hot function.
  - `migration hy2-check --duration 600` with default settings (FR-001: padding on).
  - `quic-check --baseline-node <target>` with one more group that sets the field at both ends (FR-009). Its
    `--variants` set only `quicParams` today, so xray-config's `quic-check` first needs a per-variant
    `hysteriaSettings` entry. That is an xray-config change, not Core.
  - `relay-check` with the owner's field on and off, since relay streams follow it (spec R3).
- The gate's injected loss drops datagrams at random whatever their size, so it cannot show the size effect itself.
  It shows that padding off is not worse, and, in a group with copies, that copies now cover the opening packets.
  The size effect is the wg-ix measurement (spec Measurements), followed after the rollout through `GetQuicStats` and
  the probe.
- Rollout: xray-config spec024. The Core goes to isz5, tgz.lite and vgz.ecs first. A configuration-only deployment then
  sets the field at both ends of the segment. The gate does not deploy.

## Rollback

- Removing the field restores padding with a configuration-only deployment.
- `switch revert` restores the previous Core. It ignores the field and pads, and it reports `padding` empty.
