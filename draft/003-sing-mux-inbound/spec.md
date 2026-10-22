# Draft 003: sing-mux Inbound Multiplexing

- Feature ID: draft/003-sing-mux-inbound
- Created: 2026-10-22
- Status: Draft (design only; no implementation, dependency change or deployment authority)
- Baseline: Core `8d15534a` (develop)
- Consumer: [xray-sidecar draft001 entry multiplexing](../../../xray-sidecar/draft/001-entry-multiplex/spec.md)
- Evidence: [research.md](research.md) (file:line references; static reading, nothing compiled or run)

## Problem and Goal

Entry boards accept user SS-2022 on `ss-auto-in:512` / `ss-us-in:996`. Every client
request is its own TCP connection: on 2026-10-22 pgz.luck held 798 established
connections from 23 peer addresses, 20–30 per client address. mihomo (Clash Verge,
FlClash, CMFA, Mihomo Party), about 58% of active subscription links, can multiplex only
through sing-mux (`smux:`), and sing-box only through `multiplex`. Both dial the magic
destination `sp.mux.sing-box.arpa:444` inside the proxy protocol. Core has no sing-mux
server, so that destination is routed as an ordinary domain and fails. Mux.Cool already
works over SS-2022, but only Xray-family clients speak it.

Goal: an opt-in, per-inbound sing-mux server so mihomo and sing-box clients carry many
streams over few SS-2022 connections. Each stream is routed, sniffed and accounted
exactly like a non-multiplexed connection of the same user. The same port keeps serving
plain SS-2022 and Mux.Cool, decided per connection.

## Proposed Design Decisions

- **D1 Interception point.** A branch for `sp.mux.sing-box.arpa:444` (domain and port)
  in `mux.Server.Dispatch` and `DispatchLink`, next to the Mux.Cool check
  (`common/mux/server.go:42,63`).
  - Every AlwaysOn inbound wrapped by `h.mux` gains it: SS-2022 single- and
    multi-user, VLESS, Trojan, VMess, SS.
  - The carrier never reaches `DefaultDispatcher`, so it is not sniffed, routed or
    counted as user or domain traffic. Only the `inbound>>>tag` counters see its bytes,
    as for Mux.Cool.
  - SS-2022 early data is already written into the link, so no rewinding is needed.
- **D2 Opt-in per inbound, default off.**
  - A new inbound-level field enables it. The name is fixed in the spec, for example
    `"multiplex": {"sing": true}`.
  - With the field off, the magic destination is rejected at the mux layer with an
    access-log reason, with no DNS lookup and no dial. Today it is dialed and fails.
  - AnyTLS stays excluded (spec008 FR-010).
  - Opt-in keeps relay, trunk and admin inbounds unchanged and gives each inbound a
    kill switch.
- **D3 Library.**
  - Use `sagernet/sing-mux` `Service` with `HandlerEx` (TCP and UDP stream callbacks),
    `NewStreamContext`, a Core logger adapter, `Padding: false` (padded and unpadded
    sessions are both accepted) and Brutal disabled.
  - The server auto-detects smux, yamux or h2mux per session.
  - The metacubex client used by mihomo is wire-identical (research §3).
