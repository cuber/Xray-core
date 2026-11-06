# Tasks: Balancer Picks On The Control Plane

- [x] T001 spec, plan and tasks; index row in `spec/README.md`.
- [x] T002 P1–P4: candidate status, shares per strategy, strategy name; FR-001–FR-003.
- [x] T003 Proto and `RoutingService`; the CP-7 contract; FR-004.
- [x] T004 P5: `xray api bi`; FR-005.
- [x] T005 Full suite, race, format; `xrayctl build core`; `xrayctl gate plan` and its gate; spec001 inventory.
- [x] T006 Weights at their configured precision; `api bi` marks live candidates left out and drops the selects
  where shares are reported (user, 2026-11-06: "API bi 也一起改改").

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
- 2026-11-06 T005 (on `e121e906`; carried here with the next Core change): `build/xray-linux-amd64` `vcs.modified=false`,
  SHA-256 `dbd04526fafd82224de8b56ff1f30bc38289f4df4c585eef6e96c22b4ed824e2`. `xrayctl gate plan --node hq` (base hq's
  `bfc9266`): L0, every changed function cold. L0 smoke (hk.akile.lite server, la.akile.lite client, 60 s chaos, 30 s
  quiet): PASS; the node balancers carried 956 of 1262 MiB of the echo (the weights give 3/4), no TCP failure in the
  quiet phase, sing-box UDP 127/135 (its client restarted by the last kill), no panic, handlers and carriers at 0.
  hq runs `e121e90` with Sidecar spec018 `df438f0` since 2026-11-06: 209 ms interruption, acceptance PASS with
  `balancer_members`; the ASZ Sidecar's `/xray-debug?node=hq` gives all 11 balancers from Core, shares summing to 1
  (xray-config spec022). It also showed weights widened from float32 (0.10000000149011612), which T006 reports as
  configured. The spec001 row came with `e121e906`.
- 2026-11-06 T006: `reportedWeight` in `balancerView.members`; `TestWeightsAreReportedAsConfigured` (0.1 and 0.8 exact,
  shares 1/9 and 8/9); `TestBalancerInfoListsCandidatesAndShares` adds a live candidate outside the pool (`not
  picked`, weight 0.1 from a widened value) and checks the selects appear only for the older Core.
  xray-tui follows (xray-config `xray-tui`: Core's candidates replace the configuration's members, Pick and Weight
  columns, the level's status and an active fallback in the section title, the Routes view's selection from the
  shares).
