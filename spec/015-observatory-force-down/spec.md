# Feature Specification: Observatory Force Down

- Feature ID: 015-observatory-force-down
- Created: 2026-10-29 (draft004); promoted 2026-10-30
- Status: Implementing (2026-10-30): T001–T005 done; T006 (build) open. Rollout is xray-config spec016 and needs its own go-ahead.
- Baseline: Core `31ef26bd` (develop, spec014 track)
- Origin (2026-10-29, user): "回到 asz.lite 上如果把 ahk.ecs 和 ahk.alpine 的 ob 支持一个 forceDown 是不是更加简单一些，
  控制也容易一些，但是就要给 xray-core 开发 ob forceDown 对不对？加一个控制面".
- Approval (2026-10-30, user): the draft's open points are settled (limit 190 GB, OpenAPI every 5 minutes, month
  rollover released only after OpenAPI confirms the reading cleared; Sidecar spec015). Added: "core 的 force
  状态要在控制面有体现，portal 也打个强制关停的 badage".
- Promoted from draft004 (ID reserved in `draft/README.md`).
- Consumers: [xray-sidecar spec015](../../../xray-sidecar/spec/015-cdt-quota-guard/spec.md) (CDT quota
  decision, `/xray-debug`, metrics, Portal) and [xray-config spec016](../../../xray-config/spec/016-ahk-cdt-quota/spec.md)
  (asz.lite balancers, acceptance).

## Problem and Goal

asz.lite reaches HK DMIT through two Aliyun HK forwarders, ahk.ecs and ahk.alpine.1st, each in its
own Aliyun account with 200 GB of free CDT traffic a month. Past the free quota, traffic is billed.
The operator wants a forwarder taken out of use when its month's traffic nears the quota, while the
other one keeps serving, and all traffic to stop when both are used up.

The forwarders are already probed by `burstObservatory` (`ahk.ecs-hy2`, `ahk.alpine.1st-hy2`), and
asz.lite's balancers follow those probes, directly or through spec014 `track`. A probe that reads
dead is therefore enough: every balancer moves off that forwarder by itself. What is missing is a
way for the control plane to declare a healthy outbound dead for selection. Today the
observatory's state is read-only.

Goal: a runtime, control-plane-owned set of probed outbounds that balancers treat as dead, visible
in every control-plane read of the observatory, without changing probing, strategies or configuration.

The model is a load balancer's administrative down (HAProxy `MAINT`): the server stays checked,
but receives no new traffic until it is released.

## Contract

### Behaviour

- **F1 Runtime state only.** The force-down set lives in the running instance. It is not part of
  the configuration, is empty at start, and is lost when Core restarts. The owner (Sidecar spec015)
  re-applies it.
- **F2 Declarative replace.** `SetForceDown` replaces the whole set with the request's entries.
  An empty request clears it. Re-sending the same set is a no-op and keeps each entry's `since`; an
  entry that stays in the set keeps its `since` even when its `reason` changes.
- **F3 Members.** Every entry names an outbound that a `burstObservatory` subject selector selects
  (a probe). Unknown tags, tracked outbounds (spec014), unprobed outbounds and duplicate tags are
  refused; the request fails as a whole with `InvalidArgument` and the set is unchanged. Without a
  burst observatory the request fails with `FailedPrecondition`.
- **F4 Effect on selection.** In `GetObservation`, which every balancer strategy reads, a forced
  probe's status has `alive = false` and `last_error_reason = "force down: <reason>"`; its delay
  and health-ping fields keep the measurement. Spec014 track copies the effective status, so
  every outbound tracking a forced probe is dead as well.
  - roundrobin and random skip it; leastPing, leastLoad and weightedLeastPing never pick it;
  - with no alive candidate a balancer uses its `fallbackTag`, unchanged. With no fallback Core
    uses the default outbound handler, as today; a configuration that wants traffic stopped sets
    `fallbackTag` to a blackhole outbound (xray-config spec016).
- **F5 Probing continues.** Forced probes are still probed. Releasing an entry restores the
  measured status at the next read, with no extra delay.
- **F6 Existing connections.** Only new routing decisions change; connections already dispatched
  keep running until they end.
- **F7 Bounds.** At most one entry per probed outbound; `reason` is at most 256 bytes. The set is
  swapped atomically as one immutable snapshot that the observation read path only loads.
- **F8 Reported in every observatory read.** `OutboundStatus` carries the force state:
  `force_down`, `force_down_reason` and `force_down_since` are set on a forced probe's status and,
  inherited, on every tracked copy of it (`tracked_from` names the probe).
  - `GetObservation` (selection view): `alive = false` as F4, plus the fields.
  - `GetOutboundStatus` and `GetTrack`'s `probe_status` (control-plane view): `alive`,
    `delay` and `health_ping` stay the measurement, plus the fields. `/metrics`, dashboards and alert
    rules therefore see real health, and a consumer shows a forced probe as "probe healthy, taken out
    by the operator" rather than as an outage.
  - Unforced statuses leave the three fields at their zero values.

### Proto (additive)