- **D4 Dependency.** sing-mux is its own module (`github.com/sagernet/sing-mux`) built
  on the base library `sagernet/sing`. Each release declares a minimum sing version,
  and a Go build links exactly one sing version, the highest required. v0.3.8 and later
  require sing ≥ v0.9.5; v0.3.4–v0.3.7 require ≥ v0.7.14. Core pins v0.8.14, and
  sing's only runtime user is AnyTLS (`proxy/anytls`, `proxy/anytls/internal/engine`),
  which runs on 12 nodes, mostly relays and LA schedulers (xray-config, 2026-10-22).
  - Option C (recommended): require sing-mux **v0.3.7** (2026-09-19) as a normal
    dependency. A probe module with sing v0.8.14 + sing-mux v0.3.7 passes `go mod tidy`
    (sing stays v0.8.14), `go build`, `go vet` and a `NewService` run (research §3).
    Against v0.3.9, the server path is identical: the only change is close callbacks
    on stream wrappers, used by the client's idle tracking. AnyTLS stays unchanged and
    no foreign code is copied. Moving past v0.3.7 later reopens A as a separate change.
  - Option A: bump sing to v0.9.x. The API breaks found (`CloseRead`/`CloseWrite`
    signatures, vectorised packet waiter aliases) are not used by AnyTLS. Buffer and
    copy behaviour changed, though, so every relay running AnyTLS gets new runtime
    behaviour. The spec008 regression and interop suites gate it.
  - Option B: vendor the sing-mux server subset (about 1.5k of 2.5k
    lines, without the client and brutal) at a pinned commit, built against v0.8.14.
    Every symbol it uses exists there, so AnyTLS stays unchanged and the blast radius is
    limited to inbounds with sing-mux on. The cost is syncing upstream fixes by hand;
    the wire protocol has been stable.
  - New modules either way: `hashicorp/yamux` (MPL-2.0) and `sagernet/smux` (MIT).
    sing-mux is GPL-3.0-or-later, like the sing module Core already links.
- **D5 Per-stream context.** Follow AnyTLS `streamContext`:
  - Copy `*Inbound` by value (tag, Source, User/email).
  - Assign a new session ID and fresh `Outbounds`/`Content`, copying `SniffingRequest`.
  - Give each stream its own cancel, so online-IP references are released per stream.
    Mux.Cool releases them only at carrier end.
  - Write one access-log entry per stream, with the email.
  - Dispatch to the raw dispatcher, so mux cannot nest. A magic destination arriving
    inside a Mux.Cool or sing-mux stream is rejected.
- **D6 Lifecycle.**
  - The preamble read gets the inbound's handshake deadline.
  - Streams use the user level's `connIdle`/`uplinkOnly`/`downlinkOnly` per stream, as
    AnyTLS does.
  - Carrier end interrupts every stream link.
  - h2mux idle carriers get a GOAWAY after 30 s. smux keepalive is off, so its idle
    carriers are bounded by the SS-2022 carrier's `connIdle`.
  - Handler `Close` joins all sing-mux sessions (spec002 accepted-stream retirement).
- **D7 Revocation.** Each new stream checks that the carrier's user is still on the
  inbound. After `RemoveUser`, new streams are refused. Open streams continue,
  matching the SS-2022 TCP contract (spec011). Closing carriers on removal is a
  separate option (C5).
- **D8 Bounds.**
  - Cap concurrent streams per carrier (proposed 256; h2mux defaults to 250) and
    reject the excess.
  - One goroutine per stream within that cap.
  - UDP streams in fixed-destination and PacketAddr modes; PacketAddr opens one link per
    destination, as AnyTLS `udp.go` does.
  - Brutal requests receive the library's "not enabled" error.
- **D9 Mux.Cool unchanged.** Mux.Cool is native Core code (`common/mux`) and does not
  use sing. The new branch is selected per connection by destination: `v1.mux.cool`
  goes to Mux.Cool, `sp.mux.sing-box.arpa:444` to sing-mux, anything else to the
  ordinary dispatch. The Mux.Cool client (FCC links, Lazy management) is untouched.
  The same registry also counts Mux.Cool carriers and streams, for observability only.

## Functional Requirements

- FR-001: D1/D2 interception, opt-in and fast rejection; AnyTLS excluded.
- FR-002: Stream parity. For a stream to target T by user U on inbound I, routing (tag,
  `user:` rules, domain, sniffed destination, `routeOnly`), user traffic counters,
  online IPs and the domain-traffic record (spec006) equal those of a plain SS-2022
  connection from U to T on I.
- FR-003: TCP and UDP streams (D8), smux/yamux/h2mux, with and without padding.
- FR-004: Lifecycle per D6, including handler close without leaked goroutines.
- FR-005: Revocation per D7.
- FR-006: Bounds per D8, with rejections counted.
- FR-007: Control plane per the section below.
- FR-008: Interop tests over real SS-2022 single- and multi-user inbounds:
  - clients: metacubex/sing-mux (mihomo pin) and sagernet/sing-mux (sing-box pin);
  - each protocol, with and without padding, plus a UDP PacketAddr case;
  - a disabled inbound that rejects the session.
