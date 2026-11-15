# Tasks: thresholdLeastPing

Status: approved design; implementation authorized by the operator on 2026-11-15, stage by stage, with a
review after each stage. Build, push and rollout remain separate. Every design decision is confirmed (spec.md
Decisions D1–D16).

- [x] T000 Inspect the baseline: leastPing, strategy/fallback split, dispatcher default, observatory update
  paths, relay refusal, outbound manager checks, statusReader, balancerMsg, M6 check (spec.md Current Evidence).
- [x] T001 Record the operator's decisions of 2026-11-14 and 2026-11-15 (spec.md Decisions).
- [x] T001a Promote draft005 to spec027 (`291fde47`).
- [x] T001b Choose the evaluation and read model: observation-driven passes on a new strategy type
  (spec rewrite 2026-11-15; plan.md 3–4).
- [x] T001c Restore requirements and cases lost in the rewrite against `291fde47` (2026-11-15).
- [ ] T002 Configuration, typed settings, leastPing guard (D11), observed-outbound check (D15).
  Cases C01–C12, O01–O08.
- [ ] T003 Pure decision with hysteresis and D13 comparison precision. Cases Q01–Q26, Y01–Y11 at the
  function level.
- [ ] T004 Notifications (observatories, Hy2 relay D12, outbound manager); router passes, coalescing,
  pre-publication pass on reload, lifecycle; picks and chains read one pass; drop counter (D14).
  Cases E01–E21, Y06–Y07 and Y11 at router level.
- [ ] T005 Nesting: reported status per mode, child results, member means, legacy parents and children.
  Cases N09–N23, H01–H08.
- [ ] T006 Control plane: protobuf fields, `BalancerThreshold` report, `balancerMsg`, `ListBalancers`,
  `xray api bi`. Cases R01–R16.
- [ ] T007 Native data-plane tests N01–N08 and E17; full regressions, race repetitions, vet, gofmt.
- [ ] T008 Sidecar and Portal consumers (their repositories; separate authorization).
- [ ] T009 Commit, dual-platform build, stability gate (separate authorization).
- [ ] T010 Rollout X01–X05, thresholds and paired restore plan (separate authorization; xray-config TODO-053).
