# Tasks: Hy2 Padding Switch

- [x] T001 spec, plan and tasks; index row in `spec/README.md`; link and rollout task in xray-config spec024.
- [x] T002 Proto fields, JSON parsing, `ListInbounds` and `ListOutbounds`; FR-002.
- [x] T003 Writers with the padding choice, `interConn.HysteriaPadding`, authentication headers on the dialer and the
  listener; FR-001, FR-003, FR-004.
- [x] T004 Pool and relay streams follow their owner; FR-005.
- [x] T005 Loss relay sizes and copies; FR-006.
- [x] T006 `GetQuicStats` `effective.padding`; the CP-7 contract; FR-007.
- [x] T007 `xray api quicstats`; FR-008.
- [ ] T008 CP-8 consumers, specified and implemented in their own repositories:
  - the Sidecar spec019 amendment for `/xray-debug` `effective.padding`;
  - in xray-config: the renderer accepting the field, its check that fails the field outside a WireGuard tunnel
    (R1), the `quic_stats` acceptance comparison, and a `quic-check` variant entry for `hysteriaSettings` (under
    xray-config spec025, the gate).
- [ ] T009 Full suite, race, format and netem; `xrayctl build core`; `xrayctl gate plan` and its gate (FR-009).

## Evidence

- 2026-11-08 T001: specified from the code at `76e28afd` and approved by the user the same day: the Sidecar contract
  is a spec019 amendment, the `quic-check` variant goes under xray-config spec025, the gate shows "no worse" and the
  copy coverage while the size effect rests on the wg-ix measurement and post-rollout stats, and the field is allowed
  only inside a WireGuard tunnel, enforced by xray-config's checks. The padding sites are
  `transport/internet/hysteria/config.go` (ranges), `dialer.go` (authentication request), `hub.go` (both
  authentication answers) and `proxy/hysteria/protocol.go` (TCP request and response).
  - Zero padding is already accepted by our readers and by upstream Hysteria (`apernet/hysteria` `3c9dd62`,
    `core/internal/protocol`). Neither reads `Hysteria-Padding`.
  - The fake Hy2 server in `proxy/anytls/outbound_hy2_counts_test.go` already answers authentication without that
    header, and our client accepts it.