- NFR-001: The non-mux path gains only one destination comparison. No secrets appear
  in logs or RPC output.

## Control Plane

- **New runtime state.** For each inbound tag:
  - sing-mux enabled, as loaded (runtime truth, not the file);
  - active carriers by protocol and padding;
  - active streams by network;
  - cumulative carriers and streams;
  - rejections by reason: disabled, revoked, limit, preamble, brutal;
  - active Mux.Cool carriers and streams (D9).
- **RPC.** Read-only `HandlerService.GetInboundMuxStats` (optional tag filter).
  - Bounded by the configured inbounds; short read locks; no secrets.
  - Gauges are not Stats counters, because reset-on-read would zero them.
  - An older Core answers `Unimplemented`.
- **Consumers.**
  - Sidecar `/xray-debug` field `inbound_mux` and Prometheus gauges, owned by Sidecar
    draft001.
  - Portal node view shows carriers and streams next to inbound user counts, or a dated
    non-goal (C4).
  - Consumers render an older Core as "unavailable".
- **Enablement.** `HandlerService` is already in `api.services` on the entry boards
  (verified on pgz.luck). Acceptance adds the RPC check where sing-mux is enabled.
- **Tests.** A contract test over a real gRPC server on a started instance, for a
  default config and for an inbound with sing-mux carriers (CP-7).

## Non-goals

- A sing-mux client or outbound.
- Brutal.
- Changing Mux.Cool behaviour.
- sing-mux on AnyTLS or Hy2.
- Stateless ticket or connection migration between carriers.

## Edge Cases

- **Carrier loss drops its streams.** Streams never migrate; mihomo opens a new carrier
  on the next request.
- **Mixed fleet.** A client multiplexing to a board with the field off gets an
  immediate rejection. The subscription's degrade path (Sidecar draft001 D4) then uses
  plain SS-2022. Bond scheduling can move users between boards, so every entry board
  enables it before subscriptions render `smux`.
- **Head-of-line blocking.**
  - smux v1 has one session-wide window, so a slow reader stalls its siblings.
  - h2mux and yamux have per-stream windows.
  - The subscription chooses h2mux.
- **Panic risks.** yamux panics on nil `LocalAddr`/`RemoteAddr`, so the conn handed to
  the Service must report non-nil addresses (`cnc` defaults do).
- **Upstream merges.** Upstream Xray is removing sing (#6286, #6831), so this stays
  fork-only, like AnyTLS.

## Success Criteria

- SC-001: A mihomo client with `smux` h2mux and `max-connections: 4` runs 20 concurrent
  HTTPS requests over at most 4 TCP connections to the inbound port. Per-email user
  traffic and AI/CN routing equal those of the non-mux run.
- SC-002: The FR-008 interop matrix passes.
- SC-003: A disabled inbound rejects the magic destination with no DNS query and no
  outbound dial.
- SC-004: After `RemoveUser`, the next stream open on a live carrier is refused.
- SC-005: The CP-7 contract tests pass, and an older Core is shown as unavailable by the
  Sidecar.

## Clarifications and Decisions

- [NEEDS CLARIFICATION C1: dependency — sing-mux v0.3.7 on sing v0.8.14 (recommended:
  no sing change, no copied code), vendoring, or a sing v0.9 bump?]
- [NEEDS CLARIFICATION C2: per-inbound opt-in (recommended) or always-on like Mux.Cool?]
- [NEEDS CLARIFICATION C3: per-carrier stream cap — 256 proposed.]
- [NEEDS CLARIFICATION C4: Portal presentation now, or a dated non-goal with
  `/xray-debug` and Grafana only?]
- [NEEDS CLARIFICATION C5: on `RemoveUser`, refuse new streams only (recommended,
  matches SS-2022) or also close the user's carriers?]

## Authorization and Revision Record

- 2026-10-22: the user asked to design multiplexing for entry connections ("回到多路复用的
  设计里"). No implementation, dependency change or deployment is authorized by this draft.
