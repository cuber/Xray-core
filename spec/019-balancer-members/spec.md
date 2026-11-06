# Feature Specification: Balancers As Members

- Feature ID: 019-balancer-members
- Created: 2026-11-06
- Status: Approved (2026-11-06; user after review: "可以，开始吧"); implemented 2026-11-06 (T002–T006);
  gate T007 open.
- Baseline: Core `1d3186e0` (develop, spec018)
- Origin (user, 2026-11-06):
  - "现在 hq 和 xray local 有些是有两种协议的，给节点粒度加一个 balancer 用 leastPing 然后我们不是支持嵌套 balancer
    了么，给总的 weighted 里的节点换成 balancer";
  - "存活状态应该是一个冒泡状态吧，balancer 等同于一个特殊的 outbonds ... 叠加 fallback 一起计算可用性拓扑，fallback
    也要求带上 ob ... 发现嵌套就要求 fallback 必须带 ob";
  - "balancer 的控制面 list 扁平化展示就好了，换路检查要做好";
  - "权重不用穿透，单层计算即可，应该是一层一层计算得到的", "每层只看自己的即可，不需要拍平，你把 balancer 当作一种
    节点来看就好了", "思路和可用性一样的", "权重应该是一层层向下，逐层计算的", "可用性是冒泡从低下冒泡上来的".
- Consumers: [xray-config spec021](../../../xray-config/spec/021-node-balancers/spec.md) (hq and the local client:
  one leastPing balancer per relay node over its protocols, members of the route's weightedLeastPing).

## Problem and Goal

A route on hq and on the local client balances over relay candidates, and a relay with two protocols (Hy2 and
AnyTLS) is two candidates: it weighs twice, and the two protocols compete with every other relay instead of with
each other. The wanted shape is a balancer per relay that picks the faster of its protocols, as one member of the
route's balancer. Spec017 lets a balancer continue in another only through its `fallbackTag`; a candidate is an
outbound (spec017 B3).

Goal: a balancer may have balancers as members. A member balancer is treated as a node: the level above sees it
as one candidate and knows nothing of what is below it. Two directions, one level at a time: a pick goes down,
each level choosing among its own candidates with its own weights; availability and delay come up, each level's
computed from the one below.

## Contract

### Members

- **M1 Declaration.** A balancer gains `balancers`, a list of balancer tags (exact, never prefixes, so a route
  balancer cannot match itself or a sibling by a shared prefix). `selector` keeps matching outbounds only.
- **M2 Candidates.** A level's candidates are the outbounds its `selector` matches (in the outbound manager's
  order, by tag, as today), then its `balancers`, in their order. A tag in `balancers` that names no balancer of the map is an error at publish (`balancer a: member b is
  no balancer`), as is a tag listed twice. A member balancer has a `fallbackTag` (an outbound, a balancer or a
  blackhole), so a pick inside a member never ends at the default outbound (spec017 B4 holds only at the level a
  rule names): `balancer b is a member and has no fallbackTag`.
- **M3 Picks go down, one level at a time.** A level's strategy and its settings (weightedLeastPing weights,
  leastLoad costs and baselines) choose among its own candidates, a member balancer by its tag. A pick that lands on
  a member balancer continues there, where the member's own strategy and weights choose, and so on down, as a
  fallback that names a balancer continues (spec017 B1, B2). No level's weights are combined with another's; the
  share a leaf gets follows from the levels it was picked through and is never declared. An override target
  remains an outbound (spec017 B5).

### Availability and delay

- **M4 Status bubbles up.** Every candidate has an availability and a delay. They start at the leaves: an outbound's
  come from the observatory as today, forced-down probes (spec015) and `track` followers (spec014) included. A
  balancer B's are computed from its own level, the statuses of its candidates already known:
  - with an override: the override target's status;
  - otherwise, when one of its candidates is available: available, with the smallest delay among the available
    candidates;
  - otherwise, when one of its candidates has no observation yet (a fresh start): unknown, which the level above
    reads as an unobserved candidate, available without a delay, as roundrobin reads one today;
  - otherwise its fallback's: an observed outbound's, a balancer's (computed the same way), a blackhole's
    (unavailable).
- **M5 What a level sees.** A level with member balancers reads the observation with one entry per member balancer
  added, carrying M4's status under the member's tag. Its strategy is unchanged and treats the entry as an
  outbound's. The statuses are computed from the map the level was published with (spec017 L6) at each read.
