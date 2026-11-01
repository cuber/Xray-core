# Feature Specification: weightedLeastPing Fallback

- Feature ID: 016-weighted-least-ping-fallback
- Created: 2026-11-02
- Status: Implementing (2026-11-02): T001–T004 done; T005 (build, gate) and T006 (hq rollout) open.
- Baseline: Core `f4fc16df` (develop, spec015)
- Origin (2026-11-02, user): hq and the local source dial la.aaitr.att through two domestic boards at weight
  0.1, "然后 fallback 走 vgz.ecs". Asked how, given that weightedLeastPing never reaches a `fallbackTag`
  (spec001 FR-006), the user chose "改 Core 支持 fallback" over a small-weight candidate.
- Consumers: [xray-config](../../../xray-config/xray/nodes/hq/README.md) hq and the local source
  (`la.aaitr.att-balancer`, fallback `la.aaitr.att-vgz.ecs-ss`, config commit `5e4ff352`).

## Problem and Goal

A `weightedLeastPing` balancer (spec001 FR-006, spec005) picks by weight among its healthy candidates and,
when none is healthy, among every candidate with a positive weight. Its strategy therefore never returns
an empty pick, and the balancer's `fallbackTag` is never used: a configured fallback is dead weight, and
an operator cannot send traffic elsewhere when every candidate is down.

Goal: a `weightedLeastPing` balancer that has a `fallbackTag` uses it once every candidate has been
measured and none is alive, and keeps today's behaviour in every other case.

## Contract

### Behaviour

- **W1 Fallback when every candidate is down.** For a balancer with a `fallbackTag`, when the healthy pool
  is empty, every positive-weight candidate has a verdict (W3) and none of them is alive, the strategy
  yields no candidate. `Balancer.PickOutbound` then returns the `fallbackTag`, unchanged
  (`app/router/balancing.go`).
- **W2 Otherwise unchanged.** Without a `fallbackTag`, or while a candidate is alive (slow, failing beyond
  `tolerance` or outside the RTT window), or while a candidate has no verdict yet, the strategy keeps
  spec001 FR-006: an empty healthy pool falls back to every positive-weight candidate. The default
  outbound handler is never reached through this change.
- **W3 Verdict.** A candidate has a verdict when the observation carries its status and, for a burst
  observatory, its health ping holds at least `max(1, minSamples)` samples. A candidate missing from the
  observation (not probed, or no observation at all) has none. A freshly started Core therefore keeps
  W2 until each candidate's first samples arrive: no burst of traffic to the fallback at start.
- **W4 Alive.** `alive` as read from `GetObservation`: a spec015 forced probe and every outbound tracking
  it (spec014) count as not alive, so forcing every candidate down reaches the fallback (W1).
- **W5 Selection only.** Probing, weights, the healthy pool and the smooth weighted round robin are
  unchanged. Only new routing decisions change; dispatched connections keep running.

### Proto (additive)

```proto
// app/router/command/command.proto
message FallbackInfo {
  // The balancer's fallbackTag as loaded; empty when it has none.
  string tag = 1;
  // The strategy currently yields no candidate, so new picks use `tag` (an override still wins).
  bool active = 2;
  // The strategy reports `active` (weightedLeastPing, spec016); false for the others, whose
  // `active` is unknown rather than false.
  bool reported = 3;
}

message BalancerMsg {
  OverrideInfo override = 5;
  PrincipleTargetInfo principle_target = 6;
  FallbackInfo fallback = 7;
}
```

Placement (CP-4): `RoutingService.GetBalancerInfo`, which already reports the balancer's override and
principle targets. No new service and no configuration field.

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | W1: with a `fallbackTag`, every candidate measured and none alive, the strategy picks nothing and the balancer picks the fallback | unit (strategy, balancer) |
| FR-002 | W2: without a `fallbackTag`, with an alive but slow or failing candidate, or with a candidate lacking a verdict, an all-unhealthy pool still yields every positive-weight candidate | unit |
| FR-003 | W3: a candidate below `max(1, minSamples)` samples or missing from the observation blocks the fallback; no observation keeps W2 | unit |
| FR-004 | W4: forcing every candidate down (spec015) reaches the fallback; releasing returns to the candidates | started-instance test |
| FR-005 | Control plane: `GetBalancerInfo` reports `fallback` (tag, active, reported) as below | CP-7 contract test |

## Control Plane

- **Runtime state.** Whether a balancer's selection currently ends on its fallback. It is computed from
  the observation at each pick; nothing new is stored.
- **Read RPC.** `RoutingService.GetBalancerInfo` gains `BalancerMsg.fallback` (CP-3, read only): the
  loaded `fallbackTag`, `active` and `reported`. For weightedLeastPing `active` is exactly W1 and
  `principle_target` is empty while it holds; other strategies report `reported = false` and consumers
  show their `active` as unknown (CP-6). The response holds tags only (CP-5) and takes the same reads as
  `principle_target`: one observation walk, no lock beyond the strategy's existing ones.
- **Compatibility (CP-6).** An older Core leaves `fallback` unset; consumers render it unavailable, never
  inactive.
- **Tests (CP-7).** A started instance with a burst observatory, weightedLeastPing balancers with and
  without a `fallbackTag` and a roundrobin one, read through a real gRPC server in the default (healthy)
  state, with every candidate failing its probe and with every candidate forced down.
- **Consumers (CP-8).** Sidecar `/xray-debug` and the Portal do not read `GetBalancerInfo` today; showing
  the fallback there is a non-goal recorded 2026-11-02, to be planned in Sidecar and Config specs when a
  balancer view is added. hq's tracked `api.services` already lists `RoutingService`.

## Notes

- spec015 F4 says weightedLeastPing "never picks" a forced probe. That holds while another candidate is
  healthy; with every candidate forced and no `fallbackTag`, FR-006's all-candidate fallback still picks
  among them. W1 makes a `fallbackTag` the way to stop that; without one the behaviour stays as it was.
- roundrobin with a `fallbackTag` already falls back when no candidate is alive, and counts a candidate
  missing from the observation as alive; W3 is the weightedLeastPing analogue, stricter at start.
