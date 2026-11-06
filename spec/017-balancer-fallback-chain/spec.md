# Feature Specification: Balancer Fallback Chains

- Feature ID: 017-balancer-fallback-chain
- Created: 2026-11-05
- Status: Approved (2026-11-05); implemented 2026-11-06 (T002–T006, T007 tests); build, stability-check gate and
  deployment pending. L6 resolves on the map of the rule's balancer (amended at implementation, 2026-11-06).
- Baseline: Core `ee030ff5` (develop, spec016)
- Origin (user, 2026-11-05): asz.lite's monitoring root `hk-balancer` (Mini and VGD FCC, fallback CTC FCC) lost all
  three FCC paths together on 2026-11-05, 14:12–14:36 +08:00, while both AHK forwarders stayed alive (xray-config
  spec019). The user asked "fallback 感觉要改成 ahk-balancer 能用 balancer 做fallback么",
  then "开 spec017 那逻辑上应该支持多级嵌套的吧？你要考虑清楚", "嵌套是不能成环的，这里要做配置检查，成环会有问题" and
  "spec017 要有一套比较完善的环路检查机制，这个要设计好".
- Consumers: [xray-config spec019](../../../xray-config/spec/019-balancer-fallback-chain/spec.md) (asz.lite
  rollout, xrayctl per-node and fleet loop checks, deployment gate); Sidecar presentation under Control Plane.

## Problem and Goal

A balancer's `fallbackTag` names an outbound. A balancer tag written there is looked up as an outbound; the
dispatcher finds none and closes the connection (`app/dispatcher/default.go`, "non existing outTag"). A fallback
therefore cannot reuse another balancer's candidates, health and strategy. On asz.lite the one independent path
for the monitoring root, the two AHK forwarders, sits in `ahk-balancer` and cannot back up `hk-balancer`.

Nesting brings a new failure: a chain that comes back to a balancer already on it. Such a cycle is invisible while
candidates are alive and is entered exactly during the outage the fallback exists for. The pick then never ends: a
recursive walk overflows the goroutine stack, a fatal error that `recover` cannot stop, and the restarted service
hits it again on the next connection; an iterative walk spins forever on every connection. A leastPing level falls
back before its first observation, so a cycle through one fires on the first connection after every start.

Goal: `fallbackTag` may name a balancer, to any depth, and no configuration Core accepts can make a pick loop.
Loops that one Core cannot see (across nodes) are closed by the consumer's static checks (xray-config spec019).

## Contract

### Chain semantics

- **B1 Resolution.** A `fallbackTag` that names a balancer of the router's current balancer map continues the pick
  in that balancer. Any other value is an outbound tag, as today.
- **B2 Equivalence.** Continuing in balancer B gives exactly the pick that a rule with `balancerTag: B` would get:
  B's strategy, observation, override and its own `fallbackTag`. Nesting is this rule applied level by level. When
  a level uses its fallback is unchanged:

  | Strategy | A level uses its `fallbackTag` when |
  |---|---|
  | roundrobin, random | it has one (so it observes) and no candidate is alive; a candidate missing from the observation counts as alive |
  | leastPing | there is no observation yet or reading it fails (a fresh start), or no observed candidate is alive |
  | leastLoad | its qualified set is empty |
  | weightedLeastPing | spec016 W1 |
  | every strategy | selecting the candidates fails (`SelectOutbounds` error) |

- **B3 Fallback results only.** Only a level's fallback result is resolved again (B1). A candidate or an override
  target is an outbound tag and ends the pick, even when a balancer has the same tag.
- **B4 End of chain.** An outbound tag ends the chain and is used without a health check, as today. A balancer with
  no usable candidate and no `fallbackTag` returns an error and the dispatcher takes the default outbound, exactly
  as when a rule names that balancer directly.
- **B5 Override per level.** An override on a level picks its target, and that level never falls back (as today).
  An inner level's override applies when the chain reaches it. Override targets are never resolved as balancers (B3).
