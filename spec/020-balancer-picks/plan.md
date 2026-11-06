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
