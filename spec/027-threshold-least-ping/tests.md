# Verification Matrix: thresholdLeastPing

All cases are planned, not implemented or executed. Expected values are literal, never produced by the
helper under test. Use synthetic identities and local endpoints only.

Unless stated: maxRTT 50ms, no band, candidates A and B, fallback F (an observed outbound); RTT in ms.

**Fixtures.** Router-level and native cases use a test observatory that serves injected statuses through
`GetObservation`, implements the change notification and fires it on every injection, and counts reads.
At least the E17 chain uses the real burst observer against a local HTTP probe target whose response delay
the test controls, with wide margins around the threshold.

## C. Configuration (TL-1, TL-9)

| ID | Input | Required result |
|---|---|---|
| C01 | `thresholdLeastPing`, `thresholdleastping`, `ThresholdLeastPing` with maxRTT `50ms` | accepted, same strategy |
| C02 | maxRTT `50ms`, `1s`, `500us`, `50.000001ms` | exact duration through JSON → typed proto → RPC |
| C03 | maxRTT missing, `0s`, `-1ms`, bare number, boolean, null, malformed, overflowing | error naming maxRTT before any listener starts |
| C04 | recoverRTT `40ms`, `50ms`, omitted with maxRTT `50ms` | loaded recover_rtt_ns 40ms, 50ms, 50ms |
| C05 | recoverRTT `60ms` with maxRTT `50ms`; `0s`; negative; bare number; null | error naming recoverRTT |
| C06 | no fallbackTag; outbound fallback; balancer fallback; blackhole fallback | accepted when TL-7 and the graph checks hold |
| C07 | member balancers without a fallbackTag | rejected by spec019's existing rule |
| C08 | direct protobuf with a wrong typed message, zero or negative maxRTT, recoverRTT above maxRTT | rejected, not defaulted |
| C09 | AddRule publishing a valid and an invalid thresholdLeastPing balancer | valid one live; invalid rejected, previous map kept |
| C10 | the baseline Core loads a C01 fixture as JSON and as direct protobuf | `unknown balancing strategy` / `unrecognized balancer type` |
| C11 | leastPing with settings `{maxRTT}`, `{recoverRTT}`; leastPing with another unknown setting | first two rejected naming thresholdLeastPing; the third loads as today |
| C12 | publication changes maxRTT 50ms → 30ms with A at 40 | new limit adopted atomically with the new map; latches start released; A over-limit |

## Q. Decision (TL-2, TL-3)

Literal options into `decideThreshold`.

| ID | Options | Required result |
|---|---|---|
| Q01 | A 10, B 20, F 5 | A, primary; shares A=1, B=0; principle [A]; fallback inactive |
| Q02 | A 49.999999, B 60 | A; B over-limit |
| Q03 | A 50, B 60, F 40 | F, fallback; candidate shares 0; principle empty |
| Q04 | A average 50.000001ms with Delay 50, limit 50.000001ms | over-limit: compared on Average, not truncated Delay |
| Q05 | A dead with old 1ms, B 30 | B; A's raw reason unchanged |
| Q06 | A 80, B 70, F 120 | B, degraded; share B=1; principle [B]; fallback inactive |
| Q07 | A 80, B 90, F 70 | F, degraded; fallback active; candidate shares 0 |
| Q08 | A 80, F 80 | A, degraded: a candidate wins an exact RTT tie with the fallback |
| Q09 | A 20, B 20 (exactly equal durations) in both orders | observation order wins |
| Q10 | no fallback; A 30 / A 80 / A dead | A primary / A degraded / drop |
| Q11 | A dead, B forced down, F refused relay | unavailable, drop; no default outbound |
| Q12 | all unusable; fallback is an actual blackhole with an arbitrary tag | that blackhole; fallback active; mode unavailable |
| Q13 | all unusable; an outbound merely named `block` that is not a blackhole | drop |
| Q14 | burst HealthPing with zero successes and Delay 0 | not usable; no fabricated RTT |
| Q15 | real sub-millisecond success | usable, rtt_known, within-limit |
| Q16 | classic observation without HealthPing at 49/50/51ms | within, over, over |
| Q17 | negative RTT; All < Fail; malformed alive status | invalid_measurement; no panic |
| Q18 | A never observed since publication, B dead, no fallback | pending; connection dropped |
| Q19 | A 30 under one balancer limited 20ms and another limited 100ms | over in the first, within in the second; shared observation byte-identical |
| Q20 | F is a child balancer reporting unavailable, A 80 | A degraded; never a search inside F |
| Q21 | A dead with old 1ms, B 80, F 90 | B degraded; A never revived in Phase 3 |
| Q22 | all candidates unusable; F is an unavailable child whose own fallback is a blackhole | drop at this level; no descent to the child's blackhole |
| Q23 | A window average 60 while its latest sample is 20 and minimum 15 | over-limit; only the average is used |
| Q24 | A 30.9ms, B 30.1ms; then A 30.9ms, B 29.9ms; limit 30.5ms with A 30.4ms; mean 40.5ms vs limit 40.5ms | A (both 30ms: observation order); then B (29 < 30); A within 30.5ms; mean equal to limit is over-limit |
| Q25 | observation of an unexpected type | every option unknown; mode unavailable (or pending before first results) with that reason |
| Q26 | reported upward status for each mode: primary, fallback, degraded, override, pending, unavailable | chosen option's status and RTT; override target's observation; unknown; dead with reason |

