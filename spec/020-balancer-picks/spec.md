# Feature Specification: Balancer Picks On The Control Plane

- Feature ID: 020-balancer-picks
- Created: 2026-11-06
- Status: Approved (2026-11-06; user: "改下，然后确保 portal 能看到呀，如果能显示出 balancer 的 pick 就最佳，你看看行不行")
- Baseline: Core `bfc92664` (develop, spec019 and its gate fixture)
- Origin (user, 2026-11-06): after hq ran spec019's node balancers, the Portal's Balancers tab showed only the
  configured selectors: the route's members, their bubbled status and what each level picks were visible only
  through `xray api bi -json`, whose text form printed the first balancer's pool alone.
- Consumers: [Sidecar spec018](../../../xray-sidecar/spec/018-balancers-from-core/spec.md) (`/xray-debug`
  `balancers` from this service) and the Portal through it; rollout in
  [xray-config spec022](../../../xray-config/spec/022-balancer-picks-portal/spec.md).

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
    round robin gives exactly these fractions), 0 outside the pool; `weight` carries the weight;
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
  status, delay, weight, share); `-json` prints the response as it is. With a Core before this spec, the text form
  prints what the response has.

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | P1: candidate statuses for outbounds (alive, down, unobserved) and member balancers (bubbled) | unit |
| FR-002 | P2: shares for every strategy, with an override on and off a candidate and an active fallback; weightedLeastPing's shares equal the fractions its picks take over many picks | unit |
| FR-003 | P4: reading shares leaves the next picks of roundrobin and weightedLeastPing unchanged | unit |
| FR-004 | P1–P3 through a real gRPC server on spec021's shape, with a member's protocol and a whole member forced down | started-instance CP-7 test |
| FR-005 | P5: `api bi` lists all, several tags, and the text rows | CLI test |

## Control Plane

- **Runtime state (CP-1, CP-2).** Candidate status, share, weight and the strategy name, read only through
  `RoutingService` (`ListBalancers`, `GetBalancerInfo`), computed at read time.
- **Compatibility (CP-6).** Additive proto fields. A consumer reads a candidate without `status` as a Core before
  this spec and shows shares as unavailable.
- **Consumers (CP-8).** Sidecar spec018 puts them in `/xray-debug`; the Portal's Balancers tab shows each balancer
  as a row with its candidates (status, delay, share) and links a member to its own row, still a flat list
  (spec019 M10). Acceptance: xray-config spec022 checks hq's section after deployment. xray-tui keeps reading the
  configuration (non-goal for now, its own Core pin).

## Non-goals (2026-11-06)

- Counting the picks made (that would add work to every pick; the shares are exact for the deterministic
  strategies, and expected values for random and leastLoad).
- A tree view; weights that pass through a member (spec019).