- **M6 Observation is required.** A balancer with members, and every balancer reachable from it through members or
  fallbacks, needs every outbound candidate and outbound fallback observed (an observatory subject or a `track`
  follower); a blackhole fallback needs none. With M2's fallback this is the user's rule, "发现嵌套就要求 fallback
  必须带 ob": every leaf of the availability topology has a known status. Otherwise Core refuses the configuration
  at start (spec018 S3.1) and refuses `AddRule` and `AddOutbound` that would create such a case: `balancer b:
  outbound x is not observed (balancer members need every outbound observed)`.

### Loops

- **M7 One graph.** The vertices are the balancers of a map; an edge leads from a balancer to each member and to
  its fallback when that names a balancer. The graph is acyclic, a self-reference included, checked at the
  spec017 L2 point (every publish) and at spec018's start check. A cycle of fallback edges only keeps spec017 L4's
  text (`balancer fallback cycle: a -> b -> a`); a cycle with a member edge names each edge: `balancer cycle: a
  -[member]-> b -[fallback]-> a`. One cycle is printed per strongly connected set of balancers, from its smallest
  tag, the sets in that order, joined by `; ` (for fallback edges alone, each set is exactly one cycle, as in L4).
- **M8 Bounded picks and status.** A pick descends at most len(map)+1 levels (spec017 L6's guard, now over both
  edge kinds), and the status computation of M4 visits each balancer at most once per read.
- **M9 Loopbacks.** spec018 S2 walks every member of a balancer as it walks the candidates: a member's candidates,
  override and fallback chain are reachable from the rule that names the outer balancer.

### Control plane

- **M10 Flat list.** `ListBalancers` keeps one item per balancer. Each `BalancerMsg` gains its members (tag, and
  whether it names a balancer) and its own M4 status (available, delay, reported). A consumer joins the items by
  tag; no item nests another.

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | M1–M3: a member balancer is a candidate after the selector's outbounds; a pick of it continues in it; weights of the outer level apply to the member by tag and never inside it; JSON `balancers` builds; an unknown or repeated member, and a member without a fallbackTag, are refused | unit (router, infra/conf) |
| FR-002 | M4, M5: a member's status follows its level, its override and its fallback (observed outbound, balancer, blackhole); a member with unobserved candidates is unknown; the outer weightedLeastPing, leastPing, roundrobin and random read it as an outbound's; a forced-down probe and a track follower bubble up; at a fresh start a leastPing member uses its fallback and never the default outbound | unit |
| FR-003 | M6: an unobserved outbound candidate or fallback under a balancer with members is refused at start and by `AddRule`/`AddOutbound`; a blackhole fallback is not | unit, CLI (`run -test` exit 23) |
| FR-004 | M7, M8: member and fallback cycles, mixed, a self-member, a member leading into a cycle are refused with the M7 text at publish and at start; a 32-level DAG is accepted | unit, CLI |
| FR-005 | M9: a loopback reachable only through a member balancer is found by spec018 S2 | unit |
| FR-006 | M10: `ListBalancers` and `GetBalancerInfo` report members and statuses through a real gRPC server: healthy, a member's best protocol forced down, a whole member down, all down to the outer fallback | started-instance CP-7 test |
| FR-007 | The shape of xray-config spec021 (a weightedLeastPing route over per-relay leastPing members and outbounds) picks by node weight and the faster protocol inside a node, under concurrent reloads with `-race` | started-instance test |

## Control Plane

- **Runtime state (CP-1, CP-2).** Each balancer's members and computed status, read-only through `RoutingService`
  (`ListBalancers`, `GetBalancerInfo`), computed at read time from the published map and the observation.
- **Compatibility (CP-6).** An older Core leaves the new fields empty. It also ignores `balancers` in JSON, so it
  would load a route without its members; xray-config spec021 gates deployment on this spec's commit, as spec019
  of xray-config does for spec017.
- **Consumers (CP-8).** Sidecar `/xray-debug` and Portal: non-goal (2026-11-06), as for spec017 and spec018; the
  Portal's Balancers tab keeps showing the configuration.

## Non-goals (2026-11-06)

- Weights or settings that pass through a member (user: "权重不用穿透，单层计算即可").
- Members by prefix, and override targets that name balancers.
- A tree view in any consumer.
