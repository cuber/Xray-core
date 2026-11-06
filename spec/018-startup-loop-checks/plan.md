# Plan: Loops Refused Before Start

- Spec: [spec.md](spec.md)
- Status: implemented 2026-11-06 (T002–T006, as below); gate T007 open.

## Code

- `common/loopcheck` (new): `Functional`, the cycles of a graph with at most one edge per vertex, and `Format`.
  spec017's `checkFallbackCycles` now uses it too (same L4 text; its tests unchanged).
- `features/outbound/loops.go` (new): `LoopView`, `LoopChecker`, `GraphGuard`, and `LoopbackReached`, which
  follows a handler's `dialerProxy` chain to a loopback for both the manager and the router.
- `app/proxyman/outbound/loops.go` (new), S1 and the check lock:
  - The proxyman `Handler` gains `DialerProxy() string` (its `streamSettings.SocketSettings.DialerProxy`). A handler
    without it (one registered by an app, such as the API's) has no edge.
  - `checkDialerProxyCycles(handlers)`: each handler has at most one edge, so the walk of spec017's
    `checkFallbackCycles` applies (three colours over sorted tags, cut at the smallest tag; linear). The S4 text.
  - `Manager.CheckLoops() error`: S1 over every handler, then S2 through the router when it implements
    `routing.LoopChecker`. Without a routing section (`routing.DefaultRouter`) every state's walk ends at the default
    outbound, so the manager refuses a default outbound that is a loopback or dials through one.
  - The S3.4 lock is the manager's `graph` mutex, exposed through `outbound.GraphGuard.LockGraph`; every instance
    has one manager. The router takes it before its own `mu`; the manager's checks read the published rules
    without `mu`, so the order cannot deadlock.
  - `AddHandler`, once the manager runs: under the lock, an existing tag is refused first (as before), then S1 and
    S2 over the current handlers plus the new one, before it is stored. During `core.New` handlers are added one by
    one; partial sets are not checked, the start check sees them all.
- `app/router/loops.go` (new), S2:
  - States are the `inboundTag`s fed by loopback handlers. A loopback is recognised from `ProxySettings()`: its
    typed message's instance has `GetInboundTag() string` (no import of `proxy/loopback`, which depends on routing).
  - A rule's inbound tags and whether the inbound tag is its only condition come from its `RoutingRule` proto
    (`Rule.Config`), through `protoreflect`: every populated field but `tag`, `balancing_tag`, `rule_tag` and
    `webhook` is a condition, so a field an upstream update adds can only make the check stricter. A test sets each
    field and checks that `notConditions` names existing fields.
  - Balancer levels: candidates are the handler tags that the level's selectors prefix-match, plus the level's
    override target; the fallback chain is walked on the map the check is about to publish (spec017 L6's map).
  - `(r *Router) CheckLoops(handlers []outbound.Handler) error` implements `routing.LoopChecker`; diagnostics follow S4
    (Tarjan over the states, every cycle printed with its rule, balancer level and outbound).
  - `reloadRules` (both modes) runs S2 after spec017's L1, before `r.balancers.Store`; `RemoveRule` and
    `SetOverrideTarget` run it on the state they would publish. All three hold the lock from the check to the
    publish. A router over a manager that is no `GraphGuard` (the gomock managers of older tests) is not checked.
  - Spec017's L7 tests made their loop with rules, which start now refuses; `proxy/loopback` and the CP-7
    contract make it with a routing script instead (`TestLoopGuardsContract`).
- `core/xray.go`: `initInstanceWithConfig` calls the outbound manager's `CheckLoops` after `addOutboundHandlers`
  (S3.1) and returns its error, so `xray run -test` exits 23. No proto change.

## Tests

- `common/loopcheck/loopcheck_test.go`.
- `app/proxyman/outbound/loops_test.go`: FR-001 (`TestDialerProxyCycles`), the routing-less default
  (`TestDefaultLoopWithoutRouting`).
- `app/router/loops_test.go`: FR-002 (`TestLoopbackLoopsRefused`), FR-003 (`TestLoopbackLoopsAccepted`),
  `TestRoutingRuleConditions`.
- `main/run_loop_check_test.go`: FR-004 (`run -test` exit codes and texts), beside spec017's
  `run_fallback_cycle_test.go`.
- `app/router/command/loop_check_test.go`: FR-005 (`TestLoopChecksOnChange`), FR-006
  (`TestLoopChecksAreSerialised`) through a started instance's gRPC server, `-race`.
- `proxy/loopback/loopback_test.go`: FR-007 (`TestLoopbackReentry`: a rule loop refused at start, a routing
  script's loop ended by L7).
- Full `go test ./... -count=1`, `-race -count=3` on the touched packages, `go run ./infra/vformat/main.go -pwd .`.

## Gate and rollout

- Build from the committed tree with xray-config `xrayctl build core`; the stability-check gate runs the
  dispatcher row (`singmux-check` A/B, server on at least two Akile hosts): the change touches instance start,
  the router's publish path and the outbound manager.
- Nodes take it with their next change (xray-config TODO-045 rule); no fleet configuration changes (spec S6).

## Rollback

- A Core without this spec accepts every configuration this one accepts; going back is the usual `switch revert`.
