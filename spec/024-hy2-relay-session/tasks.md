# Tasks: Hy2 Relay Sessions

- [x] T001 spec, plan and tasks; index row in `spec/README.md`.
- [x] T002 Proto fields and JSON parsing with every R1 check; FR-002.
- [x] T003 Relay negotiation and the `0x402` frame, client and server; R3 identity and re-check; FR-003, FR-004 (TCP).
- [x] T004 UDP relay: ID layout, server session users; FR-003, FR-004 (UDP).
- [x] T005 Pool: placement, scaling, stream credit, drain and spare; FR-005.
- [x] T006 Health and redial; FR-006.
- [x] T007 Probe placement and relay status in `track`; FR-007.
- [x] T008 `GetQuicStats` session, connection and relay fields; the CP-7 contract; FR-008.
- [x] T009 `xray api quicstats`; FR-009.
- [x] T010 netem; FR-010.
- [x] T012 quic-go fork spec002 (`PacketsAcked`, `BytesInFlight`) for R5's stall check; the `replace` names the pushed
  commit `67b1e9ed`.
- [ ] T011 Full suite, race, format; `xrayctl build core`; `xrayctl gate plan` and its gate.

## Evidence

- 2026-11-08 T001: designed with the user in xray-config draft/006 and promoted the same day; the spec carries the draft's
  Core part and its throughput measurements. The spec files go into the repository with the first code commit, so the
  deployed `31a83508` is not followed by a documentation-only commit.
- 2026-11-08 T002: `infra/conf/hysteria_relay_test.go`: owners and relays linked with indexes in configuration order and
  the owner's negotiation list; a session without relays and a plain Hy2 outbound unchanged; every R1 refusal (relay
  `streamSettings`, address, missing auth or session, unknown session, an owner without a pool, a freedom owner, a chain,
  the owner's credential, a credential twice, 256 relays, no keepalive, keepalive not below the idle timeout, the four
  ranges), a session on an inbound, and the server relay lists (unknown email, itself, twice, no email).
- 2026-11-08 T003, T004, T008: `proxy/hysteria/relay_session_test.go`, two started instances: the owner's own user and
  two relayed users over one QUIC connection on both ends, TCP and UDP, each answered by its own user's route and
  counted in its own user statistics; a user not on the list (`denied`) and an unknown credential (`unknown`) refused for
  TCP and UDP; `RemoveUser` refuses the next stream on the live connection while the other user works; every stream's
  count released when it closes. The CP-7 contract (FR-008): the owner's session and row (state, bindings 1–4), the four
  relay outbounds (`bound` with email, `denied`, `unknown`, no totals), the server row's two relayed users, no credential
  in either response. `transport/internet/hysteria/relay_test.go`: encodings, malformed lists, UDP IDs.
- 2026-11-08 T005, T006, T007: `transport/internet/hysteria/pool_test.go`, real loopback connections, a fake clock and a
  socket per dial: scale up at `scaleUpStreams` to `maxConnections` with streams on the least loaded; drain after 60 s at
  25%, spare, closed after 5 minutes; reactivation before dialing; the wait for stream credit at the server's limit;
  a connection whose socket is cut becomes suspect and takes no new streams; a closed connection replaced; probes in
  turn over both connections. `-race -count=3`. `app/observatory/burst/track_relay_test.go`: `unknown`, `denied` and
  `unsupported` relays dead with their reason in the observation and in `GetTrack`'s `refused`; `bound`, `no_connection`
  and other outbounds follow the probe.
  Found while testing: received packets do not show a stall (with the owner's direction cut the server keeps
  retransmitting), so R5 reads the library's acknowledgment progress; first approximated in Core from PTO counts and RTT
  samples, then, on the user's word, added to the fork as its spec002 (T012).
- 2026-11-08 T009: `TestQuicStatsRelaySessionText`.
- 2026-11-08 T010: `transport/internet/hysteria/netem_pool_test.go` on the emulated 7.5 ms path, `testing/netem/run.sh
  -test.count=3`, all six netem tests passing three times: 600 concurrent streams grow the pool to 4 connections with
  none failing for credit, and it drains back to one; one of two connections closed, ten probes succeed; after a server
  restart one stream fails on the closing connection and a stream succeeds 35 ms after the server's return; during a
  bulk transfer on a 20 Mbit/s path the probe's median rises from about 10 ms to 31–35 ms. spec021's two netem tests
  pass as before.
- 2026-11-08 T012: the fork's spec002 commit `67b1e9ed` pushed on `fork/2026-11-07/184d081eef3e`; `replace` →
  `v0.61.1-0.20261108044907-67b1e9edf55c` (`go.mod`, `go.sum`: only the quic-go lines), `TestQuicGoIsReplacedByTheFork` passes.
- 2026-11-08 T011 (suites): `go test ./... -count=1` 111 packages ok; `-race` on the 11 touched packages ok; `gofmt` clean on
  the touched files. The build and the gate follow the commit.
