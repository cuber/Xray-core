# Feature Specification: sing-mux Inbound Multiplexing

- Feature ID: 013-sing-mux-inbound (promoted from draft003, 2026-10-22; draft003 is reserved)
- Created: 2026-10-22
- Status: Implemented and verified locally; not built for release or deployed.
  Rollout (node enablement and subscriptions) is a separate decision.
- Baseline: Core `8d15534a` (develop)
- Authorization (2026-10-22): "你先把实现做好，完整测试通过；并且要用 mihome 和 singbox
  测试", and "这个阶段portal 和订阅都先别做；只做core". This covers Core only. The rollout
  plan is discussed after implementation.
- Consumer proposal: [xray-sidecar draft001 entry multiplexing](../../../xray-sidecar/draft/001-entry-multiplex/spec.md)
  (draft; deferred by the user).
- Evidence: [research.md](research.md) for design facts, and [validation.md](validation.md)
  for test results.

## Problem and Goal

Entry boards accept user SS-2022 on `ss-auto-in:512` / `ss-us-in:996`, and every client
request is its own TCP connection. On 2026-10-22, pgz.luck held 798 established
connections from 23 peer addresses, 20–30 per client address.

mihomo (Clash Verge, FlClash, CMFA, Mihomo Party; about 58% of active subscription
links) multiplexes only through sing-mux (`smux:`), and sing-box only through
`multiplex`. Both dial `sp.mux.sing-box.arpa:444` inside the proxy protocol. Before this
feature, Core had no sing-mux server.

Goal: an opt-in, per-inbound sing-mux server.

- Clients carry many streams over few SS-2022 connections.
- Each stream is routed, sniffed and accounted like a plain connection of the same user.
- The same port keeps serving plain SS-2022 and Mux.Cool, chosen per connection.

## Contract

### Configuration

```json
{"tag": "ss-auto-in", "protocol": "shadowsocks", "port": 512,
 "singMux": {"enabled": true, "maxStreams": 256}}
```

- **Fields.**
  - `singMux.enabled` defaults to false.
  - `singMux.maxStreams` bounds the concurrent streams per carrier. 0 selects 256, and
    the maximum is 4096.
- **Placement.** The setting sits at inbound level (`ReceiverConfig.sing_mux`, proto
  field 8).
  - It is rejected on `anytls` inbounds (spec008 FR-010).
  - On other inbounds it is accepted; the intended use is SS-2022 entries.
- **Without the field.** The inbound is unchanged. The carrier destination is routed
  like any other destination, so a proxy chain can forward it to a sing-mux server
  upstream.

### Behaviour

- **D1 Interception.** `mux.Server.Dispatch`/`DispatchLink` check each connection, next
  to the Mux.Cool check (`common/mux/server.go`). A TCP destination of exactly
  `sp.mux.sing-box.arpa:444` on an enabled inbound becomes a sing-mux carrier, and
  `v1.mux.cool` stays Mux.Cool. Everything else, including the carrier destination on a
  disabled inbound, takes the ordinary dispatch.
  - The carrier never reaches `DefaultDispatcher`: it is not sniffed, routed or counted
    as user or domain traffic.
  - Only `inbound>>>tag` counters see its bytes, as for Mux.Cool.
- **D3 Server.** `common/mux/singmux` wraps `sagernet/sing-mux` v0.3.7 `Service`.
  - smux, yamux and h2mux are detected per carrier, padded or not.
  - Brutal requests get the library's "not enabled" answer.
- **D4 Dependency.** The pins are sing-mux v0.3.7, hashicorp/yamux v0.1.2 and
  sagernet/smux v1.5.50-sing-box-mod.1. `sagernet/sing` stays at v0.8.14, shared with
  AnyTLS (spec008); a later bump must run both specs' suites.
  - Evaluation: research §3 and §6.
  - User decision C1, 2026-10-22: "用 sing-mux v0.3.7，不升级 sing 很合理".
- **D5 Per-stream context.**
  - Each stream copies the carrier's `Inbound` by value (tag, source, user).
  - It gets its own session ID, outbounds and content (sniffing copied from the inbound)
    and its own cancel, so online-IP references are released per stream.
  - It writes one access-log entry with the user's email.
  - It is dispatched past the mux layer. A sing-mux or `v1.mux.cool` target inside a
    stream is refused as nested.
- **D6 Lifecycle.**
  - The carrier preamble (version, protocol, padding flag) must arrive within the
    user level's handshake timeout.
  - Each stream uses the user level's `connIdle`/`uplinkOnly`/`downlinkOnly`.
  - Carrier end, handler close or `Server.Close` cancels every stream.
    `AlwaysOnInboundHandler.Close` joins carriers and streams through `mux.Server.Close`.
  - h2mux carriers without streams close after 30 s (server GOAWAY).
- **D7 Revocation.** On user-managed inbounds, a stream of a user no longer on the
  inbound is refused (`revoked`). Open streams continue, matching the SS-2022 TCP
  contract (spec011).
- **D8 Bounds.**
  - Streams beyond `maxStreams` on one carrier are refused (`limit`).
  - A UDP stream (fixed destination or PacketAddr) holds at most 64 destination links.
  - Datagrams above `buf.Size` end the stream.
- **D9 Mux.Cool.** Behaviour is unchanged. A registry counts its carriers and streams
  for the control plane.

## Functional Requirements

