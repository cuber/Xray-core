# Tasks: thresholdLeastPing

Status: Core side implemented 2026-11-15 (T002-T007) and committed together on the operator's request. Build, push and rollout remain separate. Every design decision is confirmed (spec.md
Decisions D1–D16).

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
- [ ] T009 Commit, dual-platform build, stability gate (separate authorization).
- [ ] T010 Rollout X01–X05, thresholds and paired restore plan (separate authorization; xray-config TODO-053).
