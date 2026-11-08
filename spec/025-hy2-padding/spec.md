# Feature Specification: Hy2 Padding Switch

- Feature ID: 025-hy2-padding
- Created: 2026-11-08
- Status: Approved 2026-11-08 (user: "随机panding应该可以考虑支持关闭，chrome的特征我们都关了"; on review, "1-4都ok，5 可以的，
  仅在 wg 隧道内允许使用，这个可以加到 xrayctl 的检查里，不能隐式开启"). Rollout is a separate step (xray-config spec024).
- Baseline: Core `76e28afd` on `fork/2026-11-07/0888beb38a9b`, quic-go `67b1e9ed` on `fork/2026-11-07/184d081eef3e`
  ([spec023](../023-fork-lines/spec.md)). No library change.
- Depends on: [spec021](../021-quic-loss-recovery/spec.md) (`GetQuicStats` `effective`, R2 copies) and
  [spec024](../024-hy2-relay-session/spec.md) (session owners and relay outbounds).
- Consumers: [Sidecar spec019](../../../xray-sidecar/spec/019-quic-stats/spec.md) (`/xray-debug` `quic`, to be amended
  for R4); rollout on the wg-ix segment in [xray-config spec024](../../../xray-config/spec/024-wg-ix-quic-recovery/spec.md).

## Problem and Goal

Hy2 pads its control messages with random characters so that their lengths do not identify the protocol:

| Message | Sent by | Padding today | How often |
|---|---|---|---|
| Authentication request (HTTP/3 `POST /auth`) | client | `Hysteria-Padding` header, 256–2047 characters | once per QUIC connection |
| Authentication response (status 233) | server | `Hysteria-Padding` header, 256–2047 characters | once per QUIC connection |
| TCP request (`0x401`, also the body of a relayed `0x402`) | client | 64–511 bytes | every new TCP stream |
| TCP response | server | 128–1023 bytes | every new TCP stream |

The ranges are half-open, `Min + rand.Intn(Max − Min)` (`transport/internet/hysteria/config.go`), as in upstream
Hysteria. UDP messages carry no padding.

The TCP headers often travel as packets of their own. The client writes its request into a buffered writer and
flushes it alone (`SetBuffered(false)` in `proxy/hysteria/client.go`) before it copies any payload; the server does
the same with its response (`proxy/hysteria/server.go`). A new stream therefore often opens with a packet in each
direction that is a few hundred bytes larger than its content, about 0.9 KB per stream on average.

On a path whose loss grows with packet size, short exchanges lose mostly these padded packets. On the wg-ix segment
(Hy2 inside WireGuard over an IX path, xray-config spec023), loss during episodes rises smoothly with the outer packet
size (Measurements). A burst observatory probe is one request and its response on a new stream, so most of its bytes
are padded header packets. The padded packets are also often larger than spec021 R2's `duplicateMaxSize` 512, so
size-based copies skip the very packets that open a stream.

Inside WireGuard nothing on the wire shows Hy2, and the outbounds of that segment already turn Chrome parroting off
(spec021 R1). There, the padding hides nothing (user, 2026-11-08: "随机panding应该可以考虑支持关闭，chrome的特征我们都关了").

Goal: an opt-in `disablePadding`, with which one end stops padding the messages it sends. Unset is exactly today's
behavior, and the field is set only where fingerprinting does not matter (user, 2026-11-07:
"首先新增的配置，如果不配，等同于原始配置"). The peer needs nothing, and the control plane reports the value each handler
runs with.

## Contract

### R1 Configuration

| JSON | Proto | Today | Meaning |
|---|---|---|---|
| `hysteriaSettings.disablePadding` | `transport/internet/hysteria` `Config.disable_padding` (14) | false | This end sends its Hy2 messages without random padding (R2) |

- It applies to the end whose stream settings carry it: an inbound is the server end and an outbound the client end,
  a session owner included. A relay outbound has no stream settings (spec024 R1 refuses them) and takes its owner's
  (R3).
- In intent it is a sibling of `finalmask.quicParams.disableChromeParrot`: an explicit opt-in for paths where the
  fingerprint does not matter, such as inside WireGuard. It sits in `hysteriaSettings` because the padding belongs to
  Hy2, not to QUIC. Neither field implies or requires the other, and no check couples them.
- Core has no other default. xray-config sets the field per path and never fleet-wide.
- **Only inside a WireGuard tunnel** (user, 2026-11-08: "仅在 wg 隧道内允许使用，这个可以加到 xrayctl 的检查里，不能隐式开启").
  The field is written explicitly on each handler that uses it, and xray-config's checks fail it on any Hy2 handler
  that is not inside one: an inbound must listen only on a WireGuard interface address, and an outbound must dial a
  WireGuard peer address (or be a session owner that does). Nothing turns it on implicitly, and Core does not try to
  tell a tunnel from the Internet.
- Without the field, configuration, wire format and behavior are exactly today's.

### R2 What each end sends

| End | Messages it controls | With `disablePadding` |
|---|---|---|
| client | the authentication request; every TCP request header, relayed `0x402` bodies included | no `Hysteria-Padding` header; padding length 0 and no padding bytes |
| server | the authentication response, first and repeated; every TCP response header | no `Hysteria-Padding` header; padding length 0 and no padding bytes |

- **Each end controls only what it sends.** The setting never changes what an end accepts, and an end never asks
  its peer to stop.
- **Unnegotiated and wire-compatible**, like spec021's copies. Receivers already accept a zero padding length
  (`if paddingLen > 0` in `ReadTCPRequest` and `ReadTCPResponse`), and neither this Core nor upstream Hysteria reads
  `Hysteria-Padding` (upstream `AuthRequestFromHeader` reads only the credential and `Hysteria-CC-RX`; its TCP parser
  is the same as ours). The receive bound `MaxPaddingLength` (4096) is unchanged. A peer needs no support, an older
  Core or upstream Hysteria included, and each end decides alone.
- The header is left out rather than sent empty.
- UDP messages, the masquerade handler's responses and the `Hysteria-Relay` negotiation are unchanged.

### R3 Relay outbounds and session pools

- **Padding belongs to the connection that carries a stream, not to the outbound that opens it.** A relay
  outbound's streams run on its session owner's connections (spec024 R4), so they are padded or not as the owner's
  `disablePadding` says, the `0x402` body included. A relay outbound has no setting of its own and cannot configure
  one.
- Every connection of an owner's pool follows the owner, and so do its probes.
- On the server, the setting belongs to the inbound. Every client of the inbound gets the same responses, and so do
  the users relayed through it. An inbound reachable from outside the tunnel pads for everyone or for no one.

### R4 Runtime report

- `HandlerService.GetQuicStats` (spec021 R3) gains `QuicEffective.padding` (10): `random` (today's padding) or `off`.
  It is reported for every handler entry that has `effective`: inbounds, outbounds and session owners. It reads the
  handler's loaded Hy2 settings, the same value its dialer or listener uses.
- A relay outbound's entry has no `effective` (spec024 R7). Its streams use the `effective.padding` of the owner named
  in its `relay.session`.
- A Core without this spec leaves `padding` empty. The field is a string so that an empty value is not read as
  `random`.
- `xray api quicstats` prints `padding random|off` with the other effective values, and `padding unavailable` for an
  empty value; `-json` prints the response as is.

## Measurements (2026-11-08)

The wg-ix segment tgz.lite ↔ isz5, Hy2 inside WireGuard. During loss episodes, loss rises smoothly with the outer
(WireGuard) packet size, roughly 3×10⁻⁴ per byte:

| Outer packet size | Loss in episodes |
|---|---|
| ≤160 B | 5.9% |
| 700–1000 B | 19.6% |
| 1350–1400 B | 37.1% |

From these rates and the packet sizes of a lone request and its response (the observatory probe), the estimated
chance that the exchange hits a loss:

| | Padding on | Padding off |
|---|---|---|
| During loss episodes | about 35% | about 13% |
| Over the day | about 8% | about 3% |

- Bulk transfers are unaffected. Their packets are full-size whatever the header, and the header is a negligible share
  of a long stream's bytes.
- About 0.9 KB is saved per stream: the mean request and response padding (287.5 + 575.5 bytes) and one byte of each
  length.
- With padding off, a stream's opening packets fall under spec021 R2's `duplicateMaxSize` 512, so size-based copies
  would cover them cheaply. With padding, they are often above it and are copied only when they end a send round
  (`duplicateTail`).
- Caveat: the observatory probe opens a new stream every time, so probe delay spikes improve more than bulk traffic
  does. A long-lived stream pays the padding once.

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | Without `disablePadding`, every message is padded as today. For a fixed padding string, the encoders' output equals today's byte for byte. Over 10 000 headers per message, the lengths stay in today's half-open ranges and come within 5 of both ends. The authentication request and response carry `Hysteria-Padding` with 256–2047 characters. The existing Hy2 suites pass unchanged | unit; existing suites |
| FR-002 | JSON to proto to runtime: `hysteriaSettings.disablePadding` sets `Config.disable_padding`, unset leaves it false, and a non-boolean value fails to parse. The field comes back through `ListInbounds` and `ListOutbounds`, and instances built from that JSON behave as FR-003 says | infra/conf unit; started instance |
| FR-003 | Zero padding on all four messages, seen on the wire. A client with the field sends an authentication request without `Hysteria-Padding` and TCP requests with padding length 0 and no padding bytes, read by a raw HTTP/3 server. A server with the field answers authentication, first and repeated, without the header, and answers TCP requests with padding length 0, read by a raw client. With the field at both ends, TCP and UDP round trips between a real client and server succeed | transport and proxy integration |
| FR-004 | Mixed peers interoperate for TCP and UDP: client off with server on, and client on with server off. Each end's messages follow only its own setting; a padded end's lengths are as in FR-001 | integration |
| FR-005 | Relay streams follow their owner. With the field on the owner, relayed `0x402` requests carry padding length 0; without it, they are padded as in FR-001. A raw server that accepts the relay negotiation reads them. Every connection of the owner's pool follows the owner. A relay outbound still cannot carry `streamSettings` (spec024 R1) | integration |
| FR-006 | Wire size, on spec021 FR-006's loss relay with parroting off, as on the rollout path. With the field at both ends, every 1-RTT datagram of a lone 64-byte request and its echoed response on an established connection is at most 512 bytes. With `duplicateMaxSize` 512 at both ends and the relay dropping the first instance of each such datagram, 50 such requests complete without a PTO. The same run with padding on counts the datagrams above 512 bytes; this count is recorded, not asserted | transport integration |
| FR-007 | R4 through a real gRPC server on a started instance (CP-7). `effective.padding` is `random` for a default inbound and outbound, and `off` for an inbound, an outbound and a session owner with the field. A relay outbound's entry has no `effective`. Tags and `NotFound` behave as in spec021 | started-instance contract test |
| FR-008 | `xray api quicstats` prints `padding random`, `padding off`, or `padding unavailable` when the value is empty, against a fake handler server and an older Core's response | CLI test |
| FR-009 | The Akile gate for Hy2 changes at the tier `xrayctl gate plan` picks (xray-config spec025, `stability-check` skill). Default settings must behave as the baseline, and a group with the field at both ends must be no worse than default | Akile gate |

## Control Plane

- **Configuration (CP-2).** `disablePadding` comes back through `ListInbounds` and `ListOutbounds` as part of the
  stream settings' `hysteriaSettings`.
- **Runtime state (CP-1, CP-2, CP-4).** The value each handler runs with is in `HandlerService.GetQuicStats`
  `effective.padding` (R4), beside spec021's recovery and copy values. It is read from the Hy2 settings the handler
  loaded, the object its dialer or listener uses, not from a configuration file. The service is already enabled where
  spec021 runs, so `api.services` does not change. The setting cannot change at runtime, so there is no counter or
  per-connection field.
- **Read only and bounded (CP-3, CP-5).** No side effects. One short string per handler entry. Nothing secret.
- **Compatibility (CP-6).** An additive proto field. A Core before this spec leaves `padding` empty. Such a Core always
  pads, but consumers still show the value as unavailable and never infer `random` from an empty field.
- **Tests (CP-7).** FR-007.
- **Consumers (CP-8).**
  - Sidecar: `/xray-debug` `quic.handlers[].effective.padding` passes Core's value through, and becomes
    `unavailable` when Core sends none. Spec019 Q1 copies `effective` field by field (`xray_debug_quic.go`), so the
    field must be added there. The Sidecar contract is an amendment to Sidecar spec019, written before the Sidecar
    change. There is no metric: the value is configuration, not a time series, and acceptance reads it.
  - xray-config spec024: acceptance `quic_stats` compares `effective.padding` with the tracked `disablePadding`
    (`off` where written, `random` elsewhere). It fails where the field is written and Core reports none.
  - The Portal and xray-tui do not show the value (dated non-goal below).

## Non-goals (2026-11-08)

- Changing the padding ranges, making them configurable, or padding to fixed lengths.
- Padding UDP messages.
- Negotiating padding, or asking the peer to stop. Each end decides what it sends.
- Writing the TCP header and the first payload in one packet. That would save a packet per stream, but it changes
  buffering on the hot path and is a separate change.
- A fleet default, and turning the field on for paths where Hy2 is visible on the wire (R1: inside a WireGuard tunnel
  only, enforced by xray-config's checks).
- Any coupling with `disableChromeParrot`, in either direction.
- A Sidecar metric for the value; Portal and xray-tui presentation. Revisit when an operator needs it per node in the
  Portal.

## Edge cases

- An older Core given the field ignores it: Xray's JSON loader ignores unknown keys. It keeps padding, which is
  wire-safe. `effective.padding` is then empty, and acceptance fails where the field is written.
- A peer on an older Core or on upstream Hysteria reads zero padding and a missing `Hysteria-Padding` correctly, in
  both directions.
- Only one end is off: only that end's messages shrink.
- A connection authenticated before a configuration change keeps the setting it was built with. A configuration
  reload restarts Core, so every connection and pool is rebuilt with the new value.
- A repeated authentication request on an authenticated connection is answered with the inbound's setting, like the
  first one.
- A target address may be long (up to `MaxAddressLength`, 2048 bytes). Its request header can then exceed 512 bytes
  with or without padding. FR-006 bounds the probe's datagrams, not those of every possible address.