## Y. Hysteresis (TL-4)

maxRTT 50ms, recoverRTT 40ms, the single candidate A and F 30 unless stated. Each step is one observation
update followed by one computation; the expected choice per step is literal.

| ID | Sequence | Required result |
|---|---|---|
| Y01 | A 45, 52, 45, 41, 39 | A, F, F, F, A |
| Y02 | A 49.999999, 50, 40, 39.999999 | A, F, F, A |
| Y03 | Y01 with recoverRTT omitted | A, F, A, A, A |
| Y04 | A 60, then down, then alive at 45 | F, F, F: the latch survives the outage |
| Y05 | fresh balancer, A's first result 45 | A (first result compared with maxRTT) |
| Y06 | A latched; rule publication replaces the balancer; A 45 | A: a new balancer starts released |
| Y07 | A latched at 60; 1000 picks and RPC reads with no observation change; then A 39 | latch set byte-identical across the picks and reads; the computation triggered by 39 releases it; A |
| Y08 | F 55 then 45 with A 70 | F degraded both times; at 45 F is still latched so Phase 2 refuses it, Phase 3 picks it on RTT |
| Y09 | parent latches a child balancer's result 55 then 45; child's own latches differ | parent follows the child's reported RTT; child latches untouched |
| Y10 | the same outbound in two balancers, driven so that it is latched in one only | the other balancer's latch and decision unaffected |
| Y11 | candidates A and B 70; A latched at 60; override set to B, A goes to 45, override cleared | latch unchanged by the override (still latched at 45); after clearing, F |

## E. Observation-driven Computation (TL-5)

| ID | Action | Required result |
|---|---|---|
| E01 | a burst probe result changes A from 30 to 80 with no traffic | a new pass is published; RPC shows the new decision without any pick |
| E02 | SetForceDown on A, then clear | each change publishes a pass; A excluded then restored |
| E03 | Cleanup removes a tag's results | pass published; the option becomes unknown |
| E04 | classic observatory result | pass published |
| E05 | ReloadRules, AddRule and RemoveRule publishing maps with thresholdLeastPing balancers, picks running concurrently | no pick ever sees a new map without a pass; latches start empty |
| E06 | override set and cleared | pass published; mode override then automatic |
| E07 | 1000 notifications from many goroutines during a slow computation | bounded number of passes; the last pass reflects the final observation; no goroutine growth |
| E08 | counting observatory; 10000 picks of a thresholdLeastPing balancer | zero `GetObservation` calls from picks |
| E09 | notifications fired while probes, SetForceDown, RPC reads and reloads run under `-race` | no data race, no deadlock |
| E10 | no thresholdLeastPing balancer configured | no subscription, no computation; other strategies' picks unchanged |
| E11 | counting observatory; a graph with 1, 10 and 100 thresholdLeastPing balancers | exactly one `GetObservation` per pass in every case |
| E12 | one observation change flips a child's decision | the parent's decision in the same pass (same generation) already uses the child's new result |
| E13 | benchmark: 100 balancers, 300 outbounds, nesting depth 3, one notification per probe result | pass time recorded with a p99 target under 1ms on the build host; burst RTTs measured with and without the subscription agree within run-to-run noise |
| E14 | a notification callback that calls `GetObservation`, `SetForceDown` and `Cleanup` | deterministic test completes; no deadlock; callbacks never run under the notifier's locks |
| E15 | Start, then Close | a pass exists before the first routed connection; after Close returns no callback runs and no goroutine remains |
| E16 | the observatory stops producing results | the last pass stays; RPC observation time ages; no fabricated change |
| E17 | real burst observer with a long interval/window and a local HTTP probe target whose delay the test sets | pending until the immediate first check returns, without waiting for the window; then delay changes move the decision and the routed marker through probe → notification → pass → route |
| E18 | an option's samples all older than 2 × sampling × interval | unusable at the next pass |
| E19 | a Hy2 relay outbound's relay status becomes `denied` with no probe result | a pass is published; the option is unusable (D12) |
| E20 | runtime AddOutbound of an observed outbound matching a selector; RemoveOutbound of a candidate and of the fallback | each publishes a pass; the added one is pending until observed; removed ones are unknown |
| E21 | clone the observation before a pass and compare after | byte-identical: computations never modify statuses |

