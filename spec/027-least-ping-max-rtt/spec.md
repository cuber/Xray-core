# leastPing Maximum RTT

- Spec ID: 027-least-ping-max-rtt
- Promoted from Core draft005 on 2026-11-14; draft005 remains reserved in the draft index.
- Created: 2026-11-14
- Status: Approved specification; the operator confirmed clarification and requested promotion on 2026-11-14.
  Implementation has not started. Promotion alone does not authorize implementation execution, build,
  commit, release, push or deployment.
- Inspected baseline: `10da00924db6fe404e073502010e6992ee85d0bf` on the active append-only fork line.
- Origin: the operator wants a reachable but slow primary path to yield to a fallback, while retaining leastPing.
- Scope: leastPing only, with an optional strategy setting; no global change to observatory health.
- Amendment 2026-11-14: maxRTT is a preferred-quality limit, not an absolute ban. When no usable option
  meets it, choose leastPing across this level's usable selectors and its one opaque fallback result.
  A fallback balancer owns its own bubbled status and choice; the parent must not flatten its descendants.
- Clarification 2026-11-14: roundrobin/random children report a mean for the enabled parent's evaluation;
  startup uses the immediate initial probe, fallback is optional, blackhole must be explicit, manual
  overrides retain their existing priority, and no positive threshold is supplied by default.
- Amendment 2026-11-15 (operator): add hysteresis through an optional `recoverRTT` (LP-7), so an average
  hovering at the limit does not move new connections between exits; and require every outbound an
  enabled balancer can choose, its fallback included, to be observed, rejected at load time otherwise (LP-8).
  The operator confirmed the same day: omitted recoverRTT means no band; LP-8 covers selector outbounds
  as well as the fallback; fallback stays optional; a latch survives an outage.
- [Implementation proposal](plan.md), [verification matrix](tests.md), [tasks](tasks.md),
  [design checklist](checklists/requirements.md).

## Current Evidence

These are source observations, not acceptance of a future implementation:

- `app/router/strategy_leastping.go` chooses the lowest `Delay` among matching `Alive` observations.
  It has no settings or RTT limit. Equal delays keep observation order.
- `infra/conf/router_strategy.go` maps leastPing to `strategyEmptyConfig`; its unknown settings are ignored.
- `strategy_leastload.go` already has a maximum-delay filter and yields an empty pick when no candidate qualifies.
- `strategy_weightedleastping.go` has a different contract: an empty healthy pool can reopen the original pool,
  including an alive but slow candidate. Its spec016 fallback requires every candidate measured and none alive.
  This spec must not change that contract.
- `member_status.go` bubbles up the best alive candidate, not a strategy's RTT eligibility. Updating the picker
  alone would leave a threshold-limited child advertised as available on a rejected primary.
- `command/command.go` obtains targets, members, status and fallback through separate reads. New threshold
  explanations need a consistent snapshot, not several independently changing measurements.

## Goal And Non-goals

Goal: an opted-in leastPing balancer prefers primary candidates below its RTT limit, otherwise an
under-limit fallback, and finally the lowest-RTT usable option across its selectors and one fallback result.
Slow-but-reachable degradation must not revive a failed path. Its read-only explanation must agree with
its selection, while a fallback balancer remains an opaque ordinary node to the parent.

Non-goals: changing other strategies' picks, weights, provider priorities, the probe schedule, timeout/retry,
global `Alive`, track semantics, forced-down recovery, connection termination, end-to-end exit detection,
or adding minimum-sample or failure-rate settings. Hysteresis is in scope since the 2026-11-15 amendment (LP-7);
it applies to this balancer's RTT eligibility only, never to probe health. Fleet names, addresses and credentials
remain configuration-repository inputs; no production snippets belong in this public spec.

## Proposed Configuration

Not implemented; the following is an illustrative native JSON-shaped configuration, not deployment input:

