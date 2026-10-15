# Tasks

Implementation is now authorized on the isolated candidate branch. Track current
phase completion in [implementation.md](implementation.md); unchecked historical
backport alternatives below are not all required for full-main integration.

## Refresh 2026-10-14

- [x] Fetch complete origin/main objects and lock user-selected full SHA.
- [x] Recompute commit/path/config inventory and non-worktree merge preview;
  verify deterministic regeneration with --check.
- [x] Source-review native SS authentication deadline clearing, cached UDP
  identity, allocation lifetime, and Hy2 post-tag fixes; record unresolved tests.
- [x] Map specs002-009 and cross-repository generated proxySettings consumers.
- [x] Inspect compact matcher/weak cache changes; no measured savings claimed.
- [ ] Reproduce source-level SS UDP risks; verify revocation, bounds and identity.
- [ ] Establish upstream runtime baseline and run candidate migration gates.

See [current audit](review-20261014.md). The historical implementation tasks
below are not an instruction to cherry-pick everything after selecting main.

## Research Completed

- [x] T001 [US-001] Restore instructions, owning specs, worktree and approved refs.
- [x] T002 [US-001] Pin cached base/fork/upstream and generate all commit/path/diff evidence.
- [x] T003 [US-002] Contrast SS pre-auth omission with outbound/post-auth upstream fixes.
- [x] T004 [US-002] Identify newer native SS rewrite and follow-up via primary web evidence.
- [x] T005 [US-003] Review urgent candidates, equivalent local fixes and breaking surfaces.
- [x] T006 [US-003] Scan current local node JSON fields/protocols without exporting credentials.
- [x] T007 [US-004] Write phased plan, detailed oracles and memory/THP separation.
- [x] T008 [US-001] Verify reproducible cached inventories and local document links.

## Remaining Research Gates

- [x] T009 [US-001] Fetch/pin current origin/main full objects and refresh the
  complete path/commit inventory on 2026-10-14; all full diffs available from Git.
- [ ] T010 [US-002] Review complete native SS deadline-clear helpers, framing, UDP/session
  cleanup, dynamic-user revocation and dependency graph; depends on T009.
- [ ] T011 [US-004] Review complete newer matcher patches and derive measurable budgets;
  depends on T009; no measured savings claimed yet.
- [x] T012 [US-001] Move the draft, tool and evidence to canonical Core
  draft002 on 2026-10-05; update owning and consumer indexes, remove the old cache
  draft directory, and validate links. No duplicate authoritative Config spec.

## Proposed Implementation, Not Authorized by This Draft

- [ ] T013 [US-002] Add bounded baseline-failing SS/QUIC/DNS/writer regressions;
  V-002..007; declare each actual failure before implementing its repair.
- [ ] T014 [US-002] Adapt narrow SS policy deadline and QUIC guard; V-002..005;
  preserve service/worker ownership and successful long-lived streams.
- [ ] T015 [US-003] Adapt DNS/writer/stats correctness; V-006..008/012/013.
- [ ] T016 [US-003] Adapt router snapshots and cleanup; V-009/012/013.
- [ ] T017 [US-004] Adapt weak matcher cache/registry and benchmark newer compact storage;
  V-010/014; full-source gate for web-only changes remains mandatory.
- [ ] T018 [US-003] Resolve conditional XHTTP/Hy2/burst changes, dependency and config
  migrations only within separately approved scope; V-011/012.
- [ ] T019 [US-003] Run whole-tree/race/external-client/chain regressions and official
  dual-platform builds after local commits; V-013; record failures/skips honestly.
- [ ] T020 [US-004] Authorized canary and 24-48h observation via xrayctl/SOP; V-014;
  deployment requires a separate request, not inferred from T019.

## Historical Draft Result

Research documents and dated evidence are installed in canonical Core draft002;
upstream implementation/deployment remain unchecked. Remote freshness was restored
on 2026-10-14; runtime verification of the SS review remains open. AnyTLS address-adapter adoption
is a separately approved spec008 change, not completion of these backports.

The user subsequently authorized the isolated main-based upgrade. This document
retains the historical backport checklist for provenance; current implementation
and verification status belongs to [implementation.md](implementation.md), not
the preceding draft conclusion. Production adoption remains a separate phase.

## Authorized Protocol Coverage Extension, 2026-10-15

These tasks follow the user's request to close the SS2/VLESS/mixed-chain gaps.
They do not mark unrelated release adoption or historical alternatives complete.

- [x] T021 Add the 16 two-hop/64 three-hop real TCP/UDP matrix, two identities,
  multiple destinations, exact exit bytes and per-hop receipts. Complete normal/
  race r6 passes are in [the protocol ledger](protocol-chain-network-20261015.md).
- [x] T022 Execute TLS trust/SNI positive/negative and VLESS outer-Mux gates in
  normal/race. Earlier oversized-packet and socket-collision failures stay recorded.
- [x] T023 Reproduce observed Darwin UDP return-path misdelivery, add a real-wire
  baseline-failing regression and a narrowly scoped literal-loopback source fix;
  see [spec002](../002-core-connection-lifecycle/system-udp-loopback-source-20261015.md).
- [ ] T024 Execute invalid identities, packet boundaries, cancellation, active
  shutdown, cold/established relay outage, fresh recovery and resource rounds.
- [ ] T025 Reconcile independent client/server interop, native Linux and
  three-Akile sustained/public-chain acceptance with a frozen paired build.
