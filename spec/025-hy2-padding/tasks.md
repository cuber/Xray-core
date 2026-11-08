# Tasks: Hy2 Padding Switch

- [x] T001 spec, plan and tasks; index row in `spec/README.md`; link and rollout task in xray-config spec024.
- [ ] T002 Proto fields, JSON parsing, `ListInbounds` and `ListOutbounds`; FR-002.
- [ ] T003 Writers with the padding choice, `interConn.HysteriaPadding`, authentication headers on the dialer and the
  listener; FR-001, FR-003, FR-004.
- [ ] T004 Pool and relay streams follow their owner; FR-005.
- [ ] T005 Loss relay sizes and copies; FR-006.
- [ ] T006 `GetQuicStats` `effective.padding`; the CP-7 contract; FR-007.
- [ ] T007 `xray api quicstats`; FR-008.
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
