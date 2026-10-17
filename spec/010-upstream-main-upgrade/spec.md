# Spec010: Fixed Main Upgrade

- Status: Implemented and locally verified within the recorded source/platform
  boundaries. The user authorized canonical mainline adoption on 2026-10-17;
  this does not complete unrun public-resource/provider/Windows or long-soak gates.
- Fixed upstream: `0888beb38a9ba45440557175e71ce21cf8ba3b13`; original source
  fork `1fd51943` (runtime equal to audited `baf3672e`).
- Promoted from Core draft002, whose ID stays reserved. Current Core source is
  canonical develop tracking cuber/develop, not a parallel authoritative draft.
- Read [promotion](main-promotion-20261017.md), [current validation](current-validation.md),
  [implementation ledger](implementation.md), [plan](plan.md), [tests](tests.md)
  and [checklist](checklists/requirements.md) before changing this contract.

## Scope And Ownership

Own the fixed-main review, integration of fork features and source-bound
cross-protocol/consumer/build/adoption decision. Do not become the permanent
owner of every protocol implementation. The current SS2022 safety contract is
[Core011](../011-ss2022-safety/spec.md); feature specs002-009 remain authoritative.

## User Scenarios

- US-001: Fixed base/fork/upstream inventories identify all differences and
  overlaps, separating endpoint diff from an additive patch.
- US-002: SS2022 handshake/session safety remains correct across this upgrade;
  Core011 supplies the enduring contract and its independent tests.
- US-003: AnyTLS, UDS/listeners, users, routing/scheduling, OB and ordinary and
  optional domain statistics survive the fixed-main integration.
- US-004: Live heap, sockets/workers, RSS and THP are measured separately;
  neither a restart nor lower RSS alone establishes a leak repair.

## Requirements

IDs retain their original meanings; the [original wording](initial-investigation-20261004.md#requirements)
and current provider mapping remain traceable.

- FR-001/002: Pin reviewed objects and inventory all diffs/overlaps with hashes;
  distinguish missing/equivalent/partial/breaking/conditional/unreviewed changes.
- FR-003: SS2022 handshake/clear/session safety delegates to Core011, not a
  duplicate editable implementation contract in this upgrade spec.
- FR-004/005: Preserve protocol/user routing, ordinary counters, optional
  DomainTraffic and feature contracts; shared API/proto changes require actual
  Sidecar/TUI consumer verification.
- FR-006: Security/correctness changes require baseline-red/fix-green reproducers,
  positive/negative/boundary coverage and race checks.
- FR-007: Geodata/GC keeps live matchers and semantics; cleanup cannot remove a
  replacement under the same key.
- FR-008: THP diagnosis uses a separately declared process-local A/B. No global
  THP change, runtime flag or relaxed memory cap is implied by this source merge.
- FR-009: finalRules, transport, SS engine, Hy2, toolchain/dependencies and clients
  need verified compatibility before integration/adoption is accepted.
- FR-010: Live rollout remains explicit through xrayctl with exact paired restore;
  source adoption does not authorize arbitrary nodes or upstream release zips.

## Acceptance And Evidence

SC-001/002/003 retain reproducible inventory, source-based selection and honest
equivalence/verification requirements. SC-004's SS provider gates delegate to
Core011 plus cross-client integration here. SC-005's original draft-location
migration is historical and complete; it is not a new implementation backlog.

Read [the current gate map](current-validation.md) for U/V/P requirements.
Original `tasks.md`, protocol plan, source freezes, dated reports, archived logs
and checksum files remain unchanged. Public RSS/UDP FAILs and opt-in/platform
SKIPs are not waived by documentation, later diagnostics or limited stability.

## Historical Investigation

The old Draft/no-fetch/no-adoption text, cached-203-commit counts, shortlist and
initial FR/SC wording moved to
[initial-investigation-20261004.md](initial-investigation-20261004.md), at the same
directory depth to preserve its relative references. It is historical, not a
second current spec. [Promotion](main-promotion-20261017.md) records the later
explicit adoption authority and both retained Git histories.

No new runtime, configuration, dependency, protocol or deployment change is
introduced by this 2026-10-17 documentation consolidation.
