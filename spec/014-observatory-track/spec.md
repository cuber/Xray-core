# Feature Specification: Tracked Outbound Observation

- Feature ID: 014-observatory-track (designed in conversation on 2026-10-29 and approved as a
  spec directly; no draft ID was allocated)
- Created: 2026-10-29
- Status: Approved; implementation not started. Rollout (asz.lite) is
  [xray-config spec015](../../../xray-config/spec/015-observatory-track-rollout/spec.md) and
  needs its own go-ahead.
- Baseline: Core `cf979cbf` (develop)
- Authorization (2026-10-29):
  - The goal: "怎么能够在 asz.lite 里只 ob 两个节点", with the Core change made the sound way
    ("科学合理比改动小但不合理更重要").
  - The approach: "既然 balancer 要依赖 ob，那我们就把多个原本不存在的ob mapping到那一个ob上".
  - The decision: "用 track 更好一点，config.yaml 显示写出来吧，可以把 draft 升级为 spec 了".
- Consumers: [xray-sidecar spec014](../../../xray-sidecar/spec/014-observatory-track/spec.md)
  (`/xray-debug` `observatory_track`, Portal) and xray-config spec015 (asz.lite, generator
  checks, acceptance).

## Problem and Goal

asz.lite reaches HK DMIT through two Aliyun HK forwarders, ahk.ecs and ahk.alpine.1st. Each
forwarder carries the `ahk` account's Hy2 and eight route outbounds. The route outbounds differ
only in the user, and all of them share the path asz.lite → forwarder → HK DMIT.

A failover between the forwarders today costs two things for each route:
- a copy of the outbound through the second forwarder, and a balancer that selects the primary
  and falls back to the copy;
- an observation of the primary. Balancers choose from observatory results only. roundrobin and
  random take an unobserved candidate for alive and never fall back; leastPing and its kin never
  pick one.

The observation is the structural cost. With eight routes asz.lite probes eight more outbounds
over the same path. On 2026-10-29 that fallback was withdrawn for this reason (xray-config
spec010).

The health that matters is the path's health, which one probe per forwarder already measures.
Goal: let an outbound's observation follow another outbound's probe, so a node probes each path
once, while balancers keep their own logic and configuration.

The model is HAProxy's server `track`. A server's state follows another server's health check,
and the tracking server is not checked itself.

## Contract

### Configuration

```json
"burstObservatory": {
  "pingGroups": [{"subjectSelector": ["ahk.ecs-hy2", "ahk.alpine.1st-hy2"], "pingConfig": {}}],
  "track": [{"probe": "ahk.ecs-hy2", "outbounds": ["tk.auto-hy2", "sg.auto-hy2"]}]
}
```

A roundrobin balancer then selects `tk.auto-hy2` and falls back to its copy through
ahk.alpine.1st when `ahk.ecs-hy2` is dead. The copy needs no track of its own, because
`fallbackTag` does not consult health.

- **Fields.**
  - `track` is an optional list of groups. Each group has:
    - `probe`, an outbound tag;
    - `outbounds`, one or more outbound tags, written exactly; they are not prefixes.
  - Proto: `burst.Config.track` (field 5) of `TrackGroup {probe = 1; outbounds = 2}`.
- **Placement.**
  - Only `burstObservatory` has `track`. It works with `pingGroups` and with the legacy
    single-group form.
  - The classic `observatory` has none (non-goals).

### Behaviour

- **T1 Result.** `GetObservation` returns the probed statuses as before. For every tracked
  outbound whose probe has a status, it then adds a copy of that status:
  - the copy keeps `alive`, `delay`, `last_error_reason`, the times and `health_ping`;
  - `outbound_tag` is the tracked tag, and the new `tracked_from` field (`OutboundStatus`
    field 8) names the probe.
  - A probe without a status yet gives its tracked outbounds none, which is the same as
    unobserved.
- **T2 No probing.** A tracked outbound is never probed. Validation (T5) keeps it out of every
  subject selector, so the node sends one probe for each path.
- **T3 Balancers unchanged.**
  - Every strategy reads the result as it does today: roundrobin, random, leastPing,
    leastLoad and weightedLeastPing.
  - A tracked outbound counts as observed, with its probe's alive state, delay and samples.
  - No router or strategy code changes.
- **T4 On-demand checks.** `BurstObservatory.Check(tags)` replaces each tracked tag with its
  probe, removing duplicates, before it checks. Today only the VLESS reverse inbound calls it.
- **T5 Validation at load** (`run -test`, from the outbound list in `infra/conf`). Each of the
  following is refused:
  - an unknown probe or tracked tag;
  - a probe that no subject selector selects;
  - a tracked tag that a subject selector selects (selectors match by prefix);
  - a tracked tag that is itself a probe (no chains);
  - a tag tracked in two groups;
  - an empty `outbounds`.
- **T6 Freshness.** A tracked status is exactly as fresh as its probe's. Track adds no
  smoothing or timer of its own.

## Functional Requirements