- **B6 One object per tag.** A balancer referenced by rules and as another's fallback is one object: one
  round-robin index, one override.
- **B7 Lua and webhook.** Lua `PickOutbound(tag)` resolves the chain the same way. A rule webhook reports the final
  outbound tag.
- **B8 Selection only.** The configuration field is unchanged (`fallbackTag`, a string). Only new routing decisions
  change; dispatched connections keep running.

### Loop safety

The paths that can loop through a balancer fallback, and what closes each:

| Class | Path | Decidable within one config | Core (this spec) | xray-config spec019 |
|---|---|---|---|---|
| L-A fallback cycle | fallback edges between balancers | exactly: the edges are unconditional | rejected at publish (L1–L5), pick guard (L6) | rejected at render, same rule |
| L-B re-entry | a loopback outbound feeds the connection back into routing (rules or Lua) | no: it depends on rule conditions | each loopback passed at most once per connection (L7) | no fallback chain may reach a loopback |
| L-C transport chain | `dialerProxy` / `proxySettings`, outbound to outbound | exactly | non-goal: it never names a balancer | rejected at render |
| L-D across nodes | an outbound reaches another node, or this one, whose routing comes back | no: it needs every node's config | not observable: the protocols carry no hop count | the fleet state graph must be acyclic |
| L-E control plane | a consumer walking chains over several RPCs mixes two published maps | — | the whole chain comes from one map (Control Plane) | consumers bound what they read |

- **L1 Acyclic fallback graph.** The vertices are the balancers of one balancer map; B → B′ when B's `fallbackTag`
  names B′ in that map (B1), a self-reference included. Core publishes a map only when this graph has no cycle.
  Each balancer has at most one such edge, so a balancer whose chain never ends implies a cycle; "no cycle"
  therefore also bounds every chain by the number of balancers, and there is no separate depth limit.
- **L2 One check point.** `reloadRules` (`app/router/router.go`) is the only writer of the balancer map, for Init and
  for `RoutingService.AddRule` in both modes. It checks L1 on the final map (in append mode the old balancers merged
  with the new ones), after every balancer is built and every rule validated, before `r.balancers.Store`. No other
  operation changes the graph: `RemoveRule` removes rules only; `OverrideBalancerTarget` sets an outbound target
  (B5); `HandlerService` adding or removing outbounds changes no edge, because edges depend on the balancer map
  alone (B1); a Lua script defines no balancer.
- **L3 Rejection.** At Init the error fails `core.New`: `xray run -test` prints `Failed to start` and exits 23, and a
  service does not start. On `AddRule` the RPC returns the error; the published rules and balancers stay as they
  were, and the rejected configuration's webhooks are closed (the existing `published` path).
- **L4 Diagnostic.** `balancer fallback cycle: a -> b -> a`: each cycle starts at its smallest tag, and every cycle
  is listed in tag order, joined by `; `. The wording follows the Sidecar's `remote node via cycle`.
- **L5 Cycles closed by an append.** An append can close a cycle that neither configuration contains alone. An
  existing X whose `fallbackTag` is `N` (an outbound, or nothing, when X was loaded) and an appended balancer `N`
  that falls back to X form X → N → X. L2 checks the merged map, so this is rejected.
- **L6 One map per pick.** A pick resolves every level on one map: the newest published map that contains its first
  balancer. Each publish records itself on every balancer it holds, so a rule read just before a replacing reload
  finishes on the map it was published with (never its old balancer's fallback looked up in the new map, where the
  tag may be gone), and after an append old rules see the merged map. Every published map passed L1. Guard: more
  than len(map)+1 levels can only be a Core defect. The pick then logs the path at error level, counts it (L8), and
  the dispatcher closes the connection; it does not take the default outbound. (Amended 2026-11-06: the approved
  text loaded the router's current map, which mixes two maps when a replace races with the pick; the concurrent
  reload test found it.)
