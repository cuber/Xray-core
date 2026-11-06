# Tasks: Balancer Fallback Chains

- [x] T001 spec, plan and tasks; index row in `spec/README.md`.
- [x] T002 Chain resolution B1–B8: `Balancer.pick`, `resolveChain`, `PickRoute`, Lua; units FR-001–FR-003.
- [x] T003 Loop checks L1–L6: `checkFallbackCycles` in `reloadRules`, pick guard, `ErrRouteDropped` in the
  dispatcher; units FR-004–FR-006, CLI exit 23.
- [x] T004 Loopback re-entry L7 and the guard counters L8; started-instance test FR-007.
- [x] T005 `infra/conf` collision L9; unit FR-008.
- [x] T006 Control plane: `FallbackHop`, `FallbackInfo.balancer/chain`, `reported` for every strategy,
  `ListBalancers`, `GetLoopGuards`; CP-7 contract FR-009.
- [ ] T007 Full `go test ./... -count=1`, race and format checks; `xrayctl build core` from the committed tree;
  stability-check gate (dispatcher row). Tests done (evidence); build and gate open.
- [ ] T008 Consumers: open the Sidecar spec for `/xray-debug` `balancers`/`loop_guards` and the metrics; xray-config
  spec019 for the xrayctl checks, gate, acceptance and asz.lite.

T007 results go to xray-config spec019 first; they come here with the next Core code change (stability-check: no
doc-only Core commit between the gate and the deployment).

## Evidence

2026-11-06, on `20f3023e` (spec only since `ee030ff5`):

- T002 (`app/router/fallback_chain.go`, `balancing.go`, `router.go`, `lua.go`): `TestFallbackChainEveryStrategy` (roundrobin,
  random, leastPing, leastLoad, weightedLeastPing at two and three levels, each step equal to the direct pick of the
  level reached), `…ResolvesFallbackResultsOnly` (B3), `…End` (B4), `…OverridesAndSharedIndex` (B5, B6: direct, chain,
  direct picks p, q, p), `…WebhookReportsTheFinalTag`, `TestRouterScriptPicksThroughTheChain` (B7).
- T003: `TestFallbackCycleCheck` (self, two, three, a tail into a cycle, two cycles in one message, an acyclic
  64-level chain that resolves to its end), `TestFallbackCycleOnReload` (`core.New` fails; a replace and an append
  closing `m -> n -> m` are rejected and the old map keeps picking; `RemoveRule` and an override leave the chain),
  `TestFallbackChainGuard` (a hand-built cycle is dropped and counted, also from a balancer of an earlier map),
  `TestBalancerMapHasOneWriter`, `TestFallbackChainConcurrentReloads` under `-race`, `TestFallbackChainStaysOnItsMap`
  (L6 as amended; the first version of the concurrent test returned `l2`, a fallback of the old map looked up in the
  new one), `main/run_fallback_cycle_test.go` (`run -test` exits 23 and prints `balancer fallback cycle: a -> b -> a`).
- T004: `proxy/loopback/loopback_test.go` (a started instance: `in -> lo-a -> lo-a` closes and counts one re-entry;
  `in -> lo-b -> lo-c -> direct` echoes).
- T005: `infra/conf/fallback_chain_test.go`.
- T006: `app/observatory/command/chain_contract_test.go` (three levels shaped like asz.lite's root: healthy, c1, then
  c1 and c2, then all forced down to `block`, released; `ListBalancers` equal to `GetBalancerInfo` at every step;
  `GetLoopGuards` 0, then 1 after a loopback loop; a routing section without balancers lists none). spec016's
  contract and unit tests now expect `reported` for roundrobin and the one-hop chain.
- `go test ./... -count=1 -timeout 1h` with `resources/geoip.dat` and `geosite.dat`: exit 0, 107 packages ok, no
  failure. `go vet` and `gofmt` clean on the touched packages; `-race` on `app/router/...`, `app/dispatcher`, `proxy/loopback`,
  `app/observatory/command`, `infra/conf` passes.