```json
{
  "tag": "service-balancer",
  "selector": ["service-path-a-hy2", "service-path-a-vless"],
  "strategy": {
    "type": "leastPing",
    "settings": {"maxRTT": "50ms", "recoverRTT": "40ms"}
  },
  "fallbackTag": "service-backup-balancer"
}
```

The optional backup can be an observed outbound or another balancer. With a positive limit its reported result
is evaluated at this level as one option. Without a limit, legacy unconditional fallback behaviour stays.
A single observed backup does not need an extra balancer solely to obtain this threshold check.

**Confirmed placement:** use `strategy.settings.maxRTT`, like the existing strategy configuration pattern.
The earlier conversational top-level `maxRTT` was a sketch, not an existing field. This spec narrows that
sketch to leastPing rather than introducing a shared health policy for every strategy. There is no default
positive threshold: omission leaves the feature disabled, and a positive limit must be configured explicitly.
The example value is not an operational default. Actual fleet thresholds belong to a later rollout.

## Requirements

### LP-1 Opt-in And Validation

- Omitted settings, `{}`, omitted maxRTT and `"0s"` preserve current leastPing decisions and default behaviour.
  Existing protobuf `BalancingRule` values without `strategy_settings` remain valid and mean no limit.
- Positive values use the existing duration parser: strings with units only, not bare JSON numbers.
  Negative, malformed, overflowing, null and wrongly typed maxRTT values are configuration errors.
- A positive limit does not require `fallbackTag`, for JSON, direct protobuf or live rule publication.
  Omission skips the fallback phase; all-slow degradation then compares only usable primary candidates.
  Existing independent member/fallback graph requirements remain in force: this does not remove spec019's
  fallback requirement for a balancer that declares member balancers.
- A misplaced top-level `maxRTT` must produce a focused configuration error instead of appearing to work.
  Do not turn this into global rejection of all historically ignored configuration fields.
- `recoverRTT` (LP-7) uses the same duration parser. It is valid only with a positive maxRTT and must satisfy
  `0 < recoverRTT <= maxRTT`; omitted or `"0s"` means `recoverRTT = maxRTT` (no band). A positive recoverRTT
  without a positive maxRTT, or one above maxRTT, is a configuration error naming the field.
- With a positive maxRTT, every outbound the balancer can choose must be observed (LP-8); an unobserved
  candidate or fallback is a load-time error, not a runtime `unknown`.

### LP-2 Measurement And Eligibility

For a positive limit, distinguish usable (known, alive, valid measurement) from within-limit (usable and
below maxRTT). The same test applies to selectors and this level's one reported fallback result:

1. A burst `HealthPing` with at least one successful sample uses `Average` in native duration precision.
   This is the average of successful samples already produced by the observatory, not a new aggregation.
2. A classic observation without `HealthPing` uses `Delay` in milliseconds.
3. A present but empty/invalid burst measurement is not replaced by a fabricated zero-millisecond RTT.
   Negative RTTs and inconsistent sample counts are invalid. A real zero/sub-millisecond success is valid.
4. `averageRTT < maxRTT` is within-limit; equality and greater values are over-limit. Compare durations without rounding
   the limit or burst average down to milliseconds. With a band (LP-7), an option this balancer has latched
   over-limit is within-limit again only when `averageRTT < recoverRTT`.

Missing, stale-as-reported-by-observatory, failed and forced-down probes are not usable, even during degradation.
Retain their original health/error and track provenance. This layer adds no independent freshness timer.

### LP-3 Selection And Fallback

These phases apply to positive maxRTT only. Disabled policies retain legacy selection and termination.

- Phase1: if any primary selector is within-limit, choose its leastPing winner, even if the fallback is faster.
- Phase2: otherwise, if the fallback's reported result is usable and within this parent's limit, choose it.
- Phase3: otherwise, if any option is usable, choose leastPing over only this level's usable selector
  options plus the one usable fallback result. Label this `degraded`, never within-limit/fully healthy.
  Over-limit options may take traffic here; dead, forced-down, refused, unknown or invalid ones may not.