| ID | Requirement | Verified by ([validation](validation.md)) |
|---|---|---|
| FR-001 | D1 interception on enabled inbounds; standard SS-2022 on an enabled inbound and disabled inbounds unchanged; AnyTLS rejected | V-02, V-03, V-06, V-10 |
| FR-002 | Stream parity: inbound tag, `user:` rules, domain and sniffed routing, user traffic, distinct session IDs | V-01, V-02, V-04, V-05 |
| FR-003 | TCP and UDP (fixed and PacketAddr) streams; smux/yamux/h2mux, with and without padding | V-01, V-02, V-04, V-05 |
| FR-004 | Lifecycle D6, including preamble timeout, close joins and idle release | V-01, V-02, V-07 |
| FR-005 | Revocation D7 over the real `AlterInbound` path | V-01, V-03 |
| FR-006 | Bounds D8, with refusals counted | V-01 |
| FR-007 | Control plane (below) | V-03 |
| FR-008 | Interop with released mihomo and sing-box binaries; h2mux stress against Core's Go 1.27 HTTP/2 | V-04, V-05, V-07 |
| NFR-001 | The non-mux path adds one boolean check on disabled inbounds; no secrets in logs or RPC output | code review, V-08 |

## Control Plane

- **Runtime state per inbound.**
  - sing-mux: enabled as loaded, `maxStreams`, active carriers by protocol, active padded
    carriers, active TCP and UDP streams, total carriers and streams.
  - Refusals by reason: `preamble`, `closed`, `limit`, `revoked`, `invalid`, `nested`,
    `dispatch`.
  - Mux.Cool: active carriers, active streams and total carriers.
- **RPC.** Read-only `HandlerService.GetInboundMuxStats(tag)`.
  - An empty tag lists every inbound, sorted by tag. An unknown tag is an error.
  - Values come from the running handlers, not the configuration file.
  - Responses are bounded by the configured inbounds and contain no secrets.
  - Gauges are not Stats counters, because reset-on-read would zero them.
  - An older Core answers `Unimplemented`.
- **Enablement.** `HandlerService` is already in `api.services` on every entry board.
  No new service is registered.
- **Tests.** A contract test runs over a real gRPC server on a started instance (V-03).
- **Consumers (dated non-goal, 2026-10-22).** The user deferred Portal and subscription
  work ("这个阶段portal 和订阅都先别做；只做core").
  - The Sidecar `/xray-debug` field, Prometheus gauges, Portal view and acceptance check
    are planned in Sidecar draft001 D8 and stay unimplemented until that draft is taken
    up.
  - Until then, the RPC is readable with grpcurl through the existing API listener.

## Non-goals

- A sing-mux client or outbound.
- Brutal.
- Changing Mux.Cool behaviour.
- sing-mux on AnyTLS.
- Stream migration between carriers.
- Inbounds on HTTP/2-based transports (gRPC, h2, XHTTP): sing v0.9.5's HTTP/2
  error-wrapping fix is not present (research §3). Enabling there needs that wrapper
  first.
- Node configuration, subscriptions, Sidecar and Portal (deferred by the user).

## Edge Cases

- **Carrier loss.** Its streams drop with it, and clients open a new carrier on the next
  request (V-07).
- **Mixed fleet.** A client multiplexing to an inbound without the field has its carrier
  routed as a domain, which fails. Rollout must enable every entry board that a user can
  be scheduled to before subscriptions render `smux`.
- **Older Core.** An older Core accepts `singMux` silently: unknown JSON fields are
  ignored, and the approved `8d15534` build reports "Configuration OK" for an entry
  config with it. That Core still serves no carrier. Rollout must check the runtime
  `GetInboundMuxStats` `enabled` value (CP-2), not the config test alone (V-09).
- **Head-of-line blocking.** smux v1 shares one session window. h2mux and yamux have
  per-stream windows.
- **Client sizing bug.** sagernet's client mis-sizes `ListenPacket` with an empty
  destination. Only that client is affected, and mihomo always passes a resolved one.

## Success Criteria

- SC-001: mihomo (`smux` h2mux, `max-connections: 4`) carries 20 concurrent streams over
  at most 4 TCP connections, with per-user routing and traffic intact. **Met** (V-04).
- SC-002: The FR-008 interop matrix passes. **Met** (V-04, V-05, V-07).
- SC-003: An inbound without the field is unchanged and serves no carrier. **Met** (V-03).
- SC-004: After `RemoveUser`, the next stream on a live carrier is refused, and the open
  stream continues. **Met** (V-03).
- SC-005: The CP-7 contract tests pass. **Met** (V-03). Sidecar "unavailable" rendering
  is deferred with the consumers.

## Revision Record

- 2026-10-22 draft003: design.
- 2026-10-22 promoted to 013 on implementation authorization. Changes from the draft:
  - **Disabled inbounds are unchanged.** The draft proposed rejecting the carrier
    destination on disabled inbounds. Implementation showed this breaks forwarding: a
    SOCKS inbound relaying a sing-mux carrier to an upstream server refused it. Disabled
    inbounds now keep routing it like any other destination, a strict no-op, and the
    `disabled` refusal reason was dropped.
  - **Decisions.**
    - C2: per-inbound opt-in.
    - C3: default cap 256, configurable up to 4096.
    - C4: Portal is a dated non-goal.
    - C5: refuse new streams only.
