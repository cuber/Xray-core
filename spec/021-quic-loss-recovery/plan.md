# Plan: QUIC Loss Recovery And Redundancy

- Spec: [spec.md](spec.md)

## Library fork

- The fork, its line branches, the `replace` and the upgrade procedure are [spec022](../022-quic-go-fork/spec.md)'s
  (user, 2026-11-07: "补丁用 fork 的方式做"). This spec's patches sit on the current quic-go line (`fork/2026-11-07/184d081eef3e`,
  [spec023](../023-fork-lines/spec.md)) as a short series, one per concern
  below, each with its tests, under a specification in the fork's own `spec/`. Their library contract moves
  there once the fork's spec-kit exists; this plan keeps the call sites until then.
- The `replace` moves from the base commit to the series head with T006.

### Series

1. **Config.** `quic.Config` gains `MaxAckDelay`, `AckElicitingThreshold`, `InitialRTT`, `PTOBackoffLimit`,
   `LossTimeThreshold`, `LossPacketThreshold`, `DuplicateMaxSize`, `DuplicateTail`, `DuplicateDelay`.
   `populateConfig` keeps today's values for zero, and `validateConfig` enforces the spec's ranges.
2. **Recovery (R1).** Call sites read in the baseline library.
   - `internal/utils/rtt_stats.go`: `NewRTTStats` takes the initial RTT. It replaces `DefaultInitialRTT` in three
     places: the constructor, `ResetForPathMigration`, and the PTO before the first sample.
   - `internal/ackhandler/received_packet_tracker.go`:
     - `maxAckDelay` (set from `protocol.MaxAckDelay` in the constructor) comes from the config, through
       `NewReceivedPacketHandler`;
     - `packetsBeforeAck` (2) becomes a field read by `shouldQueueACK`;
     - the immediate ACKs for missing and ECN-CE packets are unchanged.
   - `connection.go`: both transport parameter sites advertise `MaxAckDelay + TimerGranularity` instead of
     `protocol.MaxAckDelayInclGranularity`.
   - `internal/ackhandler/sent_packet_handler.go`:
     - `getPTO` shifts by `min(ptoCount, limit)` and keeps `maxPTODuration`;
     - the constants `timeThreshold` and `packetThreshold` become fields, read by `detectLostPackets`.
3. **Copies (R2).** A per-connection duplicator.
   - **Which packet is ack-eliciting.** `appendOneShortHeaderPacket` also returns the packed `shortHeaderPacket`,
     so the send loops know whether a datagram is ack-eliciting.
   - **Full queue.** `sendQueue.Send` panics when the queue is full (callers check `WouldBlock`). The duplicator
     queues a copy only when `WouldBlock` is false, and otherwise counts it as skipped.
   - **Small datagrams.** `sendPacketsWithoutGSO`, `maybeSendAckOnlyPacket` and `sendProbePacket` hand each 1-RTT
     datagram of at most `DuplicateMaxSize` to the duplicator before `sendQueue.Send`, because the queue owns and
     releases the buffer once written. The duplicator copies it into a pooled buffer.
   - **GSO batches.** `sendPacketsWithGSO` hands over the segments that qualify: only the last segment can be
     short, and every segment qualifies when the size covers full packets.
   - **Tail.** A tail is known only when the next `appendOneShortHeaderPacket` returns `errNothingToPack`:
     - with `DuplicateTail`, the non-GSO loop keeps a private copy of the last ack-eliciting datagram of the round,
       and the GSO loop copies the batch's last segment when the batch ends for that reason;
     - a round that ends on pacing, the congestion window, `WouldBlock` or pending received packets has no tail.
   - **Delay.** Delayed copies wait in a small deadline queue. `maybeResetTimer` includes its earliest deadline, the
     run loop sends what is due, and closing the connection releases what is left.
   - **Outside recovery.** Copies never pass `registerPackedShortHeaderPacket`, so the sent packet handler, the
     congestion controller and the pacer never see them. Path probes (`PackPathProbePacket`), MTU probes and
     coalesced long-header datagrams are not handed to the duplicator.