```proto
// app/observatory/config.proto, OutboundStatus
  // Set while the control plane holds this outbound out of selection (spec015): on a
  // forced probe and on every status tracked from it.
  bool force_down = 9;
  string force_down_reason = 10;
  int64 force_down_since = 11;   // Unix seconds the probe entered the force-down set

// app/observatory/command/command.proto
message ForceDownEntry {
  string outbound_tag = 1;
  string reason = 2;
  int64 since = 3;   // Unix seconds when the tag entered the set; ignored in requests
}
message SetForceDownRequest { repeated ForceDownEntry entries = 1; }
message SetForceDownResponse { repeated ForceDownEntry entries = 1; }  // the applied set
message GetForceDownRequest {}
message GetForceDownResponse { repeated ForceDownEntry entries = 1; }

service ObservatoryService {
  // existing: GetOutboundStatus, GetTrack
  rpc GetForceDown(GetForceDownRequest) returns (GetForceDownResponse) {}
  rpc SetForceDown(SetForceDownRequest) returns (SetForceDownResponse) {}
}
```

Placement (CP-4): the service that owns observation. No new service, no configuration field.

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | F2/F3: a valid set is applied atomically and echoed; an invalid entry refuses the whole request and leaves the set unchanged; re-sending keeps `since` | unit |
| FR-002 | F4: a forced probe and every outbound tracking it read `alive = false` in `GetObservation`; a roundrobin balancer moves to its other candidate, then to `fallbackTag` when all are forced | started-instance test through the real router |
| FR-003 | F5: probing continues; after release the measured status returns at once | started-instance test |
| FR-004 | F1: a restarted instance starts with an empty set | started-instance test |
| FR-005 | F8 and Control plane (below): `GetForceDown` reports the set; `GetOutboundStatus` and `GetTrack` keep the measurement and carry the force fields | CP-7 contract test |
| NFR-001 | An empty set changes nothing in `GetObservation`; the read path takes no lock beyond today's result walk plus one atomic load | review, unit |

## Control Plane

- **Runtime state.** The force-down set: tag, reason and since per entry.
- **Read RPCs.**
  - `ObservatoryService.GetForceDown` (CP-3): the set, bounded by the probed outbounds, no secrets (CP-5).
  - `OutboundStatus.force_down*` (F8) in `GetOutboundStatus`, `GetTrack` and `GetObservation`, so no
    consumer has to join two calls to know which probe is forced.
- **Mutating RPC.** `ObservatoryService.SetForceDown`, with its own requirements (CP-3):
  - **Authorization.** The same as every other Core write: whoever can reach the API endpoint
    (asz.lite: `/run/xray/api.sock`, mode 0600). No new surface.
  - **Failure.** Validation before any change; the set is replaced as a whole or not at all.
  - **Rollback.** `SetForceDown` with no entries, or a Core restart.
- **Measurements stay truthful.** `GetOutboundStatus` and `GetTrack` report each probe's measured
  status (F8). Metrics, dashboards and alert rules see real health.
- **Older Core.** Both RPCs answer `Unimplemented` (CP-6) and the three fields are absent. The
  Sidecar then reports force down as unavailable and cannot enforce; the configuration itself is
  unchanged and loads on any Core.
- **Enablement.** `ObservatoryService` in `api.services`, which asz.lite already lists.
- **Tests (CP-7).** A real gRPC server on a started instance: `GetForceDown` empty by default;
  `SetForceDown` then `GetForceDown` round trip; `TestRoute` moves off the forced probe and its
  tracked outbounds; `GetOutboundStatus` and `GetTrack` still report the measurement with
  `force_down` set; an older observatory (classic) answers `GetForceDown` empty and refuses
  `SetForceDown`.
- **Consumers (CP-8).** Sidecar spec015 owns the `/xray-debug` fields (`observatory` and
  `observatory_track` force fields, `cdt_guard`), the metric and the Portal "强制关停" badge and CDT
  card; xray-config spec016 owns the acceptance item. `xray-tui` keeps showing measured health only:
  a non-goal dated 2026-10-30 (it has no force-down operator); add the marker when it gains one.

## Non-goals

- **Persisting the set** or a configuration field for it: the decision belongs to the control
  plane, which re-applies it.
- **Forcing an outbound up**, or overriding a measured dead probe as alive.
- **The classic `observatory`**: no fleet node that needs this uses it (2026-10-29).
- **Cutting established connections.**
- **`RoutingService.OverrideBalancerTarget`** as the mechanism: it pins one target per balancer and
  ignores health, so a forced-off forwarder's partner could not fail over.

## Edge Cases

- **Probe removed by a reload.** Core has no reload; a restart clears the set, and the owner's
  next `SetForceDown` is validated against the new configuration.
- **Every candidate forced.** The balancer uses `fallbackTag`; without one, the default outbound
  (F4). On asz.lite the default outbound is `ahk.ecs-hy2`, so its configuration sets `block`.
- **Forced probe also measured dead.** It stays dead after release until the probe recovers.
- **Forced before its first result.** A probe has no status until its first result, and a balancer
  counts an unobserved candidate as alive. `GetObservation` therefore reports a synthetic status for such
  a probe, `alive = false` with the force fields and no measurement, and its tracked copies follow it.
  `GetOutboundStatus` and `GetTrack` report the same status, `force_down` set. The entry is accepted
  because the probe is a configured subject (F3), probed or not yet.

## Success Criteria

- SC-001: asz.lite takes a forwarder out of selection within one Sidecar decision interval of its
  quota decision, and keeps the other in use (xray-config spec016).
- SC-002: FR tests and the CP-7 contract test pass; the Core full suite passes.
- SC-003: With an empty set, observation results and balancer choices are unchanged (existing
  observatory and router suites).
