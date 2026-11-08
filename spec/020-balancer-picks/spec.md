# Feature Specification: Balancer Picks On The Control Plane

- Feature ID: 020-balancer-picks
- Created: 2026-11-06
- Status: Approved (2026-11-06; user: "改下，然后确保 portal 能看到呀，如果能显示出 balancer 的 pick 就最佳，你看看行不行").
  P1–P5 implemented 2026-11-06. 2026-11-08 amendment (P6–P9, FR-006–FR-010): approved 2026-11-08 (user: "根据你的建议来");
  implemented 2026-11-09 (T008–T010); the build, its gate and the rollout are pending (T011, T012).
- Baseline: Core `bfc92664` (develop, spec019 and its gate fixture); the amendment: `312ee32e` (spec024 T013).
- Origin (user, 2026-11-06): after hq ran spec019's node balancers, the Portal's Balancers tab showed only the
  configured selectors: the route's members, their bubbled status and what each level picks were visible only
  through `xray api bi -json`, whose text form printed the first balancer's pool alone.
- Origin of the amendment (2026-11-08): once spec024's relay outbounds ran under `track` on tgz.lite, a candidate's
  status no longer said where it came from. `balancerStatus()` (`app/router/member_status.go`) keeps only known, alive
  and delay, and drops the observation's `tracked_from` and `last_error_reason`, so `xray api bi -json` shows a tracked
  candidate's copied delay exactly like a measured one, and a relay outbound the server refuses as `down` without why.
- Consumers: [Sidecar spec018](../../../xray-sidecar/spec/018-balancers-from-core/spec.md) (`/xray-debug`
  `balancers` from this service) and the Portal through it; rollout in
  [xray-config spec022](../../../xray-config/spec/022-balancer-picks-portal/spec.md). The amendment:
  [Sidecar spec020](../../../xray-sidecar/spec/020-hy2-relay-session/spec.md) S7 and the Portal in
  [xray-config spec027](../../../xray-config/spec/027-portal-relay-track/spec.md).

## Problem and Goal

Spec019 reports a balancer's members and its own bubbled status (M10). A reader still cannot see, per level, how
each candidate stands as that level reads it, nor where the level's new connections go. `principleTarget` is the
pick only for leastPing; for weightedLeastPing it is the healthy pool, whose members take unequal shares.

Goal: each candidate of a level carries its status as the level reads it and the share of the level's new picks it
takes now, computed at read time from the same published map and observation the picks use. No pick path changes.

## Contract

- **P1 Candidate status.** Each `BalancerMember` (spec019 M10) carries `status`: an outbound candidate's observation
  (known, alive, delay), a member balancer's M4 status, unknown (`known` false) when not observed. It is what the
  level's strategy reads: the memberObservatory view of spec019 M5.
- **P2 Share.** Each `BalancerMember` carries `share`, 0 to 1, the fraction of the level's new picks the candidate
  takes with the observation at read time:
  - leastPing: 1 for the candidate it picks;
  - weightedLeastPing: its weight over the pool's total for a candidate in the healthy pool (its smooth weighted
    round robin gives exactly these fractions), 0 outside the pool; `weight` carries the weight at the float32
    precision the configuration has (0.1, not 0.10000000149011612);
  - roundrobin and random: equal over the candidates they pick from (not reported down);
  - leastLoad: equal over the nodes it selects;
  - an override target among the candidates takes 1 and the others 0; an override naming another outbound, and
    an active fallback, leave every share 0 (`override`, `fallback.active` say where picks go).
  The shares of a level sum to 1 or to 0. Every built-in strategy computes them.
- **P3 Strategy.** `BalancerMsg.strategy` names the level's strategy as loaded (`leastping`, `weightedleastping`,
  `roundrobin`, `random`, `leastload`).
- **P4 Read only.** Computing P1–P3 changes no strategy state: no round robin index, no die, no smooth weighted
  round robin current weight. The pick path (`PickRoute`, `resolveChain`, `PickOutbound`) is unchanged.
