# Plan: Balancer Fallback Chains

- Spec: [spec.md](spec.md)
- Status: not started (2026-11-05).

## Code

- `app/router/fallback_chain.go` (new):
  - `checkFallbackCycles(map[string]*Balancer) error` (L1, L4). Three colours over the tags in sorted order; each
    balancer has at most one edge, so a walk follows it until a done vertex, a non-balancer tag or a vertex on the
    current path (a cycle, cut at its smallest tag). Linear in the number of balancers.
  - `resolveChain(first *Balancer, m map[string]*Balancer) (string, error)` (B1–B4, L6). An iterative loop over
    `Balancer.pick`, bounded by len(m)+1. Only a fallback result is looked up in `m` (B3). When the bound is
    exceeded it returns `routing.ErrRouteDropped` and increments the router's `fallback_chain` counter.
  - `chainInfo(tag string, m map[string]*Balancer) []FallbackHop` for the control plane, built on the same map.
- `app/router/balancing.go`: `Balancer.pick() (tag string, fallback bool, err error)` carries what `PickOutbound`
  decides today and says whether the result is the `fallbackTag`. `PickOutbound` stays as the single-level wrapper
  for external callers.
- `app/router/router.go`:
  - `reloadRules` calls `checkFallbackCycles(newBalancers)` after the rule loop and before `InjectContext` and
    `r.balancers.Store` (L2, L5). A failure returns before anything is published; the deferred `closeWebhooks`
    already covers created rules (L3).
  - `PickRoute` resolves a rule's balancer through `resolveChain(rule.Balancer, *r.balancers.Load())`, loading
    the map once per pick (L6). `Rule.GetTag` keeps the non-balancer path.
  - The guard counters live on the `Router` as two atomics; an internal `routing.LoopGuard` interface
    (`features/routing`) lets the loopback outbound record a re-entry.
- `app/router/lua.go`: `PickOutbound(tag)` loads the map once and calls `resolveChain` (B7).
- Strategies: `roundrobin`, `random`, `leastPing` and `leastLoad` implement `BalancingFallbackState` (spec016).
  Each extracts its alive/qualified filter into one function used by both `PickOutbound` and `FallbackActive`;
  `FallbackActive` never advances the round-robin index or rolls a die.
- `features/routing`: `ErrRouteDropped` and the `LoopGuard` interface.
- `app/dispatcher/default.go`: a `PickRoute` error that `errors.Is(err, routing.ErrRouteDropped)` closes the link
  like a missing outbound tag. Every other error keeps the default outbound (B4). The "DO NOT CHANGE" branch for
  a missing outbound tag stays as it is.
- `proxy/loopback/loopback.go` and `common/session`: `ContextWithLoopbacks` / `LoopbacksFromContext` hold the
  loopback tags the connection has passed. `Process` refuses its own tag with `ErrRouteDropped`, logs the path and
  calls `LoopGuard.RecordLoopbackReentry` through the routing feature it already resolves (L7).
- `infra/conf/xray.go`: after outbounds and routing are built, a `fallbackTag` equal to both an outbound tag and a
  balancer tag is an error (L9).
- `app/router/command`: `FallbackHop`, `FallbackInfo.balancer = 4` and `chain = 5`, `ListBalancers` and
  `GetLoopGuards` (additive), regenerated with the local protoc like spec015/016. `GetBalancerInfo` fills the new
  fields from one map load.

## Tests

- `app/router/fallback_chain_test.go`: L1/L4 shapes (self, two, three, a tail into a cycle, two disjoint cycles
  for the message order, 64 levels acyclic); `resolveChain` for every strategy at two and three levels; B3 with a
  candidate tag equal to a balancer tag; B4; B5 outer and inner override; B6 shared round-robin index; the guard
  on a hand-built cyclic map (white box).
- `app/router/router_test.go`: `reloadRules` replace and append (L5) rejected with the old map still picking;
  concurrent reloads during picks under `-race`; a test that `r.balancers.Store` has one call site.
- `app/router/lua_test.go`: a chain through Lua.
- `proxy/loopback`: a started instance with rule → loopback → the same rule (one pass, counted) and with two
  loopbacks in sequence (works).
- `infra/conf`: L9 collision.
- `main` CLI test: `run -test` on a cyclic config exits 23 with the L4 text.
- CP-7 contract in `app/router/command` (or beside spec016's in `app/observatory/command`): a three-level chain
  healthy, first level forced down, first two forced down; `ListBalancers` equals `GetBalancerInfo`;
  `GetLoopGuards` zero, then one after a loopback loop.

## Gate and rollout

- `go test ./... -count=1`, `-race` on `app/router`, `app/dispatcher` and `proxy/loopback`, gofmt, darwin and linux
  builds through `xrayctl build core`.
- The change reaches the dispatcher, so the stability-check gate is the dispatcher row: `singmux-check` A/B with
  the server on at least two Akile hosts, plus the full suite.
- Deployment belongs to xray-config spec019: asz.lite first, through `switch upgrade`.
