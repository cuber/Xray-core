# Tasks: Balancer Picks On The Control Plane

- [x] T001 spec, plan and tasks; index row in `spec/README.md`.
- [x] T002 P1–P4: candidate status, shares per strategy, strategy name; FR-001–FR-003.
- [x] T003 Proto and `RoutingService`; the CP-7 contract; FR-004.
- [x] T004 P5: `xray api bi`; FR-005.
- [x] T005 Full suite, race, format; `xrayctl build core`; `xrayctl gate plan` and its gate; spec001 inventory.
- [x] T006 Weights at their configured precision; `api bi` marks live candidates left out and drops the selects
  where shares are reported (user, 2026-11-06: "API bi 也一起改改").

## 2026-11-08 amendment (P6–P9)

- [x] T007 Amendment P6–P9 and FR-006–FR-010 in spec, plan and tasks; index row in `spec/README.md`. Drafted
  2026-11-08 and approved the same day (user: "根据你的建议来").
- [x] T008 P6–P8: `routing.BalancerStatus` fields, `balancerStatus()` and the level status; FR-006–FR-008.
- [x] T009 Proto fields and `RoutingService`; the CP-7 contracts; FR-009.
- [x] T010 P9: `xray api bi`; FR-010.
- [ ] T011 Full suite, race, format; `xrayctl build core`; `xrayctl gate plan` and its gate; spec001 inventory.
- [ ] T012 Rollout with Sidecar spec020 S7 (Core pin) and xray-config spec027 phase 2.

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
- 2026-11-09 T008–T010 (on `312ee32e`):
  - T008: `routing.BalancerStatus` gains `TrackedFrom`, `Reason` and `Provenance`. `balancerStatus()` copies the
    observation's `tracked_from`, and its `last_error_reason` only while dead, and sets `provenance` on every status,
    an unknown one included; `balancerView.status` gives `provenance` alone without an observation. The pick path and
    `memberObservatory` are unchanged.
    - `TestOutboundCandidatesCarryProvenance` (FR-006, FR-008 P7), over a fake observation as `createResult` leaves it,
      plus a classic probe error: a probed outbound alive 30 ms; an alive one with a stale error shows no reason; a
      tracked one alive 30 ms from its probe; a tracked relay down with its probe's 30 ms, `hysteria relay denied`; a
      forced probe down with its 40 ms, `force down: cdt`, and its follower the same from it; a burst probe dead by its
      measurement down with its 25 ms average and no reason; the classic error as its reason; an unobserved candidate
      unknown. Without an observatory every status is unknown and has `provenance`.
    - `TestBalancerStatusesCarryProvenance` (FR-007): the reason bubbles up from an override target, an outbound
      fallback, a balancer fallback and a level's first down candidate, through two levels; no level and no member
      row has `tracked_from`, an alive level reached through a tracked candidate included.
    - `TestReadingProvenanceChangesNoPick` (FR-008 P8): roundrobin and weightedLeastPing pick the same 24 in a row
      with both levels' members and statuses read before each pick; the observation compares equal afterwards; the
      shares sum to 1 over the live candidates and are 0 for the dead ones.
  - T009: `BalancerStatus.tracked_from` (4), `.reason` (5), `.provenance` (6) and the P7 `delay` comment
    (`command.proto`, protoc 34.1 as the file's header says); `balancerStatusMsg` fills both `BalancerMember.status` and
    `BalancerMsg.status`. CP-7 on started instances:
    - `TestBalancerMembersContract`, a default configuration: every status of the three levels has `provenance`, no
      `tracked_from` and no reason while alive.
    - `TestBalancerProvenanceContract`: spec021's shape with a-anytls tracking a-hy2, through `ListBalancers` and
      `GetBalancerInfo` alike. Alive: a-anytls `tracked_from` a-hy2. a-hy2 forced down: a-anytls down from a-hy2 with
      `force down: cdt quota 190/190 GiB`, level a down with that reason and no `tracked_from`, route alive. Every probe
      forced down: route down with its first down candidate's reason. The dead tracked candidate keeps its probe's delay;
      `GetOutboundStatus` still lists the four measured outbounds only and `GetTrack` the group as loaded.
    - `TestRelayProvenanceContract` (`proxy/hysteria/relay_session_test.go`): two started Hy2 instances; the client
      probes its session owner through the server and tracks it on three relay outbounds under a leastPing balancer.
      relay-a is alive from the owner with share 1, relay-c down from the owner with `hysteria relay denied`, relay-u
      down with `hysteria relay unknown`, all three with the owner's delay.
    - Mutation check: `balancerStatus()` without `tracked_from` fails `TestBalancerProvenanceContract` in every step.
  - T010: `xray api bi` prints `source` after `status` (`probe`, `track <probe>`, `balancer`, `-`), a dead candidate's
    reason at the end of its row and a down level's reason on its line. `TestBalancerInfoShowsWhereStatusesComeFrom`
    against the fake routing server: the rows, a Core without P6 printing `-` and no reason, and `-json` carrying
    `trackedFrom`, `reason`, `provenance` and a dead candidate's delay. `TestBalancerInfoListsCandidatesAndShares` has the
    new column; the test helper resets the package's `-json` flag, which a `-json` run otherwise left set for the
    `quicstats` tests that follow.
  - JSON names in `xray api bi -json` (and the Sidecar's protobuf reads): `trackedFrom`, `reason`, `provenance` on each
    `members[].status` and on `status`; empty and false values are omitted.
  - Suites: `go test ./... -count=1 -timeout 1h` with `resources/geoip.dat` and `geosite.dat`, on the tree with both this
    amendment and spec025's T002–T007 (the next commit): 111 packages ok, none failed (364 s). The run before it failed
    once in the untouched `app/proxyman/inbound` (`TestMultiListenBindFailureReleasesEarlierAddress` takes a free TCP port
    number and finds the same UDP port held, here by another package running at the same time); that package then passed
    alone five times and with `-count=3`. `-race -count=3` on `app/router`, `app/router/command`,
    `app/observatory/command`, `main/commands/all/api`, `proxy/hysteria`, `transport/internet/hysteria`,
    `app/proxyman/command`, `infra/conf` and `proxy/anytls` (`features/routing` has no tests): ok. `go vet` on them and
    `.github/check-gofmt.sh` (gofmt and gofumpt over the fork's files): clean.
  - T011 remains for `xrayctl build core`, `xrayctl gate plan` and its gate (not run here); the spec001 FR-006 row is
    updated with this change.
