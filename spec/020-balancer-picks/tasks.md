# Tasks: Balancer Picks On The Control Plane

- [x] T001 spec, plan and tasks; index row in `spec/README.md`.
- [x] T002 P1–P4: candidate status, shares per strategy, strategy name; FR-001–FR-003.
- [x] T003 Proto and `RoutingService`; the CP-7 contract; FR-004.
- [x] T004 P5: `xray api bi`; FR-005.
- [ ] T005 Full suite, race, format; `xrayctl build core`; `xrayctl gate plan` and its gate; spec001 inventory.

## Evidence

- 2026-11-06 T002–T004 (on `bfc92664`):
  - T002: `BalancingShares.PickShares` on every strategy; `balancerView.GetMembers` reads one observation and fills
    each candidate's status (an outbound's observation, a member's `statusReader.balancer`) and share (the
    strategy's, then an override), `GetStrategy` by the strategy's type. `TestCandidatesCarryStatusAndShare` (hq's
    shape: x unobserved, b's Hy2 down: `x:unknown w1:0 a[B]:50ms w2:0.667 b[B]:30ms w1:0.333`, inside a and b the
    faster live protocol at 1), `TestWeightedSharesMatchThePicks` (800 picks give exactly the shares),
    `TestSharesForEveryStrategy` (leastping, roundrobin, random, leastload; override on a candidate and elsewhere;
    every candidate down), `TestReadingSharesChangesNoPick` (roundrobin and weightedLeastPing pick the same 24 in a
    row with a read before each).
  - T003: `BalancerMember.status/weight/share`, `BalancerMsg.strategy` (command.proto, protoc 34.1);
    `TestBalancerMembersContract` adds the shares through a real gRPC server: weights 1, 2, 1 give 0.25, 0.50, 0.25;
    a's faster protocol forced down gives its other one 1; a whole member down gives the others 0.50 each;
    everything down gives every share 0. Found on the way: a weight `match` is a substring and the first match
    wins; the fixture's "a" had matched `x-main` (0.40, 0.40, 0.20 until x-main came first).
  - T004: `xray api bi` lists every balancer without a tag and each given tag in turn, with candidate rows
    (`TestBalancerInfoListsCandidatesAndShares` against a fake routing server, a Core before spec019 included).
  - `go test ./... -count=1`: 109 of 109 packages; `-race -count=3` on `app/router`, `app/router/command`,
    `app/observatory/command`, `main/commands/all/api`: pass; `gofmt` clean.
