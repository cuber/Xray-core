# Implementation Plan (Not Started)

Status: Core spec027 approved and promoted from draft005 on 2026-11-14; runtime implementation has not
started or been authorized for execution. The [spec](spec.md) owns the contract; [tests](tests.md) owns
its verification matrix.

## 1. Configuration And Typed Settings

- `infra/conf/router_strategy.go`: add a leastPing settings builder using the existing duration decoder;
  reject invalid/negative values, leaving zero disabled. Keep other builders untouched.
- `app/router/config.proto`: propose a new `StrategyLeastPingConfig { int64 maxRTT = 1; int64 recoverRTT = 2; }`,
  durations in nanoseconds, matching the existing strategy convention. Do not borrow another strategy's typed
  message. Validate `0 < recoverRTT <= maxRTT` (zero meaning no band) in both the JSON builder and the
  direct-protobuf path.
- `infra/conf/router.go` and `app/router/config.go`: accept legacy absent typed settings as zero; reject
  wrong typed settings, but permit positive limits without fallback for JSON/direct protobuf/live publication.
  Retain independent spec019 member-graph requirements. Never insert a default positive maxRTT.
  Add only a focused guard for the earlier misplaced top-level maxRTT, not global strict JSON parsing.
- Regenerate protobuf with the repository's pinned toolchain; do not hand-edit generated code or headers.

## 2. Pure leastPing Evaluation

Keep immutable options on `LeastPingStrategy`. Design a pure helper accepting the primary candidates,
an ordered observation, one opaque fallback result and options. Return mode, chosen option, whether it is
the fallback, and per-option usability/within-limit/RTT/reason. Do not flatten child candidates.

- The zero-limit path reproduces the current algorithm exactly; no sorting or changed tie precedence.
- The enabled path prefers an under-limit primary, then an under-limit fallback. If none meets the limit,
  choose leastPing over usable primary options and that one usable fallback result. Only RTT may be
  relaxed; unknown/down/forced/refused candidates remain excluded from automatic selection. No new timer,
  probe average or cache. An absent fallback simply contributes no option.
- Preserve the decision's fallback flag in the existing resolver: a chosen fallback balancer is resolved
  normally, not dispatched as an outbound tag. Conversely, a degraded primary must not be mistaken for
  fallback merely because its RTT failed the preferred limit. No usable option selects this level's explicitly
  configured blackhole fallback, or produces explicit route drop when none exists. Preserve distinct terminal
  tag/fallback flags and never let a picker error reopen the default outbound.
- Retain manual override precedence, including targets automatic selection rejects; do not bypass the
  existing graph/authentication guards. Report override with real eligibility, not a healthy replacement.
- Use the helper for actual picks, FallbackActive, PickShares and principle targets. A read never calls
  another strategy's PickOutbound, which could mutate round-robin or weighted scheduler state.
- Keep raw observations immutable, including forced/down/tracked entries. A limit in one balancer cannot
  influence another's health. Review allocation/lookup cost against the existing bounded candidate walk.

### 2a. Hysteresis latches (LP-7)

- The pure helper takes the balancer's current latch set as one more input and returns the decision plus
  the latch set after this evaluation's transitions. The helper itself stores nothing.
- `PickOutbound` (actual dispatch) commits the returned latch set: a small immutable map keyed by option
  tag, replaced through an atomic pointer or under the balancer's own short lock; concurrent picks against
  the same snapshot compute the same result, so last-writer-wins is safe. No timer or goroutine.
- `FallbackActive`, `PickShares`, principle targets and the RPC projection call the same helper and
  discard the returned latches.
- A balancer built by rule publication starts with an empty set; latches are not carried across maps.
- With recoverRTT equal to maxRTT the helper must reproduce the no-band decisions exactly (Y03).

### 2b. Observed-outbound check (LP-8)

- Extend `checkMemberObservation` in `app/router/loops.go`: add every leastPing balancer with a positive
  maxRTT to the root set alongside balancers with members. The existing closure over members and
  fallbacks, the blackhole exemption, `observes()` (track followers included) and its call sites through
  `guardRouting` stay as they are. Keep M6's error text shape; say which rule required the observation.
- A fallbackTag that names neither an outbound nor a balancer must also fail there for enabled roots.
- No change for disabled balancers (O07).

## 3. Nested Status And Read Snapshots