- If no option is usable and this level's explicit `fallbackTag` names an actual blackhole outbound, select
  that terminal action. Report its tag, fallback active and all primary shares0. Otherwise return an explicit
  route-drop result: choice empty, fallback inactive and all shares0. This includes an absent fallback or an
  unusable outbound/balancer fallback. Never escape to the default handler.
- Never create an implicit blackhole, assume a handler named `block` exists or search inside an unavailable
  child for a terminal handler. Blackhole is a configured terminal action, not a zero-RTT candidate. An
  unobserved outbound fallback cannot be loaded (LP-8); a failed fallback is never blindly dispatched.
- Retain leastPing's minimum-Delay and observation-order ties among primary options. In the degraded
  comparison, an equal-delay fallback is considered after the primaries, so it does not displace them.
  For a representative member-mean option, derive its comparison Delay from that mean in milliseconds,
  never from its unrelated legacy minimum-member status. Duration precision applies to the threshold,
  not to an unrelated tie-break change.
- `fallback.active` means the actual decision chose this level's fallback, including a degraded fallback.
  A degraded primary has share1/primary target and fallback inactive, despite exceeding maxRTT. A chosen
  fallback leaves primary shares0/targets empty. Unavailable reports the explicit blackhole or route-drop
  outcome above; a terminal blackhole is still unavailable, not a usable or within-limit path.
  Default-disabled legacy behaviour is retained.

### LP-4 Recovery And Startup

- Burst observatory already starts an asynchronous one-sample check for selected probes with no scheduled
  random delay; classic observatory also starts probing without an initial interval sleep. Reuse these
  initial checks, not a new probe worker or a wait for the full sampling window.
- Before usable initial results exist, report `pending`, never a fabricated healthy/zero-RTT path. Use a
  normal valid first result immediately; failed results remain unusable under existing health rules.
  Do not add unbounded waiting or a sleep to each route dispatch: an early request without a usable option
  takes LP-3's explicit terminal/drop action. Startup readiness, not a hidden request queue, waits for probes.
- Rollout acceptance must exercise the affected business balancer after the initial probe results, within
  the existing activation budget. An admin-only success is not proof of business readiness. Missing required
  initial results or a failed business path cannot pass; retain the existing bounded recovery behaviour.
- Probing continues while a primary is rejected. As soon as the observed average falls below recoverRTT
  (equal to the limit when no band is configured, LP-7) and the probe is alive, the primary is eligible
  again for new decisions.
- A single failure/recovery continues to use the observatory's current health contract. This spec adds
  no success counter or new background worker; its only recovery threshold is LP-7's recoverRTT, which
  governs this balancer's RTT eligibility, never probe health.
- Only new dispatches change. Existing TCP, UDP, Hy2, VLESS/Mux and AnyTLS streams are not closed or retried.

### LP-5 Local Policy, Track And Overrides

- Eligibility belongs to this balancer. A probe can be eligible at one limit and rejected at another;
  neither evaluation writes back to a shared `OutboundStatus` or changes global OB alive.
- Track supplies the same observation/provenance as today. Do not start per-user probes or infer health
  from outbound names, relay ownership, service identity or a downstream path not actually probed.
- A manual override is the existing `OverrideBalancerTarget` operation, not a new automatic exception.
  It retains precedence over this level's automatic selection, even for an over-limit, unobserved or
  observed-down target. The no-revival rule applies to automatic decisions, not this explicit operator pin.
  Preserve existing graph/loop and dispatch/authentication checks; an override cannot make a refused
  connection succeed. Report mode `override` and the target's true health/eligibility, not fake health.
  A parent can still apply its own limit to the effective observation bubbled from an overridden child.

### LP-6 Nested State And Concurrency

- Each child evaluates and advertises its own effective status and choice under its own policy, including
  a degraded choice. A parent consumes only that bubbled result as one fallback node and applies its own
  threshold to it. It must not enumerate/merge the child's selector, search its deeper fallbacks or choose
  an unselected descendant that happens to look faster. This replaces the earlier global-merge proposal.
