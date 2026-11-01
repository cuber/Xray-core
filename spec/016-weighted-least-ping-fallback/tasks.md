# Tasks: weightedLeastPing Fallback

- [x] T001 spec, plan and tasks; spec001 FR-006 and spec005 point here.
- [x] T002 strategy W1–W4 with `FallbackTag`, set in `BalancingRule.Build`; units FR-001–FR-003.
- [x] T003 `GetBalancerInfo` `fallback` (proto additive, `Router.GetFallback`); CP-7 contract FR-004, FR-005.
- [x] T004 full `go test ./... -count=1` (2026-11-02: 105 packages ok).
- [ ] T005 `xrayctl build core` from the committed tree; stability-check gate on the Akile hosts (the change
  reaches routing selection only).
- [ ] T006 hq `switch upgrade-prepare --core-only` and `switch upgrade`; `acceptance check`.

T005 and T006 results go to the xray-config hq README first; they come here with the next Core code change
(stability-check: no doc-only Core commit between the gate and the deployment).
