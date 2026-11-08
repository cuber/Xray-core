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
- [x] T011 Full suite, race, format; `xrayctl build core`; `xrayctl gate plan` and its gate.
- [x] T013 Relay outbounds count their traffic under their own tag (R4 amendment, FR-011): `Handler.CountConnection`
  and the relay dial.
- [x] T014 The gate for the T013 candidate, then the rollout on the nodes that run relays (xray-config spec026).

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
- 2026-11-08 T011, candidate `76e28afd` (Linux SHA-256 `91f88b3db2b6…`); `xrayctl gate plan` for the target nodes chose L2:
  - `relay-check` (xray-config spec026 FR-009) on the three Akile pairs passed on its second run (1685 s); the first
    run's three failures were all in the check, which was fixed (one UDP flow per pair through a server-side demux,
    latency measured from each outbound's own round trip, the loss relay kept up through the restart phase, bulk
    progress recorded per second). Routing and the refused user right on all pairs; 600 streams with 0 failures spread to
    4 connections and were back to 1 after 191 s. At 15% loss each relay stayed within the noise of its reference
    (hk→tk 32–40 ms against 33–37 ms added per request, tk→la 75–93 against 76–94, la→hk 131–159 against 114–151).
    Bulk echo 87–106 Mbit/s with no stall, and the owner's request median unchanged meanwhile. A server restart of
    1.12–1.15 s cost the client 1.05–1.24 s.
  - `quic-check` PASS (267 s). Added latency per request at 15% injected loss (baseline / default / tuned / copies):
    hk→tk 43 / 42 / 35 / 14 ms, tk→la 82 / 78 / 66 / 26 ms, la→hk 147 / 160 / 115 / 43 ms.
  - `singmux-check` A/B against `31a8350` (measured within 7 days): PASS with each of hk.akile.lite and la.akile.lite as
    server; candidate peaks 1125 / 1115 goroutines, 109 / 110 h2mux handlers, 19 / 20 carriers, 73 / 77 MiB (baseline
    1171 / 1167, 101 / 102, 20 / 20, 73 / 79 MiB); handlers and carriers back to 0, no TCP failure in the quiet phase.
  - `migration hy2-check --duration 600` on hk.akile.lite: mihomo / sing-box TCP 11609 / 11608, connection churn
    2357 / 2357 and UDP 5901 / 5901, all successful; idle recovery and the negative control passed; Core 56 MB, no restart. Reverted
    afterwards.
  - Deployed 2026-11-08 to isz5, tgz.lite and vgz.ecs with Sidecar `98ee339` (Core interruptions 101, 206 and 199 ms);
    tgz.lite's acceptance shows one session owner with seven relay outbounds bound.
- 2026-11-08 T013: found on tgz.lite the same day: its seven relay outbounds counted 0 bytes since the deployment while
  their session carried several GB. A relay opened its streams through the transport, not its handler's `Dial`, which
  is where outbound counters wrap a connection. The relay dial now wraps its stream with the relay handler's counters
  (`Handler.CountConnection`, an optional interface the proxy checks); the UDP path already unwraps a counted
  connection. `TestRelaySessionCountsTrafficUnderEachRelay` (client outbound statistics on) fails without the change
  (relay-a 0/0, relay-b 0/0) and passes with it; the owner counts only its own stream. `-race -count=3` over
  `proxy/hysteria/...`, `app/proxyman/...` and `transport/internet/hysteria/...`.
- 2026-11-08 T014 (transcribed from xray-config spec026, its T010 entry of the same day): candidate `312ee32e`, Linux SHA-256
  `83074cc33fa3…`; `xrayctl gate plan --node tgz.lite` chose L1.
  - Local: `go test ./...` 111 packages ok, `-race -count=3` on the touched packages; xray-config's full suite, 1759 tests.
  - `singmux-check` quick check (hk.akile.lite server, la.akile.lite client, 60 s chaos, 30 s quiet): PASS, the installed
    services unchanged.
  - `quic-check --baseline-node tgz.lite`: the first run timed out over SSH while distributing to tk.akile.hyper through
    tk.dmit.t1.tiny (nothing installed; the empty directory it left was removed); the rerun passed (253 s). Added latency
    per request at 15% injected loss (baseline / default / tuned / copies): hk→tk 47 / 47 / 34 / 15 ms, tk→la
    78 / 85 / 69 / 28 ms, la→hk 140 / 154 / 132 / 23 ms.
  - `relay-check` PASS (1692 s): 600 streams with 0 failures spread to 4 connections and were back to 1 after 191 s; a
    1.12 s server stop cost the client 1.15 s.
  - hk.akile.lite: `switch upgrade-prepare --core-only`, `check-stage --core-only` and `switch upgrade` (213 ms
    interruption), then `migration hy2-check --duration 600` PASS: mihomo / sing-box TCP 11612 / 11611, connection churn
    2357 / 2357 and UDP 5899 / 5899, none failed; idle recovery, client restart and the negative control passed; Core
    peak 56 MB. `switch revert` (216 ms), configuration `verify` MATCH.
  - Rollout on tgz.lite, the only node that runs relays: `acceptance capture`, `switch upgrade-prepare --core-only`,
    `switch upgrade` (Sidecar 150 ms, Core interruption 208 ms), `acceptance check --baseline --artifact-dir build` PASS
    with no new warning (`hy2_relay`: 7 relay outbounds bound; `observatory_track`: 7 outbounds follow the owner's
    probe); `version` CURRENT `312ee32`. Within a minute or two the relay outbounds of hk.pccw.hkt, la.usvps24.att.1st
    and la.usvps24.att.2nd counted traffic. isz5 and vgz.ecs run no relays and stay on `76e28afd` until their next Core
    upgrade.
