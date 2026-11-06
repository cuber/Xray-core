# Plan: Balancers As Members

- Spec: [spec.md](spec.md)
- Status: implemented 2026-11-06 (T002–T006, as below); gate T007 open.

## Code

- `app/router/config.proto`: `BalancingRule.balancers` (repeated string, field 6); `infra/conf/router.go`
  `BalancingRule.Balancers` (`json:"balancers"`), refused when it repeats a tag.
- `app/router/balancing.go`: `Balancer.members []string`; `SelectOutbounds` returns the selector's outbounds, then
  the members (M2). `Rule.GetTag` keeps the one-level pick.
- `app/router/fallback_chain.go` becomes the balancer graph:
  - the publish check walks member and fallback edges (Tarjan; a balancer may have many member edges, so
    `loopcheck.Functional` no longer fits) and prints `-[member]->` / `-[fallback]->` (M7); a member that names
    no balancer is refused there too (M2);
  - `resolveChain` continues when a level's pick is a member balancer, as when it is a fallback naming one,
    within the same len(map)+1 guard (M3, M8).
- `app/router/member_status.go` (new), M4 and M5:
  - `statusReader.balancer`: memoised over one read, override first, then the level, then the fallback. The
    status it bubbles up is the measurement of the candidate it comes from (the best available one, else the
    first down one), under the balancer's tag, so weightedLeastPing's sample counts carry over;
  - `memberObservatory` wraps the observatory a strategy reads for a balancer with members: `GetObservation`
    returns the observatory's result plus one `OutboundStatus` per known member, computed on the balancer's
    `peers` map. Every strategy embeds `memberWrap`, which `Balancer.InjectContext` sets before the strategy takes
    its observatory, so the strategies' decisions are unchanged; roundrobin and random read the observatory when
    their level has members even without a fallbackTag.
- M6: `features/extension.ObservedOutbounds` (`Observes(tag)`: a subject selector's prefix, a ping group's, or a
  `track` follower), implemented by `app/observatory` and `app/observatory/burst`. `Router.CheckLoopbackLoops`
  (spec018's start check and every `AddOutbound`) runs it and marks the router started; `guardRouting` (`AddRule`,
  `RemoveRule`, overrides) runs it after that, since at Init the outbounds are not added yet.
- spec018 S2 (`app/router/loops.go`): `loopGraph.chain` adds each member's level recursively.
- Control plane: `BalancerMsg.members` (`MemberInfo{tag, balancer}`) and `BalancerMsg.status`
  (`MemberStatus{alive, delay, reported}`), additive fields; `command.go` fills them from the one map a read loads.

## Tests

- `app/router/member_test.go`: FR-001, FR-002, FR-005 (every strategy as the outer level; leastPing inside).
- `app/router/fallback_chain_test.go`: FR-004 (the graph check replaces the fallback-only cases, which stay).
- `infra/conf/router_test.go`: JSON `balancers`.
- `main/run_balancer_members_test.go`: FR-003, FR-004 exit codes.
- `app/observatory/command/members_contract_test.go`: FR-006 through a started instance.
- `app/router/members_runtime_test.go`: FR-007 with `-race` and concurrent reloads.

## Gate and rollout

- The change reaches `PickRoute` and the strategies' observation: `xrayctl gate plan` is L2 (the full sing-mux
  A/B, baseline reused when cached).
- Rollout and the configuration change belong to xray-config spec021: its deployment gate requires this spec's
  commit for a configuration with `balancers`.

## Rollback

- `switch revert` to the previous Core and configuration together; an older Core must not run a configuration with
  `balancers` (spec021's gate).
