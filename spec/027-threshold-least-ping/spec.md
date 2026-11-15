# thresholdLeastPing

- Spec ID: 027-threshold-least-ping (formerly `027-least-ping-max-rtt`; renamed 2026-11-15)
- Promoted from Core draft005 on 2026-11-14; draft005 remains reserved in the draft index.
- Created: 2026-11-14. Rewritten 2026-11-15.
- Status: Approved specification. Implementation has not started. Approval alone does not authorize a build,
  release, push or deployment.
- Inspected baseline: `291fde47` on the active append-only fork line (`10da0092` plus the spec027 commit).
- Origin: the operator wants a reachable but slow primary path to yield to a backup, while still preferring
  the fastest path. The first consumer is xray-config TODO-053 (RTT-limited tiered backup on HK relays).
- [Implementation plan](plan.md), [verification matrix](tests.md), [tasks](tasks.md),
  [design checklist](checklists/requirements.md).

## Revision History

- 2026-11-14: approved as an opt-in `maxRTT` setting on `leastPing`, evaluated when a connection is routed.
- 2026-11-15 amendment: `recoverRTT` hysteresis; every outbound such a balancer can choose must be observed.
- 2026-11-15 rewrite (operator): a **new strategy type `thresholdLeastPing`** instead of a leastPing setting,
  and an **observation-driven** model: an observation change triggers the computation, results bubble up to
  the balancers that depend on them, and routing only reads the published decision. leastPing is unchanged.
  The decision rules, hysteresis, terminal behaviour, member means and the observed-outbound check carry
  over from the earlier text; the evaluation time, configuration surface and control-plane shape change.
- 2026-11-15 completion: requirements and cases lost in the rewrite restored against `291fde47`; new
  triggers (Hy2 relay status, runtime outbound changes), the leastPing guard, drop accounting and the
  precision rule added; every decision consolidated as D1–D16, none open.

## Current Evidence

Source observations at the baseline, not acceptance of the future implementation:

- `strategy_leastping.go` picks the lowest `Delay` among matching alive observations, reading the
  observatory on every pick. It has no settings; equal delays keep observation order.
- A strategy receives only its candidates; `fallbackTag` lives on `Balancer`. When the strategy returns
  nothing, `Balancer.pick` returns the fallbackTag **without looking at its health**; with no fallbackTag
  the dispatcher falls through to the **default outbound** (`app/dispatcher/default.go`).
- An unknown strategy type is a hard error in both `infra/conf/router.go` (`unknown balancing strategy`)
  and `app/router/config.go` (`unrecognized balancer type`), so an older Core refuses a configuration
  that uses a new type instead of ignoring it.
- The observatory has no change notification. `GetObservation` builds the result on each call; burst
  results are recorded in `HealthPing.putResultLocked`, removed in `cleanupLocked`, and force-down
  (spec015) is applied at read time from `Observer.force`. Classic results are recorded in
  `updateStatusForResult`.
- Burst `HealthPingMeasurementResult.average` is in nanoseconds; `Delay` is that average truncated to
  milliseconds. Samples older than `2 × sampling × interval` are ignored.
- `balancerMsg` (`app/router/command/command.go`) reads one balancer map but calls `GetObservation`
  separately for principle target, members (and shares), own status and fallback.