- Selecting the child still uses normal routing resolution to its own choice; opaque means no flattened
  candidate pool, not that a balancer tag can be sent to an outbound handler. Preserve the child's selected
  result for the decision snapshot when deterministic; do not select a different child target merely to
  explain its RTT. A read must not advance a stateful strategy or invent a concrete next target.
- For an enabled maxRTT parent's evaluation, a roundrobin/random child supplies an arithmetic mean of its
  currently dispatchable members with known, alive, valid RTT. First take each member's own reported RTT,
  then average equally per member, not per raw sample or flattened descendant. Failed, forced-down,
  refused, unknown and invalid measurements never contribute a fabricated zero. Without any measured
  usable member the representative RTT is unknown; use the child's own fallback outcome where applicable.
- A member mean is representative, not a promise about the next individual dispatch. Report its aggregation
  kind and measured/dispatchable member counts so a partial mean is not presented as complete coverage.
  The child still performs roundrobin/random exactly once, only when selected for actual dispatch.
- The same opaque-result rules apply to child balancers in the primary member list and in fallbackTag.
  These new evaluation metadata do not overwrite legacy raw status or change non-opted-in parents'
  existing min-delay bubbling/picks. Other strategies' weighted pools and their own threshold contracts
  remain unchanged; do not silently extend the new arithmetic-mean rule to them.
- Existing graph/cycle and loopback guards stay intact, including live publication paths. No reverse
  fallback or implicit graph edge is introduced by comparing the parent options with the child result.
- Selection and explanation reuse a pure evaluation on explicit observation values. Read RPCs never pick,
  advance another strategy, change health, send probes or hold locks across I/O.
- Limit read-time work to the configured candidates/graph. No unbounded cache, per-connection state,
  new goroutine, timer or coarse global lock is needed.

### LP-7 Hysteresis (`recoverRTT`, amendment 2026-11-15)

The burst average is already a sliding window (for example 25 samples at a 10s interval), so single-sample
jitter does not cross the limit. An average that settles near the limit still crosses it slowly back and
forth, and each crossing moves new connections to a different path, often a different exit address.
The band makes leaving and returning asymmetric.

- Each enabled balancer keeps one latch per option it evaluates: each selector outbound, each member
  balancer and its one fallback result. The latch says "this option is over the limit at this level".
- Transitions, for an option with a usable measurement `r` (LP-2): not latched and `r >= maxRTT` latches it;
  latched and `r < recoverRTT` releases it. Within-limit means usable and not latched after the transition.
  With `recoverRTT = maxRTT` this reduces exactly to LP-2's single comparison.
- An option without a usable measurement (unknown, down, forced down, invalid) keeps its latch unchanged;
  it is excluded by LP-2/LP-3 regardless. A new balancer, including one replaced by rule publication or
  AddRule, starts with every latch released; an option's first usable measurement is compared with maxRTT.
- The band changes only Phase1/Phase2 eligibility (primary and fallback alike). Phase3's degraded
  comparison and the terminal/drop rules are unchanged: they rank usable options by RTT alone.
- Only an actual dispatch pick commits transitions. Control-plane reads, FallbackActive and PickShares apply
  the same transitions to a copy of the latches against the same snapshot and report what the next pick
  would decide, without storing them. A read therefore never changes a later decision.
- Latches are local to the balancer (LP-5): the same outbound can be latched in one balancer and not in
  another. A parent latches a child balancer's bubbled or representative-mean result as one option (LP-6);
  it never reads or writes the child's latches. A manual override neither consults nor changes latches.
- State is bounded by the configured options: a small fixed structure per balancer, updated under the
  balancer's own lock or by atomic replacement; no timer, goroutine or per-connection state.

### LP-8 Observed Outbounds (hard check, amendment 2026-11-15)

