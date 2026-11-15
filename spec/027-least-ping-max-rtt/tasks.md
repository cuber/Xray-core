# Planned Tasks

Status: Core spec027 approved; promoted from draft005 on 2026-11-14. No runtime implementation tasks are
authorized for execution or complete.

- [x] T000 Inspect current leastPing, the other maxRTT strategies, nested status, fallback and RPC read paths;
  record the design and verification boundaries. Evidence: spec.md Current Evidence; source-only inspection.
- [x] T001 Record the operator's schema, optional fallback, explicit block/drop, immediate-start probe,
  roundrobin/random mean, existing override and runtime-policy disclosure decisions. Evidence: spec.md
  Clarification Complete (2026-11-14); documentation only, not implementation/test evidence.
- [x] T001a Promote Core draft005 to spec027 following the operator's request on 2026-11-14; move all five
  documents, reserve draft005, advance the independent spec counter and update references. Documentation only.
- [x] T001c Amend the contract on the operator's 2026-11-15 request: hysteresis through `recoverRTT` (LP-7)
  and a load-time observed-outbound check (LP-8); cases C10--C11, Y01--Y10, O01--O07, R15. Documentation only.
- [ ] T001b During authorized implementation, choose the narrow snapshot interface. No further
  product-semantic approval or rollout implied.
- [ ] T002 Add typed leastPing settings (maxRTT, recoverRTT), native JSON/proto validation and parser tests
  C01--C11; extend `checkMemberObservation` for enabled roots with tests O01--O07.
  Files: infra/conf/router*, app/router/config.proto/config.go and generated protobuf. Depends T001b.
- [ ] T003 Add preferred/fallback/degraded/drop leastPing decisions and preserve defaults/ties;
  tests Q01--Q21/H01--H12, and LP-7 latches committed only by dispatch picks with tests Y01--Y10.
  Fallback is one opaque bubbled result, never a flattened child pool.
  Files: app/router/strategy_leastping*, dedicated tests. Depends T002. No global OB mutation.
- [ ] T004 Align opaque nested results, actual fallback flags, shares, targets and snapshot explanation;
  tests N01--N09/N11--N20/R01--R14, including mean coverage without changing child scheduling.
  Files: member_status.go, relevant features/routing and command projections/tests. Depends T003.
- [ ] T005 Add additive RPC policy/selection messages and API text output, with real started-instance gRPC
  and old-client compatibility tests. Depends T004; regenerate proto with approved toolchain.
- [ ] T006 Add native marker/fallback/flow-survival cases including N10 and a real burst-observer transition;
  bounded waits, no timing-equality oracle. Depends T003--T005.
- [ ] T007 Review/run baseline strategy, track, force-down, member, graph, user-route and protocol regressions;
  race and repeat reads/reloads; formatting/vet/full packages. Depends T006. Preserve failures/skips.
- [ ] T008 Plan/implement separately owned Sidecar and Config consumer work and rollout capability gates;
  actual RPC passthrough, Portal browser checks, old-Core unavailable, exact policy audit and bounded
  first-probe/business readiness (not admin-only success). Depends T005;
  requires implementation authorization in the relevant repositories; no copied authoritative Core spec.
- [ ] T009 Record build/commit identity and execute normal dual-platform build and stability gates when
  separately authorized. Never build from a dirty Core tree or force-push an append-only fork line.
- [ ] T010 Request/record separate rollout scope, thresholds and paired restore plan before any production
  activation. A compile, config test, TODO or spec approval alone cannot authorize a deployment.
