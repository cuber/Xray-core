# Implementation Plan: thresholdLeastPing

Status: approved design, not started. The [spec](spec.md) owns the contract and [tests](tests.md) the cases.
File names are proposals checked against the baseline `291fde47`; field numbers must be rechecked when
the protobuf is edited.

## 1. Configuration (TL-1)

- `infra/conf/router_strategy.go`: add `strategyThresholdLeastPing = "thresholdleastping"` and a settings
  builder with `MaxRTT` and `RecoverRTT` as `*duration.Duration`; enforce required/positive and
  `0 < recoverRTT <= maxRTT`, filling recoverRTT with maxRTT when omitted.
- `infra/conf/router.go`: accept the new type in the strategy switch.
- `app/router/config.proto`: `message StrategyThresholdLeastPingConfig { int64 maxRTT = 1; int64 recoverRTT = 2; }`
  (nanoseconds; field names follow the existing strategy messages), appended after `Config` so no existing
  descriptor index moves. `app/router/config.go`: a `case` building `Balancer{strategy: &ThresholdLeastPingStrategy{...}}`,
  validating the typed message again for direct protobuf and AddRule input.
- leastPing guard (TL-9, D11): leastPing keeps `strategyEmptyConfig` for every other field, but its
  loader rejects a settings object containing `maxRTT` or `recoverRTT` with an error naming
  thresholdLeastPing. Implement as a small typed config for leastPing with only those two fields checked.
- Regenerate protobuf with the repository's pinned toolchain.

## 2. Pure Decision (TL-2, TL-3, TL-4)

New file `app/router/strategy_thresholdleastping.go`:

```go
type tlpOption struct {
    Tag      string
    Kind     optionKind // outbound, member balancer, fallback outbound, fallback balancer
    Known    bool
    Usable   bool
    RTT      time.Duration
    Reason   string
    Agg      *memberMean // roundRobin/random child: mean, measured, dispatchable
}

func decideThreshold(policy tlpPolicy, candidates []tlpOption, fallback *tlpOption,
    terminal terminalKind, latches map[string]bool, firstPending bool) (tlpDecision, map[string]bool)
```

- No observatory, map, lock or clock access; unit tests feed literal values (Q, Y).
- Returns mode, choice, fallback flag, terminal outcome, per-option selection (usable, within-limit,
  latched, reason) and the latch set after this computation's transitions.
- `ThresholdLeastPingStrategy` implements `BalancingStrategy` only to satisfy the existing interface; its
  `PickOutbound` is not used for decisions (section 4).

## 3. Observation Notification (TL-5)

- `common/signal/broadcast.go`: `Broadcast` (Subscribe/Fire; cancel waits for a call in flight; Fire holds
  no lock while calling). `features/extension/observatory.go`: `ChangeNotifier { OnChange(func()) (cancel
  func()) }`, and the process-wide `NotifyRelayChange`/`OnRelayChange`.
- Burst (`app/observatory/burst`): `HealthPing.onChange` (set through `Manager.SetOnChange`) fires after a
  result is put (round loop and `PutResult`), after `Cleanup` and after a scheduled round's cleanup, each
  once `access` is released; `SetForceDown` fires after `force.write` is released (split into
  `setForceDown`). Classic (`app/observatory`): after `updateStatusForResult` releases `statusLock`.
- Hy2 relay (D12): `transport/internet/hysteria` calls `extension.NotifyRelayChange()` after a dial that
  asked for relays stores its bindings; the burst observer, which reads relay status in `createResult`,
  forwards it (`subscribeRelays` at Start, cancelled at Close). The router subscribes only to the
  observatory and the outbound manager.
- Outbound manager: `AddHandler`/`RemoveHandler` notify after they release `access` (runtime changes of
  selector-matched candidates).
- Callbacks run synchronously on the caller's goroutine and must return quickly; the router side only
  marks dirty or runs a short computation (section 4). `Close` drops subscriptions; a callback in flight
  finishes before `Close` returns.

## 4. Router Computation And Publication (TL-5, TL-6)