## N. Nesting And Data Plane (TL-3, TL-6)

A running Core whose outbounds reach local servers returning distinguishable markers; requests go through
a real inbound and routing rule. Observations are injected through the fixture and frozen per assertion.

| ID | State | Required result |
|---|---|---|
| N01 | A 20, B 30, F 10 | marker A |
| N02 | A 80, B 30 | marker B |
| N03 | A 80, B 90, F 40 | marker F |
| N04 | A 80, B 70, F 120 | marker B, decision degraded |
| N05 | all unusable, no blackhole; F's server and a default-outbound sentinel count requests | request fails; F and the sentinel receive nothing |
| N06 | all unusable with an explicit blackhole | request ends at that blackhole |
| N07 | A recovers below the limit | the next request returns marker A |
| N08 | a TCP stream, a Mux stream and a UDP association active while the decision changes | each keeps its payload intact on the original connection; a reconnect, retry or replacement stream does not count; only new requests move |
| N09 | parent A 80, B 90; child fallback reports 40 | parent chooses the child; routing reaches the child's own choice |
| N10 | child reports 120 and hides an unselected 5ms alternative | parent degraded to A 80; the hidden outbound never receives traffic |
| N11 | child limited at 200ms reports 120; parent limit 50ms | parent treats 120 as over-limit |
| N12 | roundRobin child and random child, each with members 10 and 90 | parent sees 50ms for each (over a 50ms limit), not 10ms |
| N13 | members with different sample counts, one dead, one without a result, one invalid | mean of the usable measured members only; measured/dispatchable counts disclosed |
| N14 | roundRobin child chosen repeatedly; RPC reads and computations in between | the child's rotation advances exactly once per routed request |
| N15 | legacy leastPing parent with a thresholdLeastPing member | parent bubbles the member's decision status, not its fastest candidate |
| N16 | member/fallback cycle at start or reload | rejected by the existing checks |
| N17 | roundRobin child with no usable measured member; its fallback observed, unknown, or a blackhole | child's own fallback result, unknown, or unavailable bubbles as one option; no fabricated mean |
| N18 | the same child as a candidate of one thresholdLeastPing parent and the fallback of another | both evaluate it with the same rules and the same pass |
| N19 | thresholdLeastPing parent with a legacy leastPing child | the child's existing bubbled status is used |
| N20 | thresholdLeastPing child rejects its slow candidates and chooses its own fallback at 40 | it reports 40 upward, not its rejected candidates |
| N21 | thresholdLeastPing child overridden to a 120ms target; parent limit 50ms | parent sees 120 and treats the child as over-limit |
| N22 | parent and child both degraded with different choices | each decides its own level; no reverse edge; routing follows both choices |
| N23 | parent 80/90; child roundRobin with members 10 and 190 (mean 100); limit 50ms | parent degraded to 80, comparing with 100, never with 10 |

## O. Observed Outbounds (TL-7)