`member_status.go` must let each child compute its own effective result, including a degraded choice.
The parent accepts that bubbled status/selection as one fallback node, not a pool. It cannot inspect the
child's internal selectors/deeper alternatives to improve its choice. Normal child-owned evaluation and
normal routing resolution remain; no live-observer recursion or extra child pick for a read projection.
Keep deterministic child choice and RTT tied to one snapshot. For roundrobin/random children in the enabled
parent's view, compute an equal per-member mean of dispatchable, alive, valid measured results and report
coverage/aggregation metadata. Do not weight by raw sample count or flatten descendants. This view is separate
from legacy raw status/min-delay bubbling for non-opted-in parents. Never advance a child for a read, and call
its actual picker once only if dispatch reaches it. Apply this to both member and fallback child balancers.

`command/command.go` currently obtains each facet through separate interface calls. For the enabled family,
add a narrow read-snapshot projection through `features/routing`/the existing balancer view or a small
optional interface. Decide that shape during review, not by introducing a second scheduling engine.
The projection must share the pure helper and one frozen map/observation; legacy interfaces and default
consumers remain supported. Unrelated strategy selection behaviour is outside this change.

Read default/disabled behaviour carefully: do not fix unrelated sentinel, missing-target or weighted-pool
semantics while introducing maxRTT. Tests distinguish opt-in decisions from baseline compatibility.

## 4. Additive Control-Plane Explanation

- Extend `features/routing/balancer.go` and `app/router/command/command.proto` with the optional policy and
  per-candidate selection messages proposed in the spec; recheck free field numbers at implementation time.
- Report raw alive separately from local eligibility. Represent a real zero RTT with `rtt_known = true`;
  absence/unsupported is not zero. Do not overload `LastErrorReason` with this balancer's threshold.
- Report `usable` separately from `within_limit`, and mode separately from both: an over-limit degraded
  winner must be visible as selected without being mislabeled within-limit. Include one fallback result,
  never its flattened descendants. Shares/targets/fallback.active describe the actual choice, including
  explicit blackhole vs route-drop. Add pending and representative-mean coverage, and report loaded maxRTT/enabled
  even when the strategy's policy is disabled, so unsupported and disabled are distinct.
- Update `xray api bi` text output while preserving JSON compatibility.
- Core tests use fake observers for deterministic unit/snapshot cases, and a real started Core plus real
  gRPC for the external contract. A direct call to an internal Go method is not sufficient CP-7 evidence.

## 5. Consumer And Operational Work (Separate Owners)

Before promising deployment, extend Sidecar's existing balancer projection and Config's Portal view/tests;
do not place those implementations or their authoritative specs in Core. Consumers show absent fields
as unavailable and never infer support/limits from configuration files or display strings.

Config rollout tooling must inspect the candidate's capability before activation (isolated check-stage
instance and source identity), then verify the actual loaded policy after activation. An old Core can
silently ignore this JSON; `run -test` and compile success cannot be the capability test. If staged native
capability cannot be checked, stop before production activation rather than guessing.

Reuse the observatory's immediate first check; do not add a new worker or wait for a complete sampling
window. Extend startup acceptance to require initial observations and a request through the affected
business balancer within the existing activation budget. An admin-only probe cannot waive this readiness
check. Do not queue route requests without a bound while waiting; early unusable requests terminate explicitly.

The fleet task separately owns naming, exact users/paths, track, threshold choices and hierarchy. An
observed backup outbound can now participate directly under an enabled policy; it need not be wrapped
in another balancer solely for threshold checks. An actual fallback balancer remains one opaque node.
No URL override, retry/timeout, path inference or per-route probe is added as a hidden side effect.

## 6. Gates And Evidence

1. Record the implementation SHA and exact enabled/disabled fixtures; implement the matrix in tests.md.
2. Run targeted tests, relevant full packages, race/repetition and loopback protocol/RPC checks.
3. Run broader baseline regressions, public-content review, formatting/vet, and the approved dual-platform
   build wrapper only after a separate local-commit authorization; it refuses dirty Core worktrees.
4. Run the existing stability gate before any production candidate is declared approved. An isolated
   loopback test or cross-compilation is not that gate and does not authorize rollout.
5. Revalidate Sidecar/Portal compatibility using their own test suites and runtime API fixtures.
6. Preserve every failure and skip; fill evidence only after execution, including pairing/restore tests
   before any separately authorized migration. No commands in this plan have been executed for this feature.