- `app/router/threshold_pass.go`:
  - `type tlpPass struct { gen uint64; observedAt time.Time; decisions map[string]*tlpDecision }`.
  - `Balancer.pass atomic.Pointer[tlpPass]` on every balancer of a map that contains a thresholdLeastPing.
  - `Router.compute(m)`: under `tlpMu`, read one observation, build a `statusReader` over `m` and it,
    walk thresholdLeastPing balancers children-first over the member/fallback graph (acyclic by
    spec017–019 checks), build options (legacy
    children via `statusReader.balancer`, roundRobin/random means via a new `statusReader.representative`,
    thresholdLeastPing children from this pass), call `decideThreshold` with the previous pass's latches,
    then store the new pass on every balancer of `m`.
  - Coalescing: a `dirty` flag; `compute` loops while dirty, re-reading the observation each time.
  - Subscribe on `Router.Start` when the map contains a thresholdLeastPing; unsubscribe on `Close`.
- `reloadRules`: build the new map, run `compute(newMap)` under `tlpMu`, then publish the map; latches start
  empty because the new map has no previous pass.
- `statusReader.balancer` for a thresholdLeastPing tag returns its decision's status from the pass, so a
  legacy parent bubbles the child's real choice.
- `Balancer.pick` for a thresholdLeastPing: override first; else map the published decision onto the
  existing contract: primary `(tag, false)`; fallback `(fallbackTag, true)` so `resolveChain` continues;
  blackhole `(fallbackTag, true)`; drop or pending without a usable option returns an error wrapping
  `routing.ErrRouteDropped`, which the dispatcher already closes without the default outbound.
- `resolveChain` loads the pass once and hands it to every thresholdLeastPing level it visits.
- `FallbackActive`/`PickShares`/principle target for this strategy read the pass as well.
- An observation of an unexpected type yields a pass in which every option is unknown (reason recorded);
  `decideThreshold` never sees a nil or partial status list as healthy.
- Drops (D14): the decision carries an atomic per-balancer drop counter and last reason; the drop error
  wraps `ErrRouteDropped` with a marker the dispatcher logs at info instead of warning. The loop-guard
  drop keeps its warning.

## 5. Observed-outbound Check (TL-7)

- `checkMemberObservation`: add every thresholdLeastPing balancer to the root set beside balancers with
  members; add the "fallbackTag names nothing" error for those roots. The closure, blackhole exemption,
  `observes()` and its call sites (`guardRouting` for publications, `CheckLoopbackLoops` for start and
  runtime `AddOutbound`) stay as they are. `RemoveHandler` stays unchecked (D15).

## 6. Control Plane

- `app/router/command/command.proto`: `BalancerMsg.threshold_policy = 11`, `threshold_decision = 12`,
  `BalancerMember.selection = 6` (shapes in the spec).
- `features/routing/balancer.go`: `BalancerThreshold { ThresholdReader() ThresholdReader }` on
  `balancerView` pins the newest pass among the view's thresholdLeastPing balancers;
  `ThresholdReader.GetThreshold(tag) (ThresholdReport, bool, error)` returns everything `balancerMsg`
  shows for that level from it (`app/router/threshold_report.go`). `ErrBalancerUnavailable` wraps
  `ErrRouteDropped`; the dispatcher logs it at info.
- `balancerMsg`: when the strategy is thresholdLeastPing, fill members, status, shares, principle target,
  fallback active and thresholdLeastPing chain hops from the report; otherwise unchanged.
- `ListBalancers`: load one pass from the map before the loop and use it for every thresholdLeastPing
  balancer it lists.
- `main/commands/all/api/balancer_info.go`: text columns for policy, latch, reason and observation age.

## 7. Gates

1. Tests in the order of the task list; each stage ends with the relevant `go test` and `-race`.
2. Full regressions, vet, gofmt, the public-content check, then a local commit.
3. The dual-platform build wrapper on the committed tree; the stability gate before any production use.
4. Sidecar and Portal consumers in their own repositories; rollout separately authorized.
