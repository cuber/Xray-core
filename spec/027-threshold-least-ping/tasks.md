# Tasks: thresholdLeastPing

Status: Core side implemented 2026-11-15 (T002-T007) and committed together on the operator's request; gated as
`6a7a8c4e` (T009); TL-10 cold start 2026-11-17 (T011). Push and further rollout remain separate. Every design
decision is confirmed (spec.md Decisions D1–D17).

- [x] T000 Inspect the baseline: leastPing, strategy/fallback split, dispatcher default, observatory update
  paths, relay refusal, outbound manager checks, statusReader, balancerMsg, M6 check (spec.md Current Evidence).
- [x] T001 Record the operator's decisions of 2026-11-14 and 2026-11-15 (spec.md Decisions).
- [x] T001a Promote draft005 to spec027 (`291fde47`).
- [x] T001b Choose the evaluation and read model: observation-driven passes on a new strategy type
  (spec rewrite 2026-11-15; plan.md 3–4).
- [x] T001c Restore requirements and cases lost in the rewrite against `291fde47` (2026-11-15).
- [x] T002 Configuration, typed settings, leastPing guard (D11), observed-outbound check (D15).
  Cases C01–C12, O01–O08. Evidence (2026-11-15, uncommitted on `ac0201df`, go1.27.0 darwin/arm64):
  `infra/conf/router_strategy_threshold_test.go` (C01–C05, C11), `app/router/strategy_thresholdleastping_test.go`
  (C04, C08, C12 limit, O06 on ReloadRules, interim drop and override), `main/run_threshold_least_ping_test.go`
  (15 `run -test` cases: C03, C06, C07, C11, O01–O07), `app/router/command/threshold_config_contract_test.go`
  (C09, O06 and O08 over real RPCs). C10 checked by hand: approved `10da009` exits 23 on JSON
  (`unknown balancing strategy: thresholdleastping`) and protobuf (`unrecognized balancer type`); the
  candidate loads both. Deferred to T004: C12's latch reset, O08's "fallback unknown from the next pass".
  Until a pass exists a pick drops (`routing.ErrBalancerUnavailable`, an `ErrRouteDropped`); an override still wins.
- [x] T003 Pure decision with hysteresis and D13 comparison precision. Cases Q01-Q24 and Y01-Y05, Y08-Y10 at
  the function level: `app/router/threshold_decide_test.go`.
- [x] T004 Notifications (both observatories incl. a scheduled round's cleanup, Hy2 relay D12, outbound
  manager), router passes, coalescing, pre-publication pass on reload, lifecycle; picks and chains read one
  pass; drop counter and info-level log (D14). Cases E01-E21, Q25, Q26, Y06, Y07, Y11, C12's latch reset,
  O08's absent fallback: `app/router/threshold_pass_test.go`, `app/router/threshold_bench_test.go`
  (E13: p50 244us, p99 591us per pass over 100 balancers and 300 outbounds; 277us/op, 2648 allocs/op),
  `app/observatory/burst/notify_test.go` (E02, E03, E13 RTT unaffected by a 100ms subscriber, E14, E18,
  E19), `app/observatory/notify_test.go` (E04), `app/proxyman/outbound/notify_test.go` (E20),
  `common/signal/broadcast_test.go`; E17 in `app/router/command/threshold_rpc_contract_test.go`.
- [x] T005 Nesting: reported status per mode, child results, member means, legacy parents and children.
  Cases N09-N23, H01-H08 (`app/router/threshold_pass_test.go` TestThresholdNesting and the override cases;
  H03/H04 through the observatory's own track and force-down tests, unchanged; H06 by the unchanged
  leastPing suites).
- [x] T006 Control plane: protobuf fields (BalancerMsg 11/12, BalancerMember 6, new messages appended),
  `ThresholdReader`, `balancerMsg`, `ListBalancers` pinned to one pass, `xray api bi`. Cases R01-R16:
  `app/router/command/threshold_rpc_contract_test.go` (real Core, real burst observatory, real gRPC),
  `main/commands/all/api/balancer_info_test.go` (R12), R16 in the data-plane test.
- [x] T007 Native data plane N01-N08 (a TCP keep-alive flow and a UDP association survive a decision
  change), R16 drop count and log level: `app/router/command/threshold_dataplane_test.go`. Full suite,
  `go vet ./...` and the repository format check pass.
- Local suites of the stability-check skill (step 3), each run alone on the verification Mac, 2026-11-15,
  go1.27.0 darwin/arm64: full suite `go test ./... -count=1 -timeout 1h` 111 packages ok, 0 failures;
  `-race -count=3` on common/signal, app/router/..., app/observatory/..., app/proxyman/outbound,
  app/dispatcher, infra/conf, main, main/commands/all/api, transport/internet/hysteria and
  proxy/shadowsocks_2022 all ok, no data race; strict sing-mux with mihomo 1.19.33, the cached mihomo and
  Homebrew mihomo and sing-box 1.14.3 and 1.14.2 (`SINGMUX_STRICT=1`, absolute client paths) 73 PASS,
  0 FAIL, 0 SKIP; `testing/netem/run.sh -test.count=3` 6 tests x 3 = 18 PASS. E13 alone with
  `XRAY_SPEC027_TIMING=1`: p99 504-547us. Earlier runs made while other suites ran beside them are not
  evidence: one failed the E13 wall-clock bound at 1.09ms and one routing command run failed uncaptured,
  which is why the skill requires running suites alone and why E13's bound is now opt-in.