Under an enabled policy an unobserved outbound can never be usable, so configuring one is a mistake that
would silently disable a backup. The check makes it a load-time error.

- For every leastPing balancer with a positive maxRTT, and every balancer reached from it through members
  or fallbackTag, each selector-matched outbound and an outbound fallbackTag must be observed by the
  instance's observatory (classic or burst; a track follower counts as observed). Only an actual blackhole
  fallback is exempt. A fallbackTag naming no outbound and no balancer is an error.
- This is the existing spec019 M6 rule (`checkMemberObservation`), extended to enabled leastPing balancers
  as additional roots. The same check runs wherever M6 runs today: at start and on every rule publication
  (ReloadRules, AddRule, RemoveRule). A failing update keeps the previous map (H08).
- The error names the balancer and the unobserved outbound, like M6's message. No observatory at all is
  the same error, not an exception.
- Balancers with a disabled or omitted policy are not affected; their unobserved fallbacks keep today's
  unconditional behaviour.

## Control Plane (CP-1--CP-8)

Owner: existing `RoutingService`, through `GetBalancerInfo` and `ListBalancers`. No new `api.services` entry.

Proposed additive response shape (field numbers must be rechecked immediately before implementation):

- `BalancerMsg.least_ping_policy`, currently free field 11: message containing loaded `max_rtt_ns`,
  `recover_rtt_ns` (equal to `max_rtt_ns` when no band is configured) and enabled.
  It is present for leastPing on an implementing Core, including a disabled zero limit. Presence is the
  runtime capability signal; an older Core has no field and is **unavailable**, not unlimited/healthy.
- `BalancerMember.selection`, currently free field 6: message containing `usable`, `within_limit`, `reason`,
  `rtt_known`, `rtt_ns`, `latched` (LP-7, as the next pick would see it) and RTT aggregation metadata
  (member mean and its coverage counts where applicable).
  Proposed reasons: `eligible`, `unobserved` (an observed tag with no result yet; an unobserved tag cannot
  be loaded, LP-8), `probe_down`, `rtt_unavailable`, `invalid_measurement`, `max_rtt_exceeded` and
  `recovering` (latched, below maxRTT but not yet below recoverRTT). These are per-level selection reasons,
  not OB error strings.
- Keep `status.alive`, `status.delay`, original error and `tracked_from` as raw observation facts.
  A candidate may remain alive and usable but over-limit. During degraded mode it can have share1 while
  within_limit=false; this is not a contradiction or a claim that the threshold was met. An eligible but
  nonwinning candidate has share0 and is not unhealthy merely because another candidate is faster.
- `BalancerMsg.least_ping_decision`, proposed free field12: mode (`primary`, `fallback`, `degraded`,
  `pending`, `unavailable`, `override`), choice_tag, explicit terminal/drop outcome and the fallback's own
  reported status/selection. Pending describes missing initial observations, not a usable path. Do not insert
  the fallback's descendants into the existing selector/member rows. A concrete resolved target is only
  reported where the child itself can supply it without mutating scheduler state.
- Retain `override`, `fallback`, chain and share fields. For the threshold-limited leastPing family, load
  one balancer map and one observation snapshot per response and derive targets, exclusions, shares,
  effective status, opaque child results and fallback from it. Do not assemble a contradictory response
  from changing reads or perform an extra child pick for display.

Consumers planned in the same feature family (not implemented or authorized for execution by promotion):

- Sidecar's balancer control-plane projection (existing Sidecar spec018): pass policy and selection through
  in `/xray-debug.balancers`, including absent capability, without reading node JSON to reconstruct them.
- Portal's Balancers view (existing Config spec022): show the loaded limit, candidate RTT, eligibility,
  `RTT limit exceeded`, `degraded`, pending, explicit terminal/drop and active fallback/override. Distinguish
  supported-but-disabled from unsupported, and a representative member mean from a deterministic choice's RTT.
  Keep raw Observatory alive unchanged; never reconstruct maxRTT by reading configuration files.