| ID | Requirement | Verified by ([tasks](tasks.md)) |
|---|---|---|
| FR-001 | T1: tracked outbounds appear in `GetObservation` with the probe's status and `tracked_from`; nothing appears before the probe's first result | T002 unit |
| FR-002 | T2/T5: every refusal in T5 fails `run -test` with a message naming the group and tag | T003 conf tests |
| FR-003 | T3: a roundrobin balancer with `fallbackTag` falls back when the tracked candidates' probe is dead and returns when it recovers; leastPing, which never picks an unobserved outbound, picks a tracked one | T005 instance test (no router code changes, so no router-only test) |
| FR-004 | T4: `Check` with a tracked tag checks its probe once | T002 unit |
| FR-005 | Control plane (below): `GetTrack` reports the loaded groups and probe statuses; `GetOutboundStatus` keeps reporting probed outbounds only | T005 contract test |
| NFR-001 | Without `track` nothing changes. With it, `GetObservation` adds at most one status per tracked tag, holds no lock beyond today's result walk, and keeps no state between calls | code review, T002 |

## Control Plane

- **Runtime state.** The track groups as loaded, and for each group its probe's current status,
  which every outbound in the group follows. Nothing else is new: tracked statuses are derived
  on every read and never stored.
- **RPC.** A new read-only `ObservatoryService.GetTrack` (CP-4: the service that owns the feature).
  - `GetTrackResponse.groups` lists `TrackStatus {probe = 1; outbounds = 2; probe_status = 3}` in
    configured order. `probe_status` is the probe's `OutboundStatus` and is absent before its
    first result. Without `track` the list is empty.
  - The response is bounded by the configured groups and contains no secrets. It reads the same
    result walk as `GetObservation`, under no further lock.
- **`GetOutboundStatus` keeps its meaning.** It reports probed outbounds only: the command
  service drops the statuses whose `tracked_from` is set. Existing consumers (Sidecar
  `/metrics`, `/xray-debug` `observatory`, xray-tui) therefore see no change and need no release
  order. `tracked_from` marks the copies inside `GetObservation`, where balancers read them.
- **Older Core.** `GetTrack` answers `Unimplemented`, and consumers show track as unavailable
  (CP-6). Such a Core also ignores `track` silently, because unknown JSON fields are ignored. Its
  balancers then see the tracked tags as unobserved:
  - roundrobin and random never fall back, which is today's behaviour;
  - leastPing never picks them and always falls back.
  
  Rollout therefore checks `GetTrack` at runtime, not the config test (xray-config spec015).
- **Enablement.** `GetTrack` lives on `ObservatoryService`, which asz.lite already lists in
  `api.services`. A node without that service answers `Unimplemented` for both RPCs, as it does
  today.
- **Tests.** The contract test uses a real gRPC server on a started instance (CP-7, T005):
  - `GetTrack` returns no groups without `track`, and with it returns the groups with their
    probe's status;
  - `GetOutboundStatus` never contains a tracked tag.
- **Consumers.** Sidecar spec014 owns the `/xray-debug` `observatory_track` field and the Portal
  presentation: the Observatory and Balancers tabs (user: "最好能在portal上显示出 track").
  xray-config spec015 owns the acceptance item `observatory_track`.

## Non-goals

- **Track on the classic `observatory`.** No fleet node uses it (2026-10-29). Add it with the
  first node that does.
- **Prefix or pattern membership, and automatic membership.** Core takes explicit tags, and the
  configuration writes them out ("config.yaml 显示写出来吧").
- **Inferring health beyond the shared path.** A failure after HK DMIT, or a rejected user
  credential, is invisible to a tracked outbound, just as it is to an unobserved one.
- **Changing balancer strategies, `fallbackTag` semantics or the probe methods.**
- **Endpoint failover at dial time** (rejected design below).

## Edge Cases

- **Probe removed or renamed.** The configuration fails to load (T5).
- **Probe dead.** Every outbound tracking it is dead at once, as intended. Their balancers fall
  back together.
- **Older Core with `track`** (Control Plane). Under roundrobin the fallback stays inert, the
  state before this feature. Under leastPing all traffic sits on the fallback, so rollout uses
  roundrobin (xray-config spec015).
- **Probe in a group with `destinationsByPrefix`.** The tracked copies take the probe's result
  whatever URL produced it.
- **Tracked outbound that dials a different path from its probe.** Core cannot tell. The
  generator checks that both dial the same address and port (xray-config spec015).

## Rejected Design

Endpoint failover at dial time was the first proposal of 2026-10-29:
- A top-level `failover` section named forwarder addresses, each with a probe.
- The outbound handler's `Dial` would have rewritten the server address to the first live
  standby.

It would have removed the copies and balancers altogether, but it:
- changes the dial path of every protocol, the mux and Hy2's QUIC client cache (keyed by
  destination);
- needs a new gRPC service;
- makes TLS server names that follow the address depend on the rewrite;
- splits health-based selection between the router and the dialer.

Track keeps selection in balancers and changes only how observations are reported.

## Success Criteria

- SC-001: asz.lite's AHK failover for the `ahk` account and eight routes runs on the existing
  four probes (xray-config spec015).
- SC-002: FR-001–FR-004 tests and the CP-7 contract test pass, and the Core full suite passes.
- SC-003: A configuration without `track` produces the same observation results and balancer
  choices as before (existing observatory and router suites).

## Revision Record

- 2026-10-29: designed and approved as spec014 (Authorization). "alias" was renamed `track`:
  these are different outbounds whose health follows a probe, not names for the same outbound.
- 2026-10-29: control plane revised before implementation (user: "控制面有考虑么？最好能在portal上
  显示出 track").
  - The first version marked tracked statuses with `tracked_from` inside `GetOutboundStatus` and
    left the Portal out.
  - That gave an older Core no `Unimplemented` to show as unavailable, and it made every
    existing consumer filter them.
  - `GetTrack` now reports track, `GetOutboundStatus` keeps probed outbounds only, and the Portal
    shows track.