- `checkMemberObservation` (`app/router/loops.go`, spec019 M6) already rejects an unobserved outbound
  candidate or outbound fallback for balancers with members and the balancers they reach, exempting an
  actual blackhole, at start, on every rule publication and on a runtime `AddOutbound` (through the
  outbound manager's `checkLoops`). `RemoveHandler` runs no such check.
- Burst `createResult` marks a tracked Hy2 relay outbound dead when its relay status is `unknown`, `denied`
  or `unsupported` (spec024), read at `GetObservation` time, independently of probe results.
- Both observatories' `GetObservation` currently never return an error.
- leastPing's settings load as an empty config, so a `maxRTT` written there is silently ignored.
- The dispatcher logs every `ErrRouteDropped` connection as a warning (`route dropped for`).
- leastLoad and weightedLeastPing already have a `maxRTT` setting with their own meaning (a hard filter
  and a healthy-pool limit); this strategy's preferred-limit meaning does not change theirs.

## Goal And Non-goals

Goal: a `thresholdLeastPing` balancer prefers primary candidates below its RTT limit, otherwise an
under-limit fallback, and finally the lowest-RTT usable option across its candidates and its one fallback
result; it never sends a connection to an option known to be unusable and never escapes to the default
outbound. Its decision is computed when observations change and is identical for routing and for the
control plane.

Non-goals: any change to leastPing, weightedLeastPing, leastLoad, roundRobin or random picks, weights or
fallback; the probe schedule, timeout or retry; global `Alive`, track semantics or force-down recovery;
closing or retrying existing connections; end-to-end exit detection; minimum-sample or failure-rate
settings; a ranking tolerance that switches only when another option is faster by a margin (D16). Fleet names, addresses and credentials stay in the configuration repository.

## Configuration

Illustrative native JSON (the values are examples, not defaults):

```json
{
  "tag": "service-balancer",
  "selector": ["service-path-a-hy2", "service-path-a-vless"],
  "strategy": {
    "type": "thresholdLeastPing",
    "settings": {"maxRTT": "50ms", "recoverRTT": "40ms"}
  },
  "fallbackTag": "service-backup-balancer"
}
```

## Requirements

### TL-1 Configuration

- `type` is matched case-insensitively like the other strategies (`thresholdleastping`).
- `settings.maxRTT` is **required** and positive. `settings.recoverRTT` is optional; it must satisfy
  `0 < recoverRTT <= maxRTT`; omitted means `recoverRTT = maxRTT` (no band).
- Both use the existing duration parser: strings with units only. Missing maxRTT, zero, negative,
  malformed, overflowing, null, bare numbers and wrongly typed values are configuration errors naming the
  field, before any listener starts. The same rules apply to a direct protobuf `BalancingRule` and to
  balancers published through AddRule.
- Typed settings: a new `StrategyThresholdLeastPingConfig` message; no other strategy's message is reused.
- `fallbackTag` is optional. Member balancers (`balancers`, spec019) are allowed, with spec019's own
  requirement that a balancer with members has a fallback. All graph and loop checks (spec017–019) apply.
- TL-7's observed-outbound check applies to every thresholdLeastPing balancer.

### TL-2 Measurement And Eligibility

An option is one candidate outbound, one member balancer, or the one fallback result. For each option:

1. A burst observation with at least one successful sample uses `HealthPing.Average` in nanoseconds.
2. A classic observation without `HealthPing` uses `Delay` in milliseconds.
3. A member or fallback balancer uses the result it publishes or bubbles (TL-6).
4. A present but empty or invalid measurement is never replaced by a fabricated zero. Negative RTTs and
   inconsistent sample counts are invalid. A real zero or sub-millisecond success is valid.

**Usable** means known, alive (after track and force-down, as `GetObservation` reports it) and validly
measured. **Within-limit** means usable and not latched over the limit after TL-4's transition. Without a
band this is `rtt < maxRTT`; equality is over the limit.

**Precision (D13):**

- *Threshold and latch* comparisons (`maxRTT`, `recoverRTT`) use the exact duration: the burst average in
  nanoseconds, a member mean computed from its members' nanosecond averages, a classic `Delay` converted
  exactly. For whole-millisecond limits this gives the same result as comparing the truncated `Delay`
  (`floor(avg) < L` exactly when `avg < L`), and it needs no rounding rule for fractional limits or means.
- *Ranking* (lowest RTT in every phase, ties, the degraded comparison) uses whole milliseconds, truncated
  as `Delay` is, like leastPing. Equal milliseconds are ties: observation order among candidates, a
  candidate before the fallback. Sub-millisecond differences between 25-sample averages are noise;
  ranking on them would move new connections between near-equal paths on almost every pass.

The burst window average is the only RTT used; the latest, minimum and maximum samples are never used.

Missing, failed, forced-down and refused options are never usable, in any phase. Their raw health, error
and track provenance are reported unchanged.

### TL-3 Decision

For each balancer, in order:

- **Phase 1 (primary):** if any candidate is within-limit, choose the lowest RTT among them, even when the
  fallback is faster. Ties keep observation order.
- **Phase 2 (fallback):** otherwise, if the fallback result is within this balancer's limit, choose it.
- **Phase 3 (degraded):** otherwise, if any option is usable, choose the lowest RTT across the usable
  candidates and the usable fallback result; on equal RTT a candidate wins over the fallback. Mode
  `degraded`, never reported as within-limit. Over-limit options may take traffic here; unusable ones may not.
- **Terminal:** if nothing is usable and `fallbackTag` names an actual blackhole outbound, choose it
  (mode `unavailable`, fallback active). Otherwise **drop** the connection (mode `unavailable`, no choice).
  This includes no fallback and an unusable fallback. Never the default outbound, never an implicit
  blackhole, never a handler assumed from its name, never a search inside an unavailable child.
- **Pending:** if nothing is usable and at least one existing, observed option has not had its first
  result since start, publication or its addition, the mode is `pending` instead of `unavailable`; a
  connection takes the terminal action above. A fallbackTag whose outbound was removed is unusable, not
  pending. Pending is never reported as healthy.

A blackhole is a terminal action, not a zero-RTT option. Choosing the fallback (Phase 2, degraded or
terminal blackhole) sets fallback active, every candidate share to 0 and the principle target empty; a
chosen candidate, degraded or not, has share 1, is the principle target, and fallback is inactive. An
unusable fallback never receives a connection. A pick never waits or queues while the mode is pending.

**What a thresholdLeastPing balancer reports to the level above** (its bubbled status):

| Own mode | Reported status |
|---|---|
| primary, fallback, degraded | the chosen option's status: alive, with that option's RTT |
| override | the override target's own observation; the parent applies its own limit to it |
| pending | unknown |
| unavailable (drop or blackhole) | dead, with the reason of the decision |

### TL-4 Hysteresis

- Each balancer keeps one latch per option. For an option with a usable measurement `r`: not latched and
  `r >= maxRTT` latches it; latched and `r < recoverRTT` releases it. Without a band this is exactly
  TL-2's single comparison.
- An option without a usable measurement keeps its latch unchanged; it is excluded regardless.
- Latches change **only in a computation** (TL-5), never in a pick or a read. They therefore follow the
  observation sequence and do not depend on traffic or on how often the control plane reads.
- A balancer created by rule publication starts with every latch released; an option's first usable
  measurement is compared with maxRTT.
- The band affects Phase 1 and Phase 2 only. Phase 3 ranks usable options by RTT alone.
- Latches are local: the same outbound can be latched in one balancer and not in another. A parent latches
  a child's published result as one option; it never reads or writes the child's latches. An override
  neither consults nor changes latches.

### TL-5 Observation-driven Computation

- **Triggers.** A computation runs after each change that can affect a decision: a burst probe result
  recorded or removed, a classic result recorded, a force-down set change, a Hy2 relay outbound's relay
  status change (spec024; it turns a tracked relay dead in `GetObservation` without any probe result,
  D12), a runtime `AddOutbound`/`RemoveOutbound` (it changes selector-matched candidates), and router
  changes (start, rule publication through ReloadRules/AddRule/RemoveRule, override set or clear). The
  observatory and the relay outbound expose a change notification for this; it is invoked **after** the
  notifier releases its own locks.
- **One pass, one observation.** A computation reads one observation and the current balancer map, then
  evaluates every thresholdLeastPing balancer and the balancers they depend on, children before parents
  (the graph is acyclic by spec017–019). The result is one immutable **pass**: a generation number, the
  observation time, and every thresholdLeastPing balancer's decision, per-option selection and latches.
- **Publication.** The pass is published atomically and every balancer of the map refers to it. A pick and
  a control-plane read each use one pass for everything they report: a pick resolving a fallback chain
  reads one pass for every thresholdLeastPing level, and one `ListBalancers` response uses one pass for
  every balancer it lists.
- **The observation is read-only.** A computation never modifies the statuses it reads. An observation of
  an unexpected type is treated as every option unknown, with that reason in the report.
- **Bounded state.** A pass holds one decision and one latch set per thresholdLeastPing balancer, sized by
  the loaded graph. No per-connection state, cache or history beyond the latches.
- **Rule publication.** A new balancer map gets its first pass computed **before** the map is published,
  so a pick never sees a new map without a decision.
- **Coalescing.** Computations are serialized. A trigger that arrives during a computation marks it dirty;
  the running computation repeats once more with a fresh observation. No goroutine, timer or queue is
  added; the number of passes is bounded by the triggers.
- **Pick.** A thresholdLeastPing pick reads the published decision for its tag (an override still wins
  first). It performs no observatory read and no computation.
- **Clock.** The probe scheduler is the clock: burst results arrive every interval for each probed
  outbound, so sample expiry is reflected at the next result. If an observatory stops producing results
  entirely, the last pass stays published; its observation time is reported so consumers can see it.
- **Lifecycle.** Router start computes the first pass before the first connection is routed (usually
  pending until the observatory's immediate first checks return). Close cancels every subscription; no
  callback runs after Close returns.
- **Order.** The children-first order uses only the existing member and fallback edges; comparing a
  parent's options with a child's result adds no edge to the graph.

### TL-6 Nesting

- A balancer looks only at its direct options: its candidate outbounds' observations, its member
  balancers' results and its fallback's result. It never enumerates a child's candidates, never searches a
  child's fallback and never chooses a descendant.
- A thresholdLeastPing child's result is its own published decision, reported as in TL-3's table (the
  chosen option, the override target, unknown while pending, or dead). Legacy parents read that same
  result where they read a member's bubbled status today.
- A legacy child's result is what it bubbles today (spec019 M4), computed on the pass's observation. For a
  thresholdLeastPing parent only, a roundRobin or random child contributes the arithmetic mean of its
  dispatchable, usable, measured members' RTTs, equally per member, with measured and dispatchable counts.
  Without any measured usable member its RTT is unknown and its own fallback outcome applies.
- A member mean is used wherever this parent compares RTT, including the degraded comparison; it is
  never replaced by the child's legacy minimum-member status. A child that is both a candidate of one
  parent and the fallback of another is evaluated by the same rules in both places.
- Routing a chosen child still goes through normal chain resolution to the child's own choice; a roundRobin
  or random child picks once, only when a connection reaches it. A computation or read never advances a
  stateful strategy, and never reports which member such a child would pick next: it reports the mean and
  its coverage, and a concrete target only for a deterministic child.

### TL-7 Observed Outbounds

- For every thresholdLeastPing balancer, and every balancer reached from it through members or
  fallbackTag, each selector-matched outbound and an outbound fallbackTag must be observed (classic or
  burst; a track follower counts). Only an actual blackhole fallback is exempt. A fallbackTag naming no
  outbound and no balancer is an error. No observatory at all is the same error.
- This extends `checkMemberObservation` (spec019 M6) with thresholdLeastPing balancers as additional
  roots, wherever M6 runs: at start, on every rule publication (ReloadRules, AddRule, RemoveRule) and on a
  runtime `AddOutbound` (through the manager's loop check). A failing update keeps the previous state.
- A runtime `RemoveOutbound` is not checked, as today for every balancer (D15): a removed candidate or
  fallback outbound becomes unknown at the next computation and is never usable.
- leastPing and every other strategy are not affected; an unobserved fallback of theirs keeps today's
  unconditional behaviour.

### TL-8 Locality, Track, Overrides And Flows

- Eligibility belongs to the balancer; no computation writes back to an `OutboundStatus` or global alive.
- Track supplies the same observation and provenance as today; no per-user probes, no health inferred from
  names, relays or downstream paths.
- `OverrideBalancerTarget` keeps its precedence over the automatic decision, even for an over-limit,
  unknown or down target. The report shows mode `override` and the target's true state. Graph, loop and
  authentication checks are not bypassed.
- Only new connections follow a changed decision. Existing TCP, UDP, Hy2, VLESS/Mux and AnyTLS flows are
  not closed or retried.

### TL-9 leastPing Settings Guard (D11)

- A `leastPing` balancer whose settings contain `maxRTT` or `recoverRTT` is a configuration error whose
  message names `thresholdLeastPing`. This catches the earlier draft's syntax, which would otherwise load
  and silently do nothing. No other leastPing setting, ignored or not, changes behaviour.

## Control Plane (CP-1–CP-8)

Owner: the existing `RoutingService` (`GetBalancerInfo`, `ListBalancers`). No new `api.services` entry.

- **Capability signal:** `BalancerMsg.strategy` (spec020 P3) reports `thresholdleastping`. An older Core
  cannot load such a balancer at all (TL-1), so there is no supported-but-disabled state to distinguish.
- `BalancerMsg.threshold_policy` (free field 11): `max_rtt_ns`, `recover_rtt_ns` as loaded.
- `BalancerMsg.threshold_decision` (free field 12): mode (`primary`, `fallback`, `degraded`, `pending`,
  `unavailable`, `override`), `choice_tag`, terminal outcome (blackhole tag or drop), the fallback result's
  status and selection, the pass generation and the observation time (unix milliseconds).
- `BalancerMember.selection` (free field 6): `usable`, `within_limit`, `latched`, `rtt_known`, `rtt_ns`,
  reason, and for a roundRobin/random member its aggregation kind with measured/dispatchable counts.
  Reasons: `eligible`, `unobserved` (an observed tag without a result yet), `probe_down`, `rtt_unavailable`,
  `invalid_measurement`, `max_rtt_exceeded`, `recovering` (latched, below maxRTT, not yet below recoverRTT).
- Raw `status.alive`, `status.delay`, error and `tracked_from` stay raw facts. An alive option may be
  over-limit; a degraded winner has share 1 with `within_limit = false`; an eligible loser has share 0 and
  is not unhealthy.
- **One pass per response:** for a thresholdLeastPing balancer, members, own status, shares, principle
  target, fallback active, decision and the chain hops that are thresholdLeastPing balancers all come from
  the same pass; a `ListBalancers` response uses one pass for all its balancers. Chain hops that are legacy
  balancers keep their legacy reads (they report their own level). Existing `override`, `fallback`, chain,
  share and status fields keep their meaning.
- **Drops (D14):** `threshold_decision` carries a per-balancer counter of connections this balancer
  dropped since it was published (a rule publication starts a new count) and the reason of the last drop. A drop is logged at info level, not as a warning,
  so an outage does not flood journald (which keeps warnings and errors).
- A read never computes, picks, advances a strategy, sends probes or holds locks across I/O. Response size
  is bounded by the loaded graph. No credentials, URLs or endpoints are returned.
- `xray api bi` reports the same fields; text output distinguishes reachable from over-limit and shows the
  observation time.

Consumers (separate owners; not authorized by this spec): Sidecar's `/xray-debug.balancers` passes the
fields through (Sidecar spec018); Portal's Balancers view (Config spec022) shows the limit, band, RTT,
eligibility, latch, degraded, pending, terminal, fallback, override and observation age, and tells a member mean apart from a
deterministic choice's RTT. Neither reads node configuration to reconstruct a value. xray-tui is a dated non-goal (2026-11-15) but must keep decoding.

## Compatibility And Release

- leastPing and every other strategy keep their behaviour and wire fields; existing messages are not
  renumbered or reused.
- A configuration using `thresholdLeastPing` fails `run -test` on an older Core. Rollout still verifies the
  loaded policy through the RPC after activation.
- Restoring a node means restoring the binary/config pair (`switch revert`); a binary-only downgrade fails
  to load the configuration rather than silently changing behaviour.
- The observatory notification and the router computation are additive; with no thresholdLeastPing
  balancer configured, no computation runs and picks of every other strategy are unchanged.
- Implementation follows the fork-line, format, build and control-plane rules in `spec/README.md`.

### Rollout Requirements (owned by xray-config; cases X01–X05)

1. The new Core first runs fleet-wide or on the target nodes with **unchanged** configurations and passes
   the default regression before any configuration uses `thresholdLeastPing`.
2. Capability: the staged candidate's `run -test` accepts the configuration and the installed Core
   rejects it; after activation the RPC reads back the exact maxRTT and recoverRTT of every such balancer.
3. Readiness: after activation the affected business balancers leave `pending` and carry traffic within
   the existing interruption budget; an admin path alone is not readiness.
4. Restore is the paired `switch revert`, verified; a binary-only downgrade is not a restore.
5. Evidence from another source, binary or configuration does not substitute for these checks.

## Verification And Acceptance

The [verification matrix](tests.md) defines every case; its IDs must map to executable tests or explicitly
owned consumer/rollout checks. A helper test, configuration check or build alone is insufficient. Tests use
synthetic identities and local endpoints.

| Contract | Cases | Evidence |
|---|---|---|
| TL-1 configuration | C01–C12 | Native JSON, direct protobuf and AddRule fixtures; exact loaded durations or focused errors; older-Core rejection on both paths |
| TL-2/TL-3 decision | Q01–Q26 | Literal observations into the pure decision function; independent expected tags, modes, shares, targets, terminal outcomes |
| TL-4 hysteresis | Y01–Y11 | Scripted observation sequences, one computation per step; literal latches and choices |
| TL-5 computation | E01–E21 | Real observatories and router: every trigger, coalescing, pre-publication pass, one observation per pass, children first, cost bound, re-entrancy, lifecycle, end-to-end real burst observer |
| TL-6 nesting/data plane | N01–N23 | Native dispatch to distinguishable local markers; child opacity and reported status, member means, no extra child picks |
| TL-7 observed outbounds | O01–O08 | Load-time rejection at start, on each publication path and on AddOutbound; RemoveOutbound handling; other strategies unaffected |
| TL-8 overrides/flows | H01–H08 | Override precedence with true state; track/force-down provenance; observatory recovery untouched; flows survive a decision change |
| TL-9 leastPing guard | C11 | Focused error; other leastPing settings unchanged |
| Control plane | R01–R16 | Real started Core over local gRPC; one pass per response and per listing; old/new decoding; drop counter; `xray api bi` |
| Rollout | X01–X05 | xray-config acceptance and switch tooling (owned there) |

Required execution, once tests exist (planned, not run):

```sh
go test ./infra/conf ./app/router/... ./app/observatory/...
go test -race ./app/router/... ./app/observatory/...
go test -race ./app/router/... ./app/observatory/... -run 'ThresholdLeastPing' -count=20
go test ./...
go vet ./...
bash .github/check-gofmt.sh
```

`-run` must match a nonzero number of tests; required cases may not be skipped into a pass. Tests that
need the network follow the repository's external-network setup, and a network failure is never recorded
as a pass. Native data-plane cases run the built Darwin binary; cross-compiling Linux is not a substitute.
Timing assertions use event synchronization and bounded deadlines, never fixed sleeps. Existing
strategy, member, fallback-chain, loop-guard, track, force-down, user-domain, protocol and RPC regressions
must still pass. After an authorized commit, the dual-platform build wrapper and the stability gate apply
before any production candidate. Record an evidence index (case IDs, test names, source and binary
identities, commands, exit codes, counts, skips) with PASS, FAIL or NOT RUN per stage.

## Decisions Confirmed By The Operator

Items D1–D6 were first confirmed on 2026-11-14 by the previous maintainer and re-confirmed as written on
2026-11-15 by the current operator, who took the project over. D7–D16 were confirmed on 2026-11-15.
No question is open.

| ID | Decision | Where |
|---|---|---|
| D1 | No default thresholds: `maxRTT` is configured explicitly on every balancer | TL-1 |
| D2 | Fallback is optional. With nothing usable: an explicit blackhole, otherwise drop; never the default outbound. This includes the pending window at start | TL-3 |
| D3 | Start reuses the observatory's immediate first checks; a pick never waits or queues | TL-3, TL-5 |
| D4 | A roundRobin/random child contributes the mean of its members, only to a thresholdLeastPing parent | TL-6 |
| D5 | Manual override keeps its precedence; the target's true state is reported | TL-8 |
| D6 | Runtime policy and decisions through `RoutingService`, consumed by Sidecar and Portal; xray-tui is a non-goal | Control Plane |
| D7 | Hysteresis through optional `recoverRTT`, omitted meaning no band; a latch survives an outage | TL-4 |
| D8 | Every outbound such a balancer can choose, candidates and fallback, is observed, enforced at load time | TL-7 |
| D9 | A new strategy type `thresholdLeastPing`; leastPing's behaviour is unchanged (apart from D11) | TL-1 |
| D10 | Observation changes trigger the computation; results bubble up to dependent balancers; each balancer looks one level down; only this strategy weighs its fallback | TL-5, TL-6 |
| D11 | leastPing rejects `maxRTT`/`recoverRTT` in its settings with an error naming thresholdLeastPing | TL-9 |
| D12 | A Hy2 relay status change triggers a computation | TL-5 |
| D13 | Exact durations for threshold and latch comparisons; whole milliseconds for ranking, ties by observation order and candidates before the fallback | TL-2 |
| D14 | Drops are counted per balancer in the RPC and logged at info level, not as warnings | Control Plane |
| D15 | Runtime `RemoveOutbound` stays unchecked; a removed option becomes unknown | TL-7 |
| D16 | No ranking tolerance ("switch only when faster by X"); ranking churn at millisecond boundaries is accepted | Non-goals |

## Evidence Status

Source inspection and documentation only. No feature code, protobuf generation, test, build, stress run or
deployment has been performed for this specification.
