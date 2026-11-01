# Plan: weightedLeastPing Fallback

## Code

- `app/router/strategy_weightedleastping.go`: `FallbackTag` on the strategy, set by
  `BalancingRule.Build` like `RoundRobinStrategy`'s. `filterNodes` counts the candidates with a verdict
  (W3) and the alive ones (W4); with an empty healthy pool it returns no node when W1 holds, every
  positive-weight node otherwise (W2). `FallbackActive` reports W1 for the control plane.
- `app/router/balancing.go`: `Router.GetFallback` returns the balancer's `fallbackTag`, and `active` and
  `reported` from a strategy implementing `BalancingFallbackState`. `PickOutbound` is unchanged: an empty
  pick already goes to the `fallbackTag`.
- `features/routing/balancer.go`: the `BalancerFallback` interface next to `BalancerPrincipleTarget`.
- `app/router/command`: `FallbackInfo` and `BalancerMsg.fallback = 7` (additive), regenerated with the local
  protoc like spec015's files; `GetBalancerInfo` fills it.

## Tests

- Strategy units next to the FR-006 ones: W1, every W2 case, W3 at and below `minSamples`, missing status,
  no observation.
- A balancer unit: `PickOutbound` returns the fallback under W1.
- CP-7 contract (`app/observatory/command`, beside spec015's fixtures): probes against a local 204 endpoint
  that the test fails and restores, plus force down.

## Rollout

Build through `xrayctl build core`, the stability-check gate on the Akile hosts, then `switch upgrade`
on hq (its Core moves from `cf979cb` to the candidate; the Sidecar stays). The local source stays
stopped. Other nodes need nothing: no other weightedLeastPing balancer has a `fallbackTag`.
