# SS2022 Safety Requirements Checklist

This checks the documentation contract, not a newly executed runtime release.

- [x] User authorization and existing candidate-only implementation are explicit;
  documentation splitting does not imply deployment or production adoption.
- [x] US-001..003 and FR-001..008 have requirement/test/task mappings.
- [x] Single-user/multi-user/relay deadlines, clear-before-dispatch, zero policy,
  authenticated-level timers, early bytes and deadline errors are specified.
- [x] SIP022 short-first-read rejection is preserved; no invented fragmentation
  repair, retry or timeout extension is proposed.
- [x] Dynamic RPC identities, duplicate-key compatibility change and removed
  user/new-key/B continuity semantics are explicit, including in-flight limits.
- [x] UDP authentication-before-publication, replay, context/reply isolation,
  roaming, cancellation, expiry and non-revival are covered.
- [x] Relay EIH selection does not claim downstream AEAD authentication.
- [x] Outbound early-return Close, normal/TimeoutOnly, timer cleanup, half-close
  and exact resource drain are distinguished from general transport ownership.
- [x] Upstream TCP repair and candidate UDP repairs are attributed separately.
- [x] Original Core007/Core010 IDs, historical global `spec011` names, failed
  attempts and later acceptance checkpoints retain their original meaning.
- [x] Core010 keeps fixed-main integration/build/release gates; Core007 keeps
  public infrastructure and non-SS fixes. No unrelated FRs/tasks move wholesale.
- [x] Dated evidence and compressed/source/hash/manifest artifacts stay in place;
  current task navigation does not edit Core010's frozen tasks/protocol plan.
- [x] Missing independent branch-specific controls and platform/production
  limits are not presented as passed executions.
- [x] Local links/anchors, whitespace and pre/post protected-byte checks pass;
  actual results recorded in [validation](../validation.md).

[Spec](../spec.md) · [Plan](../plan.md) · [Tasks](../tasks.md) · [Tests](../tests.md)