- `xray api bi`: report the same fields; JSON passes them through, text distinguishes reachable/over-limit.
- xray-tui presentation is a dated non-goal (2026-11-14); it must continue to decode older/newer additive
  responses. Add an explicit consumer task before promising a new TUI threshold column.

No credentials, URLs or deployment endpoints are returned. Response size is bounded by the loaded graph.
Real started-instance gRPC tests must cover both default and enabled settings; see [tests](tests.md).

## Compatibility And Release

- Other strategies, including both existing maxRTT implementations, retain their contracts and wire fields.
  Never reuse those messages or renumber protobuf fields to implement leastPing.
- New configuration can silently pass an old Core's config test while its setting is ignored. Rollout
  therefore requires an explicit candidate capability check and a post-activation RPC read of the exact
  loaded policy. Configuration validation alone is not an acceptance gate.
- A positive policy cannot be rolled back by downgrading the binary alone. Restore the saved binary/config
  pair; a default-configuration regression must pass before any opt-in fleet rollout is considered.
- Any implementation later follows the fork-line/format/build/control-plane rules in spec/README.md.
  Spec promotion creates no implementation commit, new fork generation, release artifact or deployment authority.

## Verification And Acceptance

This section defines how the contract must be proved, not completed test results. The detailed inputs and
expected outputs remain in the [verification matrix](tests.md); its 96 case IDs must map to executable
tests or explicitly owned consumer/rollout checks. A passing helper test, configuration check or build
alone is insufficient. Tests use synthetic identities and owned local endpoints, not production traffic.

### V-1 Requirement-to-evidence Map

| Contract | Cases | Method and required evidence |
|---|---|---|
| LP-1 opt-in/configuration | C01--C09, Q14, Q19 | Native JSON and direct protobuf fixtures, including live AddRule; exact loaded duration or focused rejection; absent/zero policy matches an independent legacy oracle |
| LP-2 measurement | Q02--Q11, Q13, H03--H06 | Inject exact observations; prove duration-boundary precision, successful-window average, genuine zero, invalid/unknown exclusion and unchanged raw health/track |
| LP-3 selection/termination | Q01, Q03, Q06--Q07, Q12, Q15--Q21, N01--N04, N08 | Assert chosen tag/mode/share/fallback flag, then verify native dispatch with independent destination markers and a default-handler sentinel |
| LP-4 startup/recovery/flow survival | H01--H04, H11--H12, N05, N10 | Real observer initial-check events and bounded readiness; requests demonstrate fallback/recovery, existing flow continuity and failure when only admin is ready |
| LP-5 locality/override | Q13, H05--H08, H10, R06 | Shared observation remains unchanged under different limits; override pins the actual target with true health, and clearing restores automatic policy |
| LP-6 nesting/mean/no side effects | H09, N06--N07, N09, N11--N20, R11--R12 | Child-owned opaque result, exact per-member mean and coverage, dispatch markers, graph rejection and picker-call/index instrumentation |
| LP-7 hysteresis | C10--C11, Y01--Y10, R15 | Scripted observation sequences against literal expected latches/choices; reads leave latches unchanged; no-band equals LP-2 oracle |
| LP-8 observed outbounds | O01--O07 | Load-time rejection at start and on each publication path naming balancer and tag; previous map kept; disabled policy unaffected |
| Control plane/compatibility | R01--R14, C09 | Real started-instance RoutingService calls, one-snapshot consistency, additive old/new decoding, runtime capability gate and Sidecar/Portal consumer evidence |

### V-2 Deterministic Algorithm And Native Dispatch

1. Feed an immutable ordered observation fixture to the evaluator. Expected tags, numeric RTTs, modes,
   shares and terminal outcomes must be literal independent expectations, not recomputed by production
   selection helpers. Include exactly below/equal/above the threshold in native duration precision; do not
   use Internet timing or a sleep to decide whether an equality boundary passed.
