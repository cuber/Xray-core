# Tasks: QUIC Loss Recovery And Redundancy

- [x] T001 spec, plan and tasks; index row in `spec/README.md`.
- [x] T002 Spec022 done (fork, line `fork/2026-11-07/184d081eef3e` at the base, `replace`); the fork's spec-kit set up; the upstream suite passes on the
  unmodified branch.
- [x] T003 Series 1–2: config fields, validation and R1; FR-001–FR-003 in the fork.
- [x] T004 Series 3: copies (R2); FR-004 in the fork.
- [x] T005 Series 4: loss counting moved out of Cubic, new counters; `ConnectionStats` additions; FR-009 in the fork.
- [x] T006 Core: `replace` to the series head, proto fields 17–25, JSON parsing and ranges, `quicrecovery.Apply` in the
  Hy2 dialer and listener, masque and splithttp HTTP/3; FR-005.
- [x] T007 Loss relay integration test; FR-006.
- [x] T008 Hy2 per-handler stats set, `HandlerService.GetQuicStats`; the CP-7 contract; FR-007.
- [x] T009 `xray api quicstats`; FR-008.
- [x] T011 netem tests (FR-010): `testing/netem/run.sh`, each test passing three times in a row.
- [ ] T010 Full suite, race, format (done); `xrayctl build core` (`go version -m` names the replacement); `xrayctl gate plan`
  and its gate with default and spec024 parameters. The forked dependency is recorded by spec001 FR-012 (spec022);
  `inventory.md` is the 2026-09-27 audit and stays as it is.

## Evidence

- 2026-11-07 T002–T005: in the fork, [spec001 tasks](../../../quic-go/spec/001-loss-recovery/tasks.md). The spec-kit
  (`533e1b9f`) and three commits, `90940e89` (config and recovery), `1db03ecc` (copies), `afe4141e` (counters). The
  fork's suite with go1.27.0 passes before and after each commit, with `-race` on the touched packages. On the
  simulated 10 ms link a lost request takes 47 ms by default and 24 ms with a 2 ms ACK delay on both ends; copies
  carry requests whose originals are all lost at no extra round trip; dropping every 10th datagram counts 24 losses
  under a custom congestion controller, where upstream counted 0.
- 2026-11-07 T006: `TestQuicParamsLossRecovery*` (unset fields stay zero, every field maps, the ranges and the delay
  rule, a negative value fails to parse); `TestQuicParamsMaxAckDelayNeedsExplicitParrotOff` (a `hysteria` outbound, an
  `xhttp` outbound over HTTP/3 and an `xhttp` `downloadSettings` over HTTP/3 with `maxAckDelayMs` fail without
  `disableChromeParrot: true` and build with it; other fields, XHTTP over HTTP/2 or without TLS, other transports and
  stream settings built for an inbound are not affected); `TestApply`, `TestEffectiveDefaults`.
- 2026-11-07 T007, `TestHy2LossRecovery*` on loopback:
  - a request whose first datagram is lost takes 29 ms by default and 6 ms with `maxAckDelayMs` 2 on both ends, and
    each counts a PTO;
  - with `duplicateMaxSize` 1500 and every original dropped, five requests complete within 10 ms each, with copies
    sent, nothing lost and no PTO;
  - in a 300 ms blackout a request takes 425 ms with the unlimited backoff and 311 ms with `maxAckDelayMs` 2 and
    `ptoBackoffLimit` 1.
- 2026-11-07 T008, `TestQuicStatsContract` on a started instance with a real gRPC server:
  - the three Hy2 handlers only, with configured and default effective values;
  - `hy2-tuned` sent 75 copies and `hy2-in` dropped 75 duplicates; `hy2-plain` sent none;
  - the inbound's rows carry both users by email and no secret; one live connection per outbound;
  - a tag selects one handler, rows only on request, `NotFound` for an unknown or non-QUIC tag;
  - restarting the inbound replaces the outbound's connection: 2 total, 1 active, every total monotonic; the restarted
    inbound starts its own totals.
  - Unit: `TestQuicStatsSet*` (nil set, replacement, 256 rows and `truncated`, oldest first).
- 2026-11-07 T009: `TestQuicStatsText`, `TestQuicStatsJSON`, `TestQuicStatsOlderCore` against a fake handler server and
  one that answers `Unimplemented`.
- 2026-11-07 T011, `testing/netem/run.sh -test.count=3` (OrbStack, linux/amd64 under Rosetta, lo only; the median round
  trip is about 10 ms, the emulated 7.5 ms plus the container's overhead). Every level of all three runs passed; the
  slowest tenth and the share of requests slowed by a loss, by default / with the recovery parameters / with copies:

  | Random loss | Slowest tenth (ms) | Slowed |
  |---|---|---|
  | 5% | 42–45 / 25–30 / 12–13 | 8.5–9% / 8–10.5% / 0.5% |
  | 10% | 54–62 / 32–39 / 15–17 | 15–16.5% / 17–18.5% / 2–3% |
  | 15% | 92–100 / 41–45 / 19–22 | 30–33% / 28–30.5% / 4.5–6% |
  | 20% | 108–140 / 60–66 / 23–27 | 33–35.5% / 35–40% / 6–7.5% |

  The recovery parameters do not avoid losses, they halve what one costs; copies make a loss rare. A 300 ms blackout
  ended 245–261 ms before the request completed by default, 12–59 ms with `ptoBackoffLimit` 2.
  The netem test found that a packet retired by a PTO probe was never counted lost; the fork's `0162af63` counts it
  (its spec001 T007), and Core pins it.
- 2026-11-07 T010 (suites): `go test ./... -count=1` 110 packages ok; `-race` on `transport/internet`, `quicrecovery`,
  `hysteria`, `splithttp`, `masque/...`, `app/proxyman/...`, `infra/conf` and `main/commands/all/api` ok;
  `.github/check-gofmt.sh` ok. An earlier run's `proxy/anytls` and `masque/connectip` failures did not recur (each
  passed 10 times alone, and the whole suite after).
