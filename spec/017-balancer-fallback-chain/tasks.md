# Tasks: Balancer Fallback Chains

- [x] T001 spec, plan and tasks; index row in `spec/README.md`.
- [ ] T002 Chain resolution B1–B8: `Balancer.pick`, `resolveChain`, `PickRoute`, Lua; units FR-001–FR-003.
- [ ] T003 Loop checks L1–L6: `checkFallbackCycles` in `reloadRules`, pick guard, `ErrRouteDropped` in the
  dispatcher; units FR-004–FR-006, CLI exit 23.
- [ ] T004 Loopback re-entry L7 and the guard counters L8; started-instance test FR-007.
- [ ] T005 `infra/conf` collision L9; unit FR-008.
- [ ] T006 Control plane: `FallbackHop`, `FallbackInfo.balancer/chain`, `reported` for every strategy,
  `ListBalancers`, `GetLoopGuards`; CP-7 contract FR-009.
- [ ] T007 Full `go test ./... -count=1`, race and format checks; `xrayctl build core` from the committed tree;
  stability-check gate (dispatcher row).
- [ ] T008 Consumers: open the Sidecar spec for `/xray-debug` `balancers`/`loop_guards` and the metrics; xray-config
  spec019 for the xrayctl checks, gate, acceptance and asz.lite.

T007 results go to xray-config spec019 first; they come here with the next Core code change (stability-check: no
doc-only Core commit between the gate and the deployment).