2. Start a real Core with local target A, target B and fallback F returning different response markers.
   Send requests through an actual inbound and its configured balancer route, not a direct call to the
   strategy. Freeze the injected observations for each assertion so RPC and dispatch are compared against
   the same state, not measurements from different times.
3. With an illustrative 50ms limit, prove at least these independent outcomes: A20/B30/F10 selects A;
   A80/B90/F40 selects F; A80/B70/F120 selects B in degraded mode; A80/B90/F70 selects degraded F.
   Repeat without a fallback: usable primaries still work, and all-slow primaries select their minimum.
4. Install a distinguishable default-handler sentinel. When all options are unusable and no explicit
   blackhole fallback exists, the request must fail by route drop and the sentinel must receive zero
   dispatches. With an explicitly configured blackhole under an arbitrary tag, prove that exact handler
   was selected, all primary shares are zero and fallback is active. A tag merely named block is not a
   blackhole type check. Verify no request reaches a failed fallback or a hidden child alternative.
5. Cross a threshold while a TCP stream, Mux stream and UDP association are already active. Check their
   ongoing payload integrity, then create a new dispatch and verify its new destination. A reconnect,
   transparent retry or a replacement stream cannot count as continuity of the original flow.

### V-3 Child Means, Startup And Read-only Snapshots

- A roundrobin/random child with measured members10/90ms must report50ms, not10ms. Vary their raw sample
  counts while retaining those member averages: the result must stay50ms. Add dead, forced, unknown and
  invalid members and assert both the included values and measured/dispatchable coverage counts.
  Test exact parent-threshold equality and degraded comparison using the mean, not legacy minimum status.
- Interleave RPC reads and parent evaluations between actual child dispatches. A controlled round-robin
  sequence must remain unchanged; instrument stateful/random picks to prove zero extra scheduling calls
  from reads and exactly one child pick when dispatch selects it. Do not use a probabilistic traffic
  histogram as the sole proof. Include nested children and both primary-member and fallback positions.
- Start the real burst observer with a long interval/window, record its initial-check event and the owned
  HTTP fixture's first request, and require the immediate initial-check path rather than waiting for the
  scheduled window. Use event synchronization and a bounded deadline. Keep its reply gated to observe
  pending before release, then prove the first valid result enables selection without filling the window.
- Exercise readiness with admin responding while the affected business route is still pending or failing.
  The candidate must not be accepted. This is a Config rollout-tool check, not a claim that the router
  itself queues requests. Missing required first results within the activation budget trigger the existing
  recovery path; extending the budget silently or accepting admin-only readiness is failure.
- Use a counting/changing observer and concurrent rule/override updates to verify one published map and
  observation snapshot for each enabled-family response. Assert internally matching choice, member reasons,
  shares, bubbled RTT and fallback flags. Clone/compare raw observations before and after reads to detect
  mutation. Run these paths under the Go race detector; no locks may be held over a blocked fixture I/O.

### V-4 Runtime API, Sidecar And Portal

Start a real Core and call both GetBalancerInfo and ListBalancers over local gRPC. In-process getter calls
alone do not meet CP-7. Verify exact loaded max_rtt_ns/enabled, known-zero versus unknown RTT, aggregation
and coverage, every decision mode, terminal/drop, override and track provenance. Check xray api bi's text
and JSON against the same frozen fixtures; a response may not fabricate an exact random child's next target.

Separately owned consumer tests must prove all of the following:

- Sidecar passes the runtime fields through /xray-debug.balancers. Omitted fields from an old Core remain
  unavailable; a supported zero/disabled policy is distinct. Neither consumer loads node JSON to invent a
  missing policy, measurement or capability.
- Portal browser tests at desktop and mobile sizes show the loaded limit and candidate/mean RTT, pending,
  over-limit, degraded, manual override and terminal outcomes. An alive over-limit winner stays raw-alive
  but visibly degraded; a healthy nonwinner is not labeled down. Old-Core absence is not shown as healthy.