- **P5 CLI.** `xray api bi` without a tag lists every balancer (`ListBalancers`), with tags each one in turn; the
  text form prints the strategy, the level's status, override and fallback, and one row per candidate (kind,
  status, delay, weight, share; a live candidate the level leaves out now is `not picked`); the strategy's selects
  only where no shares are reported. `-json` prints the response as it is. With a Core before this spec, the text
  form prints what the response has.

### 2026-11-08 amendment: where a status comes from

Decision (user, 2026-11-08): "那还是保持 A 仍然只用 track 显式声明，这个逻辑清晰，本身 outbonds relay 和 ob track 就是两个东西，联系在一起反而不太灵活了".
A relay (spec024) is a property of an outbound: whose session it rides, its user, its binding. `track` (spec014) is a
property of the observatory: an outbound takes another outbound's probe result because the configuration says so.
They stay independent. A candidate's provenance comes from `track` alone, never from its relay session, and nothing
checks that a relay outbound tracks its own owner. The one existing link stays: a tracked relay outbound the server
refuses is dead with its reason (spec024 R6, `relayRefusal` in `app/observatory/burst/burstobserver.go`), which is the
relay's own health fact and already in the observation.

- **P6 Provenance.** `BalancerStatus` gains `tracked_from`, `reason` and `provenance`, read from the same observation
  P1 reads:
  - `provenance` is true on every status a Core with this amendment reports, `known` or not, and with or without an
    observatory. An older Core leaves it false; its `tracked_from` and `reason` are then empty and say nothing.
  - An outbound candidate:
    - `tracked_from` is its observation's `tracked_from`: the probe outbound whose result it carries through `track`
      (spec014). Empty when it is probed itself, or not observed.
    - `reason` is its observation's `last_error_reason` while it is dead, and empty while alive:
      - `hysteria relay unknown`, `hysteria relay denied` or `hysteria relay unsupported` for a relay outbound the
        server refuses (spec024 R6);
      - `force down: <reason>` for a forced probe and every outbound that tracks it (spec015);
      - the classic observatory's probe error.

      A burst probe dead by its measurement records no error text, so its `reason` is empty; its sample counts are in
      `GetOutboundStatus` (`health_ping`).
  - A member balancer candidate, and a level's own status (`BalancerMsg.status`, spec019 M4): `tracked_from` is empty,
    because the status is the balancer's own, reported under its tag as today. `reason` is the reason of the status M4
    bubbles up while it is dead: its override target's, its fallback's, or its first down candidate's. Where that status
    came from is read on the member's own row, through its candidates (spec019 M10 keeps the list flat).
- **P7 Delay of a dead status.** `delay` stays what the observation has. A dead outbound candidate can carry a non-zero
  delay: a refused relay outbound keeps its probe's, a recovering probe its recent average. The `BalancerStatus.delay`
  comment ("0 when not alive") is wrong for those and is corrected. No value changes. Consumers show no delay for a dead
  status; `xray api bi` already prints `down`.
- **P8 Read only.** As P4: reading P6 changes no strategy state, no observation and no pick. `GetObservation`,
  `GetOutboundStatus` and `GetTrack` are unchanged.