4. **Counters.**
   - **Loss.** `PacketsLost` and `BytesLost` move from `cubicSender.OnCongestionEvent` to
     `sentPacketHandler.detectLostPackets`, at the call of `cc.OnCongestionEvent`: ack-eliciting packets that are
     neither path probes nor MTU probes. Cubic users see the same numbers, nothing is counted twice, and custom
     controllers get them too.
   - **New counters** in `connStats`, all returned by `ConnectionStats`:
     - `PTOCount`: `OnLossDetectionTimeout` in PTO mode;
     - `SpuriousLosses`: `detectSpuriousLosses`;
     - `CopiesSent`, `CopyBytesSent`, `CopiesSkipped`;
     - `DuplicatesReceived`: incremented at both `IsPotentiallyDuplicate` drops, 1-RTT and long header.

## Core

- `transport/internet/config.proto`: `QuicParams` fields 17–25, regenerated with the protoc the file names.
- `infra/conf/transport_finalmask.go`: `QuicParamsConfig` JSON fields; `Build` checks ranges and rejects
  `duplicateDelayMs` without a copy mode.
- `transport/internet/quic_params.go` (new): `ApplyLossRecovery(*quic.Config, *QuicParams)` and
  `EffectiveLossRecovery(*QuicParams)`, used by the Hy2 dialer and listener, masque, and splithttp HTTP/3.
- `transport/internet/hysteria/stats.go` (new):
  - a per-handler accumulator keyed by the handler's `*internet.MemoryStreamConfig`. That pointer already keys
    the dialer's `clientManager`, and the listener holds it.
  - The dialer's `client` and the `Listener` register their live connections, and fold a closing connection's
    `ConnectionStats` into the totals.
  - The listener records the authenticated user's email per connection for the rows.
- `transport/internet`: a small registry `QuicStatsFor(*MemoryStreamConfig)` that the transports fill. The
  outbound handler and the inbound workers answer `QuicStats()` from their own stream settings.
- `app/proxyman/command/command.proto` and `command.go`: `GetQuicStats` (request `tag`, `connections`; messages
  `QuicHandlerStats`, `QuicEffective`, `QuicCounters`, `QuicConnection`). It walks the inbound and outbound
  managers as `GetInboundMuxStats` does.
- `main/commands/all/api/quic_stats.go`: `xray api quicstats`.

## Tests

- Fork: the upstream suite (`go test ./...`, including `integrationtests`), plus a unit test per series patch:
  - recovery: timers, thresholds, backoff limit, advertised transport parameter;
  - copies: selection, delay, tail marking, a full send queue, receiver drop;
  - counters: each new counter, and losses counted under a custom controller (FR-009).
- `infra/conf/transport_finalmask_test.go`: FR-005 parsing, ranges and the delay rule.
- `transport/internet/hysteria/loss_recovery_test.go`: FR-006 through a local UDP relay that drops chosen
  datagrams, like `handshake_loss_test.go`.
- `app/proxyman/command/quic_stats_contract_test.go`: FR-007 on a started instance through a real gRPC server.
- `main/commands/all/api/quic_stats_test.go`: FR-008.

## Gate and rollout

- The series reaches the QUIC send and receive path, so `xrayctl gate plan` is expected to pick the Hy2/QUIC tier
  of the stability-check skill:
  - `migration hy2-check --duration 600` on the Akile hosts with the default parameters (FR-001) and with the
    xray-config spec024 values;
  - `hy2-overhead` and `udp-path` for wire behavior, with copies on and off.
- Rollout: xray-config spec024, which deploys to isz5, tgz.lite and vgz.ecs only; the gate does not deploy.

## Rollback

- Removing the fields from a node's config restores today's behavior with a config-only deployment.
- `switch revert` restores the previous Core. An older Core reports `GetQuicStats` as `Unimplemented`.