- Old clients still decode the additive response. An old Core that accepts the new JSON but ignores maxRTT
  must fail the candidate capability gate. Test this negative path rather than relying on run -test alone.

### V-5 Regression, Execution And Release Evidence

After the test cases are implemented, run from the Core repository (commands are planned, not run here):

```sh
go test ./infra/conf ./app/router ./app/router/command ./app/observatory/...
go test -race ./app/router ./app/router/command ./app/observatory/...
go test -race ./app/router ./app/router/command -run 'LeastPing.*MaxRTT' -count=20
go test ./...
go vet ./...
bash .github/check-gofmt.sh
```

The focused prefix must match actual tests: zero matched tests is not success. Required cases may not be
skipped into a PASS. Retain network failures and apply the existing external-network test setup; do not
confuse a network failure with a passing feature test. Regress weightedLeastPing/leastLoad, disabled
leastPing, non-opted-in member bubbling, roundrobin/random picks, track/force-down, graph/loop rejection,
user-domain routing, protocol/Mux dispatch and live publication. Consumer suites run in their own repositories.

Once source is committed under the normal workflow, use the approved dual-platform Core build wrapper and
execute the built Darwin binary for fixture validation. Record Linux artifact identity too; cross-compilation
is not a substitute for native execution or the existing pre-release stability gate. This documentation-only
change neither runs those gates nor authorizes a build, commit, push or production switch.

Before a separately authorized rollout, Config owns isolated capability verification, affected-business
readiness within the existing interruption budget, post-activation readback of the exact policy and paired
restore verification. Preserve the prior binary/config pair and service state. A binary-only downgrade that
silently ignores the new field is not a valid restore. An earlier PASS cannot substitute for evidence for a
different source, binary or enabled configuration.

Record one evidence index linking requirement/case IDs to the executable test name or consumer check,
source/pin revisions, fixture and binary hashes, platform/Go version, full commands, exit codes, matched test
counts, skips, logs, actual marker responses and real RPC captures. Give each required stage PASS, FAIL or
NOT RUN; any required FAIL, unexpected skip or missing evidence prevents a complete acceptance claim.
Document checks passing means only links/structure are valid, never that this feature works.

## Clarification Complete (2026-11-14)

The operator confirmed the following design boundary after review; no product-semantic question remains:

1. Roundrobin/random supply the representative member mean described in LP-6, without parent re-selection.
2. Startup reuses the immediate initial probes; pending is not healthy and does not require a full window.
3. Blackhole must be explicit. Otherwise all-unusable selection explicitly drops, never the default handler.
4. A limit does not impose a new fallback requirement on existing leastPing configurations; independent
   graph requirements remain. Without a fallback, degradation considers only this level's primary pool.
5. Preserve the existing manual override semantics and expose the override and actual target health.
6. Use strategy.settings.maxRTT with no default positive limit; runtime control-plane policy and decisions
   feed Sidecar and Portal, including mean/pending/degraded/terminal explanations and older-Core absence.
7. (2026-11-15) Add hysteresis: optional `strategy.settings.recoverRTT`, `0 < recoverRTT <= maxRTT`,
   omitted meaning no band; per-balancer latches committed only by dispatch picks (LP-7).
8. (2026-11-15) An enabled balancer's selector outbounds and outbound fallback must be observed; anything
   else except an actual blackhole fallback is a load-time error, through the extended spec019 M6 check (LP-8).

The narrowest snapshot interface is an implementation-review detail, not a pending product decision.
Actual per-region thresholds and production rollout scope are operational inputs, not default values or
blockers to contract clarification. Runtime implementation authorization and execution evidence remain separate
from this completed promotion.

## Evidence Status

Source inspection and specification documentation only. No feature code, protobuf generation, new test implementation,
build, stress run or deployment has been performed for this proposal. All verification here and in tests.md is planned,
not claimed to have passed. Historic baseline test results do not validate this new feature.