- **P9 CLI.** `xray api bi` text rows gain a `source` column after `status`:
  - `probe` for an observed outbound candidate without `tracked_from`;
  - `track <probe>` for one with it;
  - `balancer` for a member balancer;
  - `-` for an unobserved candidate, and for every candidate of a Core without P6.

  A dead candidate with a `reason` ends its row with it, and the level line adds its own reason when it is down.
  `-json` prints the new fields as the response has them, so it needs no change.

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | P1: candidate statuses for outbounds (alive, down, unobserved) and member balancers (bubbled) | unit |
| FR-002 | P2: shares for every strategy, with an override on and off a candidate and an active fallback; weightedLeastPing's shares equal the fractions its picks take over many picks | unit |
| FR-003 | P4: reading shares leaves the next picks of roundrobin and weightedLeastPing unchanged | unit |
| FR-004 | P1–P3 through a real gRPC server on spec021's shape, with a member's protocol and a whole member forced down | started-instance CP-7 test |
| FR-005 | P5: `api bi` lists all, several tags, and the text rows | CLI test |
| FR-006 | P6 on outbound candidates (2026-11-08): a probed outbound (no `tracked_from`); a tracked outbound alive with its probe's delay and `tracked_from` the probe; a tracked relay outbound the server denies (dead, `hysteria relay denied`, `tracked_from` its probe); a forced probe and an outbound that tracks it (`force down: <reason>`); an unobserved candidate; `provenance` on every status, and without an observatory | unit |
| FR-007 | P6 on member balancers and the level status (2026-11-08): no `tracked_from`; while dead, the reason of the status M4 bubbles up (override target, fallback, first down candidate) | unit |
| FR-008 | P7, P8 (2026-11-08): delays as the observation has them, a dead candidate's included; shares and the next picks of roundrobin and weightedLeastPing unchanged after a read; `GetObservation`, `GetOutboundStatus` and `GetTrack` unchanged | unit |
| FR-009 | P6 through a real gRPC server on a started instance (2026-11-08): a default configuration (a balancer over probed outbounds: `provenance` true, no `tracked_from`); spec021's shape with a track group and a forced probe, through `ListBalancers` and `GetBalancerInfo`; two started Hy2 instances where a denied relay outbound tracks its owner under a balancer (`hysteria relay denied`, `tracked_from` the owner) | started-instance CP-7 tests |
| FR-010 | P9 (2026-11-08): the `source` column, row and level reasons; a Core without P6 prints `-`; `-json` carries the fields | CLI test |

## Control Plane

- **Runtime state (CP-1, CP-2).** Candidate status, share, weight and the strategy name, read only through
  `RoutingService` (`ListBalancers`, `GetBalancerInfo`), computed at read time.
- **Compatibility (CP-6).** Additive proto fields. A consumer reads a candidate without `status` as a Core before
  this spec and shows shares as unavailable.
- **Consumers (CP-8).** Sidecar spec018 puts them in `/xray-debug`; the Portal's Balancers tab shows each balancer
  as a row with its candidates (status, delay, share) and links a member to its own row, still a flat list
  (spec019 M10). Acceptance: xray-config spec022 checks hq's section after deployment. xray-tui shows the same
  candidates, statuses and shares (its Core pin raised to this spec's T006 commit; user, 2026-11-06: "xray-tui 也一起改下").
- **2026-11-08 amendment (P6–P9).**
  - Runtime state (CP-1, CP-2): `tracked_from`, `reason` and `provenance` are a read of the observation the level
    already reads, through the same `RoutingService` RPCs. No new RPC, no `api.services` change.
  - Read only and bounded (CP-3, CP-5): no side effects. `reason` carries only texts that `GetOutboundStatus` and
    `GetTrack` already return; a relay reason names the binding status, never a credential.
  - Compatibility (CP-6): additive fields (`BalancerStatus` 4–6). An older Core leaves `provenance` false; consumers
    then show provenance as unavailable and never infer it from configuration.
  - Tests (CP-7): FR-009.
  - Consumers (CP-8):
    - Sidecar spec020 S7 passes `tracked_from` and `reason` through on each candidate's and level's status, only when
      `provenance` is true.
    - The Portal (xray-config spec027) shows each candidate's effective state with its source (`probe`,
      `track ← <probe>`, a member balancer's link), a dead candidate's reason, and no measured-looking delay for a
      tracked one. Track links take the effective state's colour.
    - xray-tui: dated non-goal (2026-11-08). `xray api bi` and its `-json` show the fields in a terminal; xray-tui would
      need its Core pin raised and its own view. Revisit when an operator asks for it there.
    - Acceptance: no new item in this amendment. xray-config spec027 leaves open whether its `balancer_members`
      acceptance item checks provenance.

## Non-goals (2026-11-06)

- Counting the picks made (that would add work to every pick; the shares are exact for the deterministic
  strategies, and expected values for random and leastLoad).
- A tree view; weights that pass through a member (spec019).

## Non-goals (2026-11-08 amendment)

- Deriving provenance from a relay session, or any check that couples relay and track (user decision above).
- Naming the candidate that a balancer's status bubbles up from: the member's own row shows its candidates and their
  provenance. Revisit if reading down the rows proves too slow.
- An error text for a burst probe's failed measurement (the burst observatory records counts, not errors).
- Per-relay counters (spec024 R4 counts relay traffic under each relay outbound's own tag).