| ID | Configuration | Required result |
|---|---|---|
| O01 | outbound fallback matched by no observatory selector | start fails naming balancer and tag |
| O02 | an unobserved selector-matched candidate | start fails naming balancer and tag |
| O03 | unobserved actual blackhole fallback; unobserved non-blackhole named `block` | first accepted, second rejected |
| O04 | fallback balancer whose own outbound fallback is unobserved | rejected through the reached set |
| O05 | fallback observed only as a track follower | accepted |
| O06 | AddRule or ReloadRules introducing O01; RemoveRule leaving it; no observatory at all; fallbackTag naming nothing | each rejected; previous map kept |
| O07 | leastPing (and each other legacy strategy) with an unobserved outbound fallback such as `direct` | loads; legacy unconditional fallback unchanged |
| O08 | runtime AddOutbound of an unobserved outbound matching a thresholdLeastPing selector; RemoveOutbound of its fallback | add rejected; removal accepted, fallback unknown from the next pass (D15) |

## H. Overrides, Track And Locality (TL-8)

| ID | Action | Required result |
|---|---|---|
| H01 | override to an over-limit target, then clear | override wins with its true state; clearing resumes the decision |
| H02 | override to an unknown or forced-down target | still wins; mode override; true state reported; guards not bypassed |
| H03 | several outbounds track one probe | same average and provenance; no extra probes |
| H04 | force down the probe a tracked candidate follows | candidate excluded; restored only by normal observation |
| H05 | same outbound in two thresholdLeastPing balancers with different limits | independent decisions; shared observation unchanged |
| H06 | leastPing balancer with identical selectors and fallback | its picks identical to the baseline Core |
| H07 | override to an outbound outside the candidate set | wins with its true state, as override does today |
| H08 | a probe goes unhealthy and recovers under the observatory's own health rule | usable exactly when the observatory reports alive again; the latch adds no recovery counter of its own |

## R. Control Plane

Real started Core, real local gRPC, both `GetBalancerInfo` and `ListBalancers`.

| ID | Check | Required result |
|---|---|---|
| R01 | thresholdLeastPing loaded | strategy `thresholdleastping`; exact max_rtt_ns and recover_rtt_ns |
| R02 | alive but over-limit option | raw alive true; within_limit false; reason max_rtt_exceeded |
| R03 | latched option between recoverRTT and maxRTT | latched true; reason recovering |
| R04 | degraded winner | share 1, within_limit false, mode degraded |
| R05 | pending vs real 0ms vs eligible loser | three distinct reports |
| R06 | fallback child with hidden alternatives | one fallback result; no flattened rows |
| R07 | observation changes during a read | every field of the response from one pass (same generation) |
| R08 | concurrent reads, picks, AddRule, RemoveRule and overrides under `-race` | no race, deadlock or mixed maps |
| R09 | old client against new Core; new client against baseline Core | old client decodes; baseline response has none of the new fields |
| R10 | leastPing balancer | no threshold fields |
| R11 | terminal blackhole vs drop vs override | distinct mode, choice and fallback flags |
| R12 | `xray api bi` text and `-json` on the R01–R16 fixtures | same facts; observation age, latch, reason and mean coverage shown |
| R13 | normal fallback, degraded candidate, degraded fallback | principle target, shares and fallback.active agree with the decision in each |
| R14 | roundRobin/random child as candidate and as fallback | mean with aggregation kind and measured/dispatchable counts; no concrete next target reported |
| R15 | `ListBalancers` while passes are being published | all balancers of one response carry the same generation |
| R16 | 100 connections dropped by an unavailable balancer | counter 100 and last reason in threshold_decision; dispatcher log lines at info level, none at warning (D14) |

## X. Rollout (owned by xray-config; listed for traceability)

| ID | Check | Required result |
|---|---|---|
| X01 | new Core with unchanged configurations | default regression passes before any thresholdLeastPing configuration |
| X02 | staged candidate and installed Core against the new configuration | candidate `run -test` accepts; installed Core rejects |
| X03 | after activation | RPC readback of exact maxRTT/recoverRTT for every thresholdLeastPing balancer |
| X04 | after activation | affected business balancers leave pending and carry traffic within the interruption budget; admin-only success fails the check |
| X05 | `switch revert` | paired restore verified; a binary-only downgrade is not accepted as restore |

## Consumers (separate owners)

Sidecar: fields passed through `/xray-debug.balancers`, absent from an older Core, never reconstructed from
configuration. Portal: browser checks at desktop and mobile widths for limit, band, RTT, latch, degraded,
pending, terminal, fallback, override, observation age and drop count; a member mean shown apart from a
deterministic choice's RTT; raw observatory alive shown separately.
