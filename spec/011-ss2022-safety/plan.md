# SS2022 Safety Ownership Plan

## Scope And Authority

This plan documents the existing candidate implementation; it is not a proposal
to rewrite it. The 2026-10-16 authorization covers documentation ownership only.
Read [the requirements](spec.md), [SAFETY.md](../../proxy/shadowsocks_2022/SAFETY.md)
and [the evidence mapping](validation.md) before changing this contract later.

## Existing Technical Boundaries

| Boundary | Existing owner / mechanism | Requirements |
|---|---|---|
| TCP pre-authentication | `inbound.go`, `inbound_multi.go`, `inbound_relay.go`; level-0 absolute accepted-socket deadline and deferred Close | FR-001 |
| Authenticated transition | `stream.go` and inbound handlers; successful deadline clear before dispatch, authenticated-level transport policy | FR-002/003 |
| Dynamic users | `inbound_multi.go`; serialized admission/removal and key/email uniqueness; production HandlerService fixtures | FR-004 |
| UDP authentication/replay | `packet.go`, `replay.go`, multi-user decoder; authenticated publication and serialized replay admission | FR-005 |
| UDP identity/replies | per-session copied inbound metadata, original-session reply encoder, user/relay-target binding and transport roaming | FR-006 |
| UDP retirement | `replay.go`; cancel before late publication, conditional removal, quiet timer expiry and manager shutdown | FR-007 |
| Outbound/transport | `outbound.go`, `stream.go`; immediate Dial-result ownership, context/timer cleanup, half-close policy | FR-008 |

These are code-location references, not an instruction to edit code in this
task. Shared policy, dispatcher, Stats and HandlerService APIs retain their
owning contracts. Do not add a global socket registry, change wire framing or
hold a session lock across Dispatch to implement a future SS safety increment.

## Attribution And Compatibility

The selected upstream already supplies finite native TCP handshake deadlines
and accepted-socket close ownership. The old-fork/upstream/candidate executable
comparison demonstrates that distinction; do not attribute the upstream repair
to this candidate's additional UDP work. Candidate-specific reproduced defects
include invalid-UDP retained state, shared-context identity corruption and Close
blocked behind Dispatch. The restored outbound regression has older Core007
provenance even though its native adaptation is now owned here.

Keep single/multi-user AEAD validation separate from relay EIH selection. Relay
cannot authenticate arbitrary downstream payloads. Preserve the native short
first-read rejection and distinguish already-authenticated TCP lifetime from
later UDP admission after RemoveUser. Duplicate-key rejection preserves identity
independence; the coordinated Config candidate migration is integration evidence,
not permission to rotate production credentials.

## Documentation Sequence

1. Establish the scope and source-specific status without transferring historical
   tasks or experiments wholesale to a new ID.
2. Map Core010 U-001..003/V-003..005 and Core007 V-032's SS-only portion to the
   new requirements/tests. Keep the old numbers reachable for provenance.
3. Link original dated evidence in place. Do not move/rewrite source manifests,
   compressed logs, summaries, SHA256SUMS or their claimed source revisions.
4. Add current navigation to Core007/Core010. Core010 retains the fixed-main
   integration/release gate, including V-005's cross-client compatibility work.
5. Check local Markdown targets, requirement/test/task coverage and whitespace;
   verify protected evidence bytes against the pre-edit baseline. No Go tests,
   builds, external clients or remote operations are run for this split.

## Historical Freeze Handling

Core010's protocol evidence freezes both `tasks.md` and
`protocol-chain-plan.md`. Keep those files byte-for-byte, including their
unchecked historical alternatives. Its new
[current validation navigation](../010-upstream-main-upgrade/current-validation.md)
provides the live task boundary instead of regenerating a historical checksum.
Core007's original numbered tasks and dated reports likewise retain their
original meaning; only current entry/plan/task navigation changes.

## Future Validation And Release

Future runtime edits need relevant focused tests, race checks, negative controls,
resource oracles and the shared Core gates. Run socket-heavy suites serially.
Pin source/platform/client inputs and keep failures/skips visible. The commands
in [tests.md](tests.md) are existing rerun recipes, not results of this task.

Core010 owns reconciliation with all other fork features and paired consumers.
Official dual-platform builds, candidate adoption, consumer pins and deployment
remain separately authorized operations through the approved xrayctl workflow.
