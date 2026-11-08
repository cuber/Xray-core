# Plan: Balancer Picks On The Control Plane

- Spec: [spec.md](spec.md)

## Code

- `app/router/command/command.proto`: `BalancerMember.status` (3), `.weight` (4), `.share` (5);
  `BalancerMsg.strategy` (10). Regenerated with the protoc the file names.
- `features/routing/balancer.go`: `BalancerMember` gains `Status`, `Weight`, `Share`; `BalancerMembers` gains
  `GetStrategy(tag)`.
- `app/router/balancing.go`: `BalancingShares` (`PickShares(candidates) (shares, weights map[string]float64)`),
  implemented by every strategy with the decision its `PickOutbound` makes, without its side effects:
  leastPing's pick; weightedLeastPing's `getNodes` pool and `weightFor`; roundrobin's and random's
  `aliveCandidates`; leastLoad's `pickOutbounds`.
- `app/router/member_status.go`: `balancerView.GetMembers` reads the observation once, fills each candidate's
  status (the observation for an outbound, `statusReader.balancer` for a member) and its share (override first,
  then an active fallback, then the strategy); `GetStatus` shares the observation helper.
- `app/router/command/command.go`: copies the new fields.
- `main/commands/all/api/balancer_info.go`: P5.

## Tests

- `app/router/picks_test.go`: FR-001–FR-003.
- `app/observatory/command/members_contract_test.go`: FR-004 (statuses and shares added to the spec019 steps).
- `main/commands/all/api/balancer_info_test.go`: FR-005 against a fake routing server.

## Gate and rollout

- New functions only, read by `RoutingService` and the CLI; `xrayctl gate plan` is expected L0 (startup and
  control plane). Rollout with Sidecar spec018 in xray-config spec022 (hq).

## Rollback

- `switch revert`; an older Core leaves the fields empty and the Sidecar shows them as unavailable.

## 2026-11-08 amendment (P6–P9)

### Code

- `app/router/command/command.proto`: `BalancerStatus.tracked_from` (4), `.reason` (5), `.provenance` (6); the `delay`
  comment says what P7 says. Regenerated with the protoc the file names.
- `features/routing/balancer.go`: `routing.BalancerStatus` gains `TrackedFrom`, `Reason`, `Provenance`.
- `app/router/member_status.go`:
  - `balancerStatus(status)` sets `Provenance`, copies `status.TrackedFrom`, and copies `status.LastErrorReason`
    only while `!status.Alive`. It reads the selection view (`GetObservation`), where `createResult` has already
    marked a refused relay outbound and a forced probe's followers dead with their reasons.
  - `statusReader.balancer` already clears `TrackedFrom` under the balancer's tag and keeps the bubbled status's
    `LastErrorReason`, which is P6 for member balancers and levels.
  - `GetStatus` without an observatory returns `Provenance` true and nothing else.
  - `balancerStatus` is called only by `members` and `GetStatus`, the report path; strategies read
    `memberObservatory`, which is unchanged.
- `app/router/command/command.go`: copies the three fields into both `BalancerMember.status` and `BalancerMsg.status`.
- `main/commands/all/api/balancer_info.go`: P9's `source` column, row and level reasons.

### Tests

- `app/router/picks_test.go`: FR-006–FR-008, with a fake observatory whose observation carries tracked copies (a
  refused one among them) and forced statuses, and a classic observatory error.
- `app/observatory/command/members_contract_test.go`: FR-009 on spec021's shape, a track group and `SetForceDown`;
  a default configuration.
- `proxy/hysteria/relay_session_test.go`: FR-009 with a denied relay outbound tracking its owner under a balancer,
  read through `RoutingService` on the started client.
- `main/commands/all/api/balancer_info_test.go`: FR-010 against the fake routing server, a Core without P6 included.

### Gate and rollout

- Report path only (cold for picks); `xrayctl gate plan` is expected to choose L0. Rollout with Sidecar spec020 S7 and
  xray-config spec027 phase 2, on the nodes' next Core release (spec024 T014's nodes first).

### Rollback

- `switch revert`; the older Core leaves `provenance` false, and the Sidecar and the Portal show provenance as
  unavailable or take it from `GetTrack` (xray-config spec027 phase 1).
