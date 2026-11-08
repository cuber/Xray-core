# Feature Specification: QUIC Loss Recovery And Redundancy

- Feature ID: 021-quic-loss-recovery
- Created: 2026-11-07
- Status: Approved (2026-11-07; user: "那，1，2，3 是不是都可以做，然后做成可配置的，考虑控制面"; "补丁用 fork 的方式做；开始设计吧";
  "开始吧；这个改好需要经过全量测试"; "我感觉需要 check fail 出来，显示disable，不要做隐式操作"; "全量测试里是不是可以考虑用tc模拟一些丢包来验证"; "把这个测试做到测用例里，
  必须要验证到且要符合预期"). Rollout is a separate step
  (xray-config spec024).
- Baseline: Core `431e0868`, `github.com/apernet/quic-go` `v0.61.1-0.20260806010916-184d081eef3e`; implemented on the
  lines `fork/2026-11-07/0888beb38a9b` (Core) and `fork/2026-11-07/184d081eef3e` (quic-go, [spec023](../023-fork-lines/spec.md))
- Depends on: [spec022](../022-quic-go-fork/spec.md), the fork the library changes land on. The library contract is
  the fork's [spec001](../../../quic-go/spec/001-loss-recovery/spec.md).
- Consumers: [Sidecar spec019](../../../xray-sidecar/spec/019-quic-stats/spec.md) (`/xray-debug` `quic` and its
  metrics); rollout on isz5, tgz.lite and vgz.ecs in
  [xray-config spec024](../../../xray-config/spec/024-wg-ix-quic-recovery/spec.md).

## Problem and Goal

tgz.lite reaches isz5 through Hy2 inside WireGuard over an IX path (xray-config spec023), about 7.5 ms RTT. The
path loses packets in the network: neither host drops any (UDP buffer, softnet and NIC counters stay at zero), and
the loss comes in a daily pattern (none 03:00–07:00, most around midnight and 09:00–17:00). On 2026-11-07, a burst
observatory probe over Hy2 that lost a packet took:

- about 42 ms for a single loss. The probe is a lone request on an idle connection, so only the probe timeout
  (PTO) recovers it: smoothed RTT + 4 × RTT variance + the peer's `max_ack_delay`, about 7.5 + 1 + 26 ms. Three
  quarters of it is the 25 ms delayed-ACK allowance that quic-go hard-codes and advertises.
- 108, 504–572 and 1015 ms when the loss went on for a while: each PTO doubles (35 × 1, 3, 7, 15, 31 ms), so the
  recovery overshoots the end of the loss by up to the loss's own length.

These values are constants inside `apernet/quic-go` (`protocol.MaxAckDelay`, `packetsBeforeAck`,
`utils.DefaultInitialRTT`, the unbounded `<< ptoCount`, `timeThreshold`, `packetThreshold`). `quic.Config` cannot
set them, so Core cannot either. Core also cannot see what the transport loses. quic-go counts packets per
connection (`Conn.ConnectionStats`), but nothing reports them. And on Hy2 its loss counters are always 0: quic-go
adds to `PacketsLost` and `BytesLost` only inside its own Cubic sender (`cubicSender.OnCongestionEvent`), while
Hy2 installs BBR or Brutal through `SetCongestionControl`.

Goal: three opt-in capabilities on `finalmask.quicParams`, each off or at today's value when unset:

1. recovery parameters for paths whose RTT and loss are known;
2. redundant copies of the packets whose loss costs a timeout: small packets, and the last packet of a send round;
3. the QUIC connections' loss, timers and copies, reported read-only per inbound and outbound.

An unset field is exactly the original behavior, so one path can be tuned without touching any other. Copies cost
traffic and are off unless configured (user, 2026-11-07: "首先新增的配置，如果不配，等同于原始配置；这样才能做到我们针对
isz5 去特调"; "流量复制涉及到流量浪费，因此更需要默认关闭，在需要的地方开启"). xray-config spec024 sets the fields on one
path only.

## Contract

### R1 Recovery parameters

New `QuicParams` fields. 0 keeps today's value. A value outside its range fails the configuration (`run -test`).

| JSON | Proto | Today | Range | Meaning |
|---|---|---|---|---|
| `maxAckDelayMs` | `max_ack_delay_ms` (17) | 25 | 1–16382 | The local delayed-ACK timer, and the `max_ack_delay` transport parameter (advertised as this value plus the 1 ms timer granularity, as quic-go does today). The peer adds the advertised value to its PTO, so a path that should recover fast sets it on both ends. |
| `ackElicitingThreshold` | `ack_eliciting_threshold` (18) | 2 | 1–64 | Ack-eliciting packets received before an ACK is sent at once instead of after the delayed-ACK timer. |
| `initialRttMs` | `initial_rtt_ms` (19) | 100 | 1–10000 | The RTT before the first sample. The first PTO is twice this. It applies to the handshake and to a reset RTT estimate. |
| `ptoBackoffLimit` | `pto_backoff_limit` (20) | unlimited | 1–16 | The PTO doubles at most this many times in a row. quic-go's 60 s ceiling stays. |
| `lossTimeThreshold` | `loss_time_threshold` (21) | 9/8 | 1.0–4.0 | Time threshold of RFC 9002 loss detection, in RTTs. |
| `lossPacketThreshold` | `loss_packet_threshold` (22) | 3 | 1–64 | Packet reordering threshold of RFC 9002 loss detection. |

The values apply to every connection built from the stream settings: Hy2 dialer and listener, masque, and
splithttp's HTTP/3. XHTTP's `downloadSettings` carry their own `quicParams`. When XHTTP's HTTP/3 ends at a CDN, as
upstream recommends, the far end is the CDN's QUIC stack: only the client's own timers and copies apply on that hop,
and the listener fields and server-side copies need our own HTTP/3 listener. Congestion control is unchanged. A PTO stays a probe, not a loss signal, for the built-in BBR
and Brutal senders.

- The advertised value is the field plus 1 ms, so 16382 is the largest value a peer accepts (16383 ms).
- **Chrome parroting.** The Hy2 dialer and splithttp's HTTP/3 parrot Chrome unless `disableChromeParrot` is set. A
  parroting client keeps advertising Chrome's `max_ack_delay` (25 ms, omitted from its transport parameters), so the
  server's PTO towards it would not follow `maxAckDelayMs`. Nothing changes this implicitly: a `hysteria` outbound,
  or an `xhttp` outbound over HTTP/3 (TLS with exactly the ALPN `h3`, its `downloadSettings` checked on their own),
  that sets `maxAckDelayMs` without `disableChromeParrot: true` fails the configuration (`run -test`). Only the outbounds that need the field turn parroting off; it stays the default
  everywhere else (user, 2026-11-07). Inbounds do not parrot and take the field as it is.

### R2 Redundant copies

| JSON | Proto | Today | Range | Meaning |
|---|---|---|---|---|
| `duplicateMaxSize` | `duplicate_max_size` (23) | off | 1–65535 bytes | A 1-RTT datagram of at most this size is sent twice: ACK-only packets, small requests, PINGs. A value at or above the packet size copies every 1-RTT datagram, doubling the traffic. |
| `duplicateTail` | `duplicate_tail` (24) | off | bool | The last ack-eliciting datagram of a send round is sent twice whatever its size, when the round ends because nothing is left to send. A round that stops because of pacing or the congestion window has no tail. |
| `duplicateDelayMs` | `duplicate_delay_ms` (25) | 0 | 0–1000 | The copy leaves this long after the original instead of right behind it, so a short burst of loss does not take both. Requires `duplicateMaxSize` or `duplicateTail`. |

- A copy is the same datagram, byte for byte. The receiver drops it by packet number, as it drops any duplicated
  packet. The peer needs no support and no negotiation: one end can copy while the other does not.
- Copies are invisible to loss recovery and congestion control. They are not tracked as sent packets, add nothing
  to bytes in flight, and bypass the pacer.
- A delayed copy is dropped when its connection closes first. At most one copy per datagram is ever sent.
- A copy is queued only when the send queue has room. Otherwise it is skipped and counted, and the original is
  never held back for it.
- Handshake (long-header) packets are not copied. `initialRttMs` speeds up their recovery instead. Path probes and
  path MTU probes are not copied either, and nothing is copied before the handshake is confirmed: copies bypass loss
  recovery, so they must not count against a server's amplification limit.
- Delayed copies wait in a budget of 256 KiB per connection; a copy that does not fit is skipped and counted.

### R3 Runtime report

`HandlerService.GetQuicStats` (read-only; request: optional `tag`, `connections` flag) returns one entry for each
inbound and outbound handler that has QUIC connections built from its stream settings (Hy2 in this spec):

- `tag`, `direction` (inbound or outbound), `transport` (`hysteria`);
- `effective`: the R1 and R2 values after defaults, as the connections use them;
- `totals` since the handler started, monotonic across connection replacement (a closing connection folds its
  counters into the totals):
  - packets and bytes sent, received and lost. Sent counts originals only. Received packets are the processed ones,
    so a copy or a network duplicate counts under duplicates received instead; received bytes count every datagram.
    Lost counts the ack-eliciting packets declared lost, whatever the congestion controller: by loss detection (the
    packets Cubic counts today; an ECN-CE event no longer counts as one), and by a PTO probe that retransmits their
    frames, which is how a lone request's lost packet ends and which no controller counted before. It is no longer 0
    under BBR or Brutal;
  - spurious losses: packets declared lost, either way, and acknowledged later (quic-go's `detectSpuriousLosses`).
    They show when the thresholds or the PTO are too eager;
  - PTO expirations;
  - copies sent and their bytes, and copies skipped because the send queue was full;
  - duplicates received (packets dropped as already received: on a path where one end copies, the copies that
    arrived after their original);
- `connections_active`, `connections_total`;
- with `connections` set, one row per live connection (at most 256 per handler, `truncated` when cut): remote
  address, the user for an inbound Hy2 connection (email, never the auth secret), age, smoothed, minimum and
  latest RTT, RTT variance, and the same counters as `totals`.

Totals read quic-go's atomic counters. The handler's connection set is held under a short lock, never during I/O.
`xray api quicstats [-connections] [tag...]` prints the response; `-json` prints it as is.

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | Without the new fields, every connection behaves as with the unpatched library: the forked library passes its upstream test suite, and each new code path is gated on its field | quic-go fork suite; unit |
| FR-002 | R1 `maxAckDelayMs`: the local ACK timer and the advertised transport parameter follow it, and the peer's PTO includes the advertised value | quic-go fork unit; loss relay |
| FR-003 | R1 `ackElicitingThreshold`, `initialRttMs`, `ptoBackoffLimit` and the two loss thresholds change exactly their timer or threshold | quic-go fork unit |
| FR-004 | R2: small datagrams and tails are copied as defined (delay, at most once); receivers drop the copies and count them; copies stay outside loss recovery, congestion control and pacing; a full send queue skips the copy without a panic or a delay to the original; probes are never copied | quic-go fork unit and integration |
| FR-005 | JSON to proto to `quic.Config` for Hy2 dialer and listener, masque and splithttp HTTP/3; out-of-range values, a delay without a copy mode, and `maxAckDelayMs` on a parroting Hy2 or XHTTP outbound fail `run -test` | infra/conf unit; transport unit |
| FR-006 | Loss relay: a local UDP relay drops chosen datagrams of a Hy2 connection. A request whose first datagram is dropped completes within RTT + `maxAckDelayMs` + 15 ms with `maxAckDelayMs` 2, against at least 20 ms more by default (the advertised values differ by 23 ms). With `duplicateMaxSize` covering the request, it completes as if nothing was lost. Under a 300 ms blackout, `ptoBackoffLimit` 1 finishes within one PTO of the blackout's end | transport integration |
| FR-007 | R3 through a real gRPC server on a started instance: a Hy2 inbound and outbound with default and with configured parameters; totals stay monotonic when the outbound's connection is replaced; effective values match; rows are bounded; an unknown tag returns not found | started-instance CP-7 test |
| FR-008 | `xray api quicstats` text and `-json`, against a fake handler server and an older Core (`Unimplemented`) | CLI test |
| FR-009 | Loss counters do not depend on the congestion controller. With a custom controller installed through `SetCongestionControl` (Hy2's BBR and Brutal), losses and spurious losses are counted, including the packets a PTO probe retransmits. Cubic's loss-detection counts equal today's for the same packets, nothing is counted twice, and a PTO raises no congestion event | quic-go fork unit; loss relay; netem |
| FR-010 | Kernel loss (netem, `testing/netem/run.sh`), Hy2 on an emulated 7.5 ms path. Seeded random loss at 5%, 10%, 15% and 20% (user: "随机丢包率按照 5% 10% 15% 20% 四挡测试"), 200 lone requests per tuning at each level: the slowest tenth is at least 12 ms faster with spec024's recovery parameters on both ends; with copies on both ends a request is slowed by a loss at most a third as often as by default, and the slowest tenth is at least 25 ms faster; losses and PTOs are counted under BBR. A 300 ms blackout: with `ptoBackoffLimit` 2 the request completes within 100 ms of the path's return, at least 100 ms sooner than with the doubling PTO. The runner fails unless every netem test ran in an isolated namespace and passed, none skipped | netem |

## Control Plane

- **Configuration (CP-2).** The new fields come back through `ListInbounds` and `ListOutbounds` as part of the
  stream settings. `GetQuicStats` reports the effective values the connections run with.
- **Runtime state (CP-1, CP-2, CP-4).** Loss, timers, copies and connections per handler through
  `HandlerService.GetQuicStats`, next to `GetInboundMuxStats`. Handlers are its domain, so no new service is
  needed, and nodes that already enable `HandlerService` need no `api.services` change.
- **Read only and bounded (CP-3, CP-5).** No side effects. One entry per configured handler, at most 256
  connection rows each. No auth secret: users appear by email.
- **Compatibility (CP-6).** Additive proto fields and a new RPC. An older Core answers `Unimplemented`, and
  consumers show the section as unavailable, never as zero loss.
- **Tests (CP-7).** FR-007.
- **Consumers (CP-8).**
  - Sidecar spec019 puts the response under `/xray-debug` `quic` and exports per-handler metrics.
  - xray-config spec024 adds the Grafana loss and copy panels and an acceptance item where the fields are
    deployed.
  - The Portal does not show QUIC stats (dated non-goal below). Revised 2026-11-08: the Portal's QUIC tab shows them
    (xray-config [spec027](../../../xray-config/spec/027-portal-relay-track/spec.md)); xray-tui stays a non-goal.

## Non-goals (2026-11-07)

- Forward error correction, multipath QUIC, and the ACK frequency extension (draft-ietf-quic-ack-frequency).
  Copies cover the small and tail packets without changing the wire format.
- Copying handshake packets; congestion control changes.
- Stats for masque and splithttp HTTP/3 handlers: they take the R1 and R2 fields, but the fleet runs neither. For
  XHTTP the registration points are known: the dialer adds its connection after `DialEarly` and removes it in the
  `AfterFunc` that already watches it, the listener in `QListener.Accept`; the RPC then reports `splithttp` handlers
  that dial HTTP/3, with a second entry for `downloadSettings`, and no user column (VLESS users share a connection).
- Portal and xray-tui presentation of QUIC stats: Grafana carries the time series. Revisit when an operator needs
  them per node in the Portal. Revisited 2026-11-08 for the Portal (xray-config spec027, the current values from
  `/xray-debug` `quic`, no time series); xray-tui remains a non-goal.
