# Plan: QUIC Loss Recovery And Redundancy

- Spec: [spec.md](spec.md)

## Library fork

- The fork, its line branches, the `replace` and the upgrade procedure are [spec022](../022-quic-go-fork/spec.md)'s
  (user, 2026-11-07: "补丁用 fork 的方式做"). The patches sit on the current quic-go line (`fork/2026-11-07/184d081eef3e`,
  [spec023](../023-fork-lines/spec.md)) as three commits under the fork's own
  [spec001](../../../quic-go/spec/001-loss-recovery/spec.md): config and recovery, copies, counters.
- On 2026-11-07 the library series, its call sites and its tests moved to the fork's
  [plan](../../../quic-go/spec/001-loss-recovery/plan.md); this plan keeps the Core side.
- The `replace` moves from the base commit to the series head with T006.

## Core

- **Proto.** `transport/internet/config.proto`: `QuicParams` fields 17–25, regenerated (header kept at protoc
  v6.33.5, as the file was).
- **JSON.** `infra/conf/transport_finalmask.go`: the `QuicParamsConfig` fields and `checkLossRecovery`, which
  `StreamConfig.Build` (`infra/conf/transport_internet.go`) calls with the other `quicParams` checks: the ranges, and
  `duplicateDelayMs` only with a copy mode. `OutboundDetourConfig.Build` (`infra/conf/xray.go`) calls
  `checkChromeParrotAckDelay`: `maxAckDelayMs` on a `hysteria` or `splithttp` outbound requires `disableChromeParrot`.
- **`transport/internet/quicrecovery`** (new, the only Core package that maps these fields to quic-go):
  - `Apply(*quic.Config, *internet.QuicParams)`, called after each `quic.Config` literal of the Hy2 dialer and
    listener, masque's dialer and listener, and splithttp's HTTP/3 dialer and listener;
  - `Effective(*internet.QuicParams)`, quic-go's `Config.LossRecovery()` for the report;
  - `Conn(*quic.Conn)`, which adapts a connection to `internet.QuicConn`.
- **Runtime state.** `transport/internet/quic_stats.go` (no quic-go import):
  - `QuicStatsSet` holds a handler's connections and the counters of the closed ones. A closing connection's final
    counters fold into the totals, so they stay monotonic across replacement.
  - Each `MemoryStreamConfig` carries one (`QuicStats`, created in `ToMemoryStreamConfig`), so the set lives exactly
    as long as the handler's stream settings and needs no registry. Every listener of an inbound and every dialed
    client of an outbound share it.
  - `Report` copies the set under its lock and reads quic-go's atomic counters after; rows are the live connections,
    oldest first, at most 256.
- **Hy2.** The dialer's `client` adds its connection when the dial succeeds and removes it in `close`. The listener
  adds each accepted connection in `handleClient`, records the user's email after authentication, and removes it
  when the connection ends.
- **Handlers.** `AlwaysOnInboundHandler` and the outbound `Handler` expose `StreamSettings()`.
- **RPC.** `app/proxyman/command/command.proto`: `GetQuicStats` (request `tag`, `connections`; messages
  `QuicHandlerStats`, `QuicEffective`, `QuicCounters`, `QuicConnection`). `quic_stats.go` walks the inbound and
  outbound managers as `GetInboundMuxStats` does and reports handlers whose stream settings name the `hysteria`
  transport; a tag that names none returns `NotFound`.
- **CLI.** `main/commands/all/api/quic_stats.go`: `xray api quicstats [-connections] [-json] [tag...]`; an older
  Core's `Unimplemented` prints as unavailable.

## Tests

- Fork: its own suite and spec001's tests, with Core's toolchain.
- `infra/conf/transport_quic_params_test.go`: FR-005 parsing, ranges and the delay rule.
- `transport/internet/quicrecovery/quicrecovery_test.go`: `Apply` and `Effective`.
- `transport/internet/quic_stats_test.go`: the set (nil, replacement, bounded rows).
- `transport/internet/hysteria/loss_recovery_test.go`: FR-006, a real Hy2 listener and client on loopback whose
  client socket drops chosen datagrams, like `handshake_loss_test.go`.
- `app/proxyman/command/quic_stats_contract_test.go`: FR-007 on a started instance through a real gRPC server.
- `main/commands/all/api/quic_stats_test.go`: FR-008.
- `transport/internet/hysteria/netem_test.go` (`linux` only) and `testing/netem/run.sh`: FR-010. The runner
  cross-compiles the package's tests for linux/amd64, the fleet's architecture, and runs the `TestNetem*` tests in a
  container with `--network none` (lo only) and `NET_ADMIN`, built from `testing/netem/Dockerfile` (Alpine with
  iproute2). The tests set netem on lo themselves; with `XRAY_NETEM=1` a missing precondition, or any interface other
  than lo, fails instead of skipping. Expectations compare tunings on the same seeded loss and measure against the
  run's own median and the real end of a blackout, so the container's overhead does not move them.
- The full suite of a QUIC change is `go test ./...`, `testing/netem/run.sh -test.count=3`, and the fork's suite.

## Gate and rollout

- The series reaches the QUIC send and receive path, so `xrayctl gate plan` is expected to pick the Hy2/QUIC tier
  of the stability-check skill:
  - `migration hy2-check --duration 600` on the Akile hosts with the default parameters (FR-001) and with the
    xray-config spec024 values;
  - `hy2-overhead` and `udp-path` for wire behavior, with copies on and off.
- Rollout: xray-config spec024, which deploys to isz5, tgz.lite and vgz.ecs only; the gate does not deploy. Its
  `wg-hy2` outbounds set `disableChromeParrot: true` with `maxAckDelayMs` (spec R1).

## Rollback

- Removing the fields from a node's config restores today's behavior with a config-only deployment.
- `switch revert` restores the previous Core. An older Core reports `GetQuicStats` as `Unimplemented`.
