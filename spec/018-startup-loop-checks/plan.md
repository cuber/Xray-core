# Plan: Loops Refused Before Start

- Spec: [spec.md](spec.md)
- Status: approved 2026-11-06; not implemented.

## Code

- `app/proxyman/outbound/loops.go` (new), S1 and the check lock:
  - The proxyman `Handler` gains `DialerProxy() string` (its `streamSettings.SocketSettings.DialerProxy`). A handler
    without it (one registered by an app, such as the API's) has no edge.
  - `checkDialerProxyCycles(handlers)`: each handler has at most one edge, so the walk of spec017's
    `checkFallbackCycles` applies (three colours over sorted tags, cut at the smallest tag; linear). The S4 text.
  - `Manager.CheckLoops() error`: S1 over every handler, then S2 through the router when it implements
    `routing.LoopChecker`. Without a routing section (`routing.DefaultRouter`) every state's walk ends at the default
    outbound, so the manager refuses a default outbound that is a loopback or dials through one.
  - The S3.4 lock is the manager's `graphMu`, exposed to the router through `outbound.GraphLocker` (features); every
    instance has one manager.
  - `AddHandler`, once the instance passed the start check: under `graphMu`, S1 and S2 over the current handlers plus
    the new one, before it is stored. During `core.New` handlers are added one by one; partial sets are not checked,
    the start check sees them all.
- `app/router/loops.go` (new), S2:
  - States are the `inboundTag`s fed by loopback handlers. A loopback is recognised from `ProxySettings()`: its
    typed message's instance has `GetInboundTag() string` (no import of `proxy/loopback`, which depends on routing).
  - Per rule, `reloadRules` records the rule's inbound tags and whether the inbound tag is its only condition,
    from the `RoutingRule` proto. A test walks the proto's fields with `protoreflect` and fails on a field that is
    neither classified as a condition nor listed as not one (`tag`, `balancing_tag`, `rule_tag`, webhook settings),
    so an upstream field is never silently taken as absent.
  - Balancer levels: candidates are the handler tags that the level's selectors prefix-match, plus the level's
    override target; the fallback chain is walked on the map the check is about to publish (spec017 L6's map).
  - `(r *Router) CheckLoops(handlers []outbound.Handler) error` implements `routing.LoopChecker`; diagnostics follow S4
    (Tarjan over the states, every cycle printed with its rule, balancer level and outbound).
  - `reloadRules` (both modes) runs S2 after spec017's L1, before `r.balancers.Store`; `RemoveRule` and
    `SetOverrideTarget` run it on the state they would publish. All three hold `graphMu` from the check to the publish.
- `core/xray.go`: `initInstanceWithConfig` calls the outbound manager's `CheckLoops` after `addOutboundHandlers`
  (S3.1) and returns its error, so `xray run -test` exits 23.
- `features/routing`: `LoopChecker`; `features/outbound`: `GraphLocker`. Both internal interfaces, no proto change.

## Tests

- `app/proxyman/outbound/loops_test.go`: FR-001.
- `app/router/loops_test.go`: FR-002, FR-003 with fake handlers; the `protoreflect` classification test.
- `main/run_loop_check_test.go`: FR-004 (`run -test` exit codes and texts), beside spec017's
  `run_fallback_cycle_test.go`.
- `app/router/command/loop_check_test.go`: FR-005, FR-006 through a started instance's gRPC server (`AddRule`,
  `RemoveRule`, `OverrideBalancerTarget`, `HandlerService.AddOutbound`), `-race`.
- `proxy/loopback/loopback_test.go`: FR-007 (a routing script's loop through a loopback still ends at L7).
- Full `go test ./... -count=1`, `-race -count=3` on the touched packages, `go run ./infra/vformat/main.go -pwd .`.

## Gate and rollout

- Build from the committed tree with xray-config `xrayctl build core`; the stability-check gate runs the
  dispatcher row (`singmux-check` A/B, server on at least two Akile hosts): the change touches instance start,
  the router's publish path and the outbound manager.
- Nodes take it with their next change (xray-config TODO-045 rule); no fleet configuration changes (spec S6).

## Rollback

- A Core without this spec accepts every configuration this one accepts; going back is the usual `switch revert`.