- [x] T007b Real-network functional check A01–A11 with the configuration repository's `threshold-check`
  (operator, 2026-11-15: before the stability gate; repeated to check that the results agree). Candidate
  `db65843` (Linux sha256 `05bab80305c0…`). Five runs on three test hosts in different regions, all PASS; the
  last four with A01's fastest-candidate check, per-sample TL-3/TL-4 rule checks (11011 responses, no
  violation) and hb's release asserted. Across those four: probe RTTs stable within 1 ms (SS-2022 about twice
  Hy2 on the same path, a new TCP connection per probe); restart to first flow 0.33–0.35 s with 2 attempts
  dropped; fallback 8–13 s after a path's probes turned late (one window of 2 s × 5); endpoint outage left in
  5–9 s and returned in 2–5 s; drops exact, logged at info only; nested and mean children as specified;
  without a band the hysteresis balancer changed exit 22–28 times in 300 s, with one 1–2 (latch, release);
  2720 churned flows, none failed; the decider's goroutines, FDs and RSS flat. The first run's A01 showed the
  slower SS-2022 candidate chosen: the polls of the later runs show why, Hy2's first sample carries the QUIC
  handshake (about 150 ms), so for about 2 s after a start its window average is the higher one; the choice
  then moves to Hy2. A11 (added afterwards) in three more runs, all PASS: a TCP flow and a UDP association
  opened while `t` used its fallback kept echoing on the fallback after the candidate region returned, once
  from late probes and once from a stopped endpoint (9–15 TCP echoes, none failed; every UDP reply from the
  fallback, none lost), while new flows went back to the candidate region. Records stay in xray-config's
  private `.cache/threshold-check-*`.
- [ ] T008 Sidecar and Portal consumers (their repositories; separate authorization).
- [x] T009 Commit, dual-platform build, stability gate. Candidate `6a7a8c4e` (Linux sha256 `a0b3d849910c…`);
  `xrayctl gate plan --base 10da0092` chose L2 with the Hy2 and relay suites. Passed 2026-11-16 on the user's
  decision. A first run on 2026-11-15 was void, because another session switched the test hosts during it.
  - `singmux-check` A/B, 600 s chaos and 120 s quiet, against `10da009` (its cases measured within 7 days):
    PASS with each of hk.akile.lite and la.akile.lite as server, no panic, everything drained.
    Candidate peaks: 1240 / 1155 goroutines, 243 / 230 FDs, 72.9 / 80.0 MiB.
    Baseline peaks: 1208 / 1174, 237 / 233, 72.4 / 77.5 MiB.
  - `quic-check` PASS (2026-11-15). Added latency per request at 15% injected loss
    (baseline / default / tuned / copies): hk→tk 38 / 39 / 32 / 12 ms, tk→la 109 / 97 / 76 / 38 ms,
    la→hk 163 / 115 / 136 / 48 ms.
  - `migration hy2-check --duration 600` on hk.akile.lite after `switch upgrade`:
    mihomo / sing-box TCP 11641 / 11640, churn 2361 / 2361 and UDP 5912 / 5913, none failed;
    idle recovery, client restart and the negative control passed; Core peak 53 MiB, no restart.
    `switch revert` afterwards.
  - `relay-check`: one failure. la→hk scaling sampled one of its 4 connections as suspect. This is a false
    suspect of spec024 R5, present since spec024 and the same in `10da009`; fixed as spec024 T015, whose
    candidate takes the Hy2 suites again (spec024 T016).
- [ ] T010 Rollout X01–X05, thresholds and paired restore plan (separate authorization; xray-config TODO-053).
  2026-11-16, authorized for the LA Verizon aggregate only: `6a7a8c4e` first ran with unchanged configurations on
  the four LA DMIT clients (X01), then the aggregate went live with `maxRTT` 100ms and `recoverRTT` 30ms, read
  back from the RPC (X02, X03), with deep acceptance on each client. Records in xray-config TODO-053; the rest
  of TODO-053 is not authorized.
- [x] T011 TL-10 cold start (D17, operator 2026-11-17): `app/observatory/burst/healthping.go` `coldStart`,
  the scheduled rounds' wait, K01–K05 in `coldstart_test.go` and the rewritten upstream round contract.
  Evidence (2026-11-17, go1.27.0 darwin/arm64, each suite run alone): the baseline is the A06 polls of the seven
  2026-11-15 runs (spec.md TL-10). `TestUpstreamSchedulerSupersedesInflightRound` assumed that the first round
  probes while the initial check is in flight; it now holds the cold start's first probe and requires that rounds
  neither cancel it nor probe before it ends, and that a round's probe is still superseded by the next tick.
  Full suite `go test ./... -count=1 -timeout 1h`: 111 packages ok. `-race -count=3` on app/observatory/...,
  app/router/..., main and main/commands/all/api: all ok, no data race; the cold-start and contract tests of
  app/observatory/burst also pass `-race -count=10`. `go vet` and gofmt clean.
- [ ] T012 Commit, dual-platform build and the gate `xrayctl gate plan` chooses for the T011 candidate, plus a
  `threshold-check` run whose restart shows the first decision (results in xray-config TODO-053 until the next
  Core code change); rollout separately authorized.