- **L7 Loopback re-entry.** A connection passes each loopback outbound at most once. The loopback outbound records
  its tag in the session context before dispatching. If its tag is already there, it closes the link, logs the
  path at error level and counts it (L8). Any loop through loopbacks repeats one of them, so every such loop ends
  within the number of loopback outbounds, whatever rules, Lua or fallback lead there. This refuses a path that
  passes one loopback twice and would then leave through a different random or round-robin pick. Upstream allows
  that; our configurations have none (one loopback, the local source's `api`, which is in no chain).
- **L8 Guard counters.** Two counters since start: `fallback_chain` (L6, expected to stay zero) and
  `loopback_reentry` (L7). They are read through the Control Plane.
- **L9 Collision.** A `fallbackTag` that names both an outbound and a balancer of one JSON configuration is an
  error in `infra/conf` (`fallbackTag x names both an outbound and a balancer`); the router does not see the
  outbounds at Init. At runtime, an outbound added later with a balancer's tag does not change resolution: the
  balancer wins (B1).

### Proto (additive)

```proto
// app/router/command/command.proto

// spec017: one level of a balancer's fallback chain.
message FallbackHop {
  string tag = 1;
  // `tag` names a balancer of this router (B1); false: an outbound tag, which ends the chain.
  bool balancer = 2;
  // For a balancer hop: its strategy yields no candidate now, so its picks use its own fallback (an override on
  // that hop still wins), as FallbackInfo.active.
  bool active = 3;
  // The hop's strategy reports `active`.
  bool reported = 4;
}

message FallbackInfo {
  string tag = 1;
  bool active = 2;
  bool reported = 3;
  // spec017: `tag` names a balancer (B1).
  bool balancer = 4;
  // spec017: the chain after this balancer, resolved on one balancer map. chain[0] is `tag`; the last hop is an
  // outbound, or a balancer without a fallbackTag. Empty when the balancer has no fallbackTag.
  repeated FallbackHop chain = 5;
}

// spec017: every balancer, from one balancer map.
message ListBalancersRequest {}
message BalancerItem {
  string tag = 1;
  BalancerMsg balancer = 2;
}
message ListBalancersResponse {
  repeated BalancerItem balancers = 1;
}

// spec017 L8: loop guard counters since start.
message GetLoopGuardsRequest {}
message GetLoopGuardsResponse {
  uint64 fallback_chain = 1;
  uint64 loopback_reentry = 2;
}

service RoutingService {
  rpc ListBalancers(ListBalancersRequest) returns (ListBalancersResponse) {}
  rpc GetLoopGuards(GetLoopGuardsRequest) returns (GetLoopGuardsResponse) {}
}
```

`reported` becomes true for every strategy. roundrobin, random, leastPing and leastLoad implement the spec016
fallback state through the same decision code as their pick, without side effects: a report never advances the
round-robin index or rolls a die.

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | B1–B3: a fallback that names a balancer continues there; two- and three-level chains over every strategy end where a direct reference would; a candidate or override tag equal to a balancer tag is not resolved | unit (router) |
| FR-002 | B4: a chain ending at an outbound uses it; a last balancer with no candidate and no fallback takes the default outbound | unit |
| FR-003 | B5–B7: an outer override prevents the fallback and an inner one applies; one round-robin index across direct and fallback use; Lua `PickOutbound`; webhook final tag | unit |
| FR-004 | L1, L3, L4: a self-reference, two- and three-balancer cycles and a balancer leading into a cycle are rejected at Init with the L4 text; `xray run -test` exits 23; an acyclic 64-level chain is accepted and resolves to its end | unit, CLI test |
| FR-005 | L2, L5: `AddRule` replacing with a cycle and appending one that closes a cycle (L5) are rejected, and picks then still use the old map; `RemoveRule`, `OverrideBalancerTarget` and `HandlerService` leave the graph unchanged; `r.balancers.Store` has one caller | started-instance gRPC test, unit |
| FR-006 | L6: picks during concurrent reloads terminate under `-race`; a hand-built cyclic map (white box) closes the connection, increments `fallback_chain` and never reaches the default outbound | unit |
| FR-007 | L7: a rule → loopback → same rule loop ends after one pass and is counted; a path through two different loopbacks still works | started-instance test |
| FR-008 | L9: a collision is rejected by `infra/conf` | unit |
| FR-009 | Control plane: `fallback.balancer`, `fallback.chain`, `reported` for every strategy, `ListBalancers`, `GetLoopGuards` | CP-7 contract test |

## Control Plane

- **Runtime state.** The resolved chain of each balancer, whether each level falls back now, and the two guard
  counters. The chain and the levels are computed at read time from the published map and the observation; the
  counters (two atomics) are the only stored state.
- **Read RPCs (CP-3, CP-4).** `RoutingService`, which owns routing: `GetBalancerInfo` gains `fallback.balancer` and
  `fallback.chain`, `reported` holds for every strategy, `ListBalancers` returns every balancer and
  `GetLoopGuards` the counters. Responses are bounded by the number of balancers and hold tags only (CP-5); a read
  loads the map pointer and takes the strategies' existing observation reads, no lock beyond them.
- **One map (L-E).** `GetBalancerInfo` and `ListBalancers` build every chain on the one map they loaded. A consumer
  walking tag by tag over several calls during a reload could see A → B from one map and B → A from the next, a
  cycle no published map contained; it never needs to.
- **Compatibility (CP-6).** An older Core leaves `balancer` false and `chain` empty: a non-empty `tag` with an empty
  `chain` means an older Core, shown as unavailable. `ListBalancers` and `GetLoopGuards` answer `Unimplemented`.
  A Core without a `routing` section has no router of its own; both answer `unsupported router implementation`.
  An older Core cannot resolve a balancer fallback at all (B1 fails and the connection is closed); xray-config
  spec019 therefore gates deployment on the installed revision.
- **Tests (CP-7).** A started instance through a real gRPC server: a default configuration without fallbacks; a
  three-level chain healthy, with its first level forced down (spec015), and with the first two forced down;
  `GetLoopGuards` zero, then one after the FR-007 loop.
- **Consumers (CP-8).**
  - Sidecar: `/xray-debug` `balancers` (tag, strategy, override, the chain with `active`/`reported` per hop) and
    `loop_guards`, from `ListBalancers` and `GetLoopGuards`; metrics `xray_balancer_fallback_active{balancer}` (the
    balancer's own level falls back) and `xray_routing_loop_guard_total{kind}`. Planned in a Sidecar spec opened
    with T008; until then the Sidecar does not read them (it does not read `GetBalancerInfo` today, spec016 CP-8).
  - Portal: non-goal (2026-11-05), as in spec016: there is no balancer view; Grafana alerts carry the signal.
  - Nodes: `api.services` already lists `RoutingService` on asz.lite and hq. Acceptance and alert rules belong to
    xray-config spec019.

## Non-goals (2026-11-05)

- Override targets that name balancers.
- `dialerProxy` / `proxySettings` cycles in Core (L-C): they involve no balancer, predate this spec and the fleet has
  none; xray-config spec019 rejects them at render.
- Detecting cross-node loops at runtime (L-D).
- A depth limit (L1 bounds every chain).
- Changing when a level falls back (B2). In particular leastPing still falls back before its first observation;
  with nesting a fresh start therefore passes down the chain until observations arrive.

## Notes

- spec016's `GetFallback` already reads the current map. The chain extends it level by level on that same map.
- An older Core loads the same JSON without error and drops what should have fallen back to a balancer; the gate
  in xray-config spec019 is what prevents that deployment.