- 2026-11-09 T002–T007 (on `312ee32e` with spec020's amendment, the commit before this one):
  - T002: `transport/internet/hysteria` `Config.disable_padding` (14) and `app/proxyman/command` `QuicEffective.padding`
    (10), regenerated with protoc 34.1 and protoc-gen-go v1.36.11, each file keeping its existing header line (v6.33.5
    and v7.34.1); `HysteriaConfig.DisablePadding` (`json:"disablePadding"`) copied by `Build`.
    `TestHysteriaDisablePaddingParses`: unset and false leave it false, true sets it, a string and a number fail to
    parse. `TestHysteriaDisablePaddingOnHandlers`: an inbound's stream settings carry it; a relay outbound that writes it
    in `streamSettings` is still refused ("has streamSettings"). `ListInbounds` and `ListOutbounds` return it on a
    started instance (`TestQuicStatsPaddingContract`).
  - T003: `setPadding` writes `Hysteria-Padding` on the authentication request and both answers, and leaves it out with
    the field; `interConn.HysteriaPadding()` is set in `tcpOpen` and in both `StreamDispatcher` cases;
    `WriteTCPRequest` and `WriteTCPResponse` take the choice over one encoder each, and `sendsPadding(conn)` reads it
    through the stats wrapper (padding kept when a connection does not say). Tests:
    - FR-001: `TestTCPHeaderEncodingsUnchanged` (golden bytes for fixed paddings, one- and two-byte lengths);
      `TestTCPHeaderPaddingRanges` (10 000 headers each, request 64–511, response 128–1023, within 5 of both ends,
      Hy2's characters); `TestAuthPaddingRanges` (10 000 per message, 256–2047, within 5 of both ends; with the field the
      header is absent, not empty). The existing Hy2 suites pass unchanged.
    - FR-003: `TestTCPHeadersWithoutPadding` (length 0 and nothing after it; the readers take both forms and leave the
      payload); `TestAuthRequestPadding` (a real dialer against a raw HTTP/3 server); `TestAuthResponsePadding` (a real
      listener against a raw HTTP/3 client: the first and a repeated authentication on one connection, and the stream
      handed to the inbound); on instances built from JSON, `TestClientSendsPaddingAsConfigured` (a raw Hy2 server reads
      the authentication requests and the TCP requests) and `TestServerSendsPaddingAsConfigured` (a raw Hy2 client reads
      two authentication answers and five TCP responses).
    - FR-004: `TestPaddingEndsInteroperate`, TCP and UDP round trips with the field at neither end, at either end and at
      both.
  - T004 (FR-005): in `TestClientSendsPaddingAsConfigured` the session owner keeps up to two connections
    (`scaleUpStreams` 1), and its relay outbound is answered ok by the raw server. Three `0x401` and three `0x402`
    (index 1) streams run over both owner connections, and both connections negotiate the relay. With the owner's field,
    every padding length is 0 and no authentication request carries the header; without it, every padding is in range.
    Mutation check: without `noPadding` in `tcpOpen` and the dispatcher, the client test reads padded `0x401` and `0x402`
    requests and the server test padded responses.
  - T005 (FR-006), `TestHy2PaddingOffFitsCopies`, three runs, 50 lone 64-byte requests on new streams after a warm-up,
    parroting off and `duplicateMaxSize` 512 at both ends:
    - padding off: client 103–105 distinct datagrams, largest 125 B; server 132–137, largest 97 B; none above 512 B.
      With the first instance of every datagram dropped in both directions, 50 more requests took 5.2–5.7 ms on
      99–102 client and 109–119 server copies, with no PTO and no loss at either end;
    - padding on (recorded, no drops): client 14–15 of 103–105 datagrams above 512 B (largest 621–637 B), server 34–35 of
      117–140 (largest 1101–1115 B).
  - T006 (FR-007): `quicPadding` reads the handler's loaded Hy2 settings through `GetDisablePadding()`: `off`, `random`,
    and empty for settings without the getter. `TestQuicStatsPaddingContract`: `random` on a default inbound and
    outbound; `off` on an inbound, an outbound and a session owner with the field; the owner's relay outbound bound and
    without `effective`; a tag selects one handler, an unknown tag is `NotFound`. Echoes through the outbound and the
    relay with the field at both ends.
  - T007 (FR-008): `formatQuicEffective` appends `padding random`, `padding off`, or `padding unavailable` for an empty
    value; `TestQuicStatsText` covers the three (the third an older Core's handler), `TestQuicStatsJSON` the `padding`
    field, `TestQuicStatsOlderCore` is unchanged.
  - Field names for the consumers: `GetQuicStats` `handlers[].effective.padding` (proto `QuicEffective.padding`, JSON
    `padding`), `random` or `off`, absent from an older Core.
  - Suites: `go test ./... -count=1 -timeout 1h` with `resources/geoip.dat` and `geosite.dat`, on the tree with both this
    change and spec020's amendment: 111 packages ok, none failed (364 s). The run before it failed once in the untouched
    `app/proxyman/inbound` (`TestMultiListenBindFailureReleasesEarlierAddress` takes a free TCP port number and finds the
    same UDP port held, here by another package running at the same time); that package then passed alone five times and
    with `-count=3`. `-race -count=3` on `app/router`, `app/router/command`, `app/observatory/command`,
    `main/commands/all/api`, `proxy/hysteria`, `transport/internet/hysteria`, `app/proxyman/command`, `infra/conf` and
    `proxy/anytls` (`features/routing` has no tests): ok. `go vet` on them and `.github/check-gofmt.sh` (gofmt and gofumpt
    over the fork's files): clean.
  - Pending: T008 (Sidecar spec019 amendment and `/xray-debug`; in xray-config the renderer, the WireGuard-only check,
    the `quic_stats` acceptance comparison and the `quic-check` variant) and T009 (`testing/netem/run.sh`,
    `xrayctl build core`, `xrayctl gate plan` and its gate, FR-009); not run here.
