# Current Validation And Task Boundaries

## Mainline Adoption, 2026-10-17

The user explicitly authorized source adoption after the limited rollout.
Read [promotion](main-promotion-20261017.md) for retained tags/histories and
unchanged runtime source. P12's source-adoption boundary has advanced; full
fleet deployment, public-resource/provider and Windows/long-soak acceptance
have not. Config003 records fresh canonical verification results.

The dated ownership/evidence map below remains useful. Its original no-adoption
statements describe the earlier documentation phase, not a veto on this later
authorization. The earlier investigation is separated from the current spec
in [initial investigation](initial-investigation-20261004.md).

## Authority, 2026-10-16

Core010 remains the fixed-main upgrade difference/integration/release contract.
Read [spec.md](spec.md), [implementation phases](implementation.md),
[local verification](local-verification-20261015.md) and the source-specific
protocol ledgers together. This page changes documentation ownership only; it
does not rerun tests, change an experimental source or approve production adoption.

## SS2022 Delegation

The existing native SS safety implementation is now documented in
[Core011](../011-ss2022-safety/spec.md), with [plan](../011-ss2022-safety/plan.md),
[tests](../011-ss2022-safety/tests.md), [tasks](../011-ss2022-safety/tasks.md) and
[validation/history mapping](../011-ss2022-safety/validation.md).

| Original gate/task | Current ownership |
|---|---|
| U-001..003 | Core011: dynamic UDP identity/revocation, authenticated allocation, replay/isolation/roaming and retirement |
| V-003/004 and FR-003 | Core011: absolute handshake deadline, clear-before-dispatch, normal session policy and SS resource oracles |
| V-005 local safety/control | Core011: SS-specific safety contract; Core010 still requires cross-version/independent-client compatibility and integration |
| P09 SS portion | Core011 supplies contract and recorded results; Core010 owns the phase's fixed-main integration decision |
| Historical Refresh SS risk item, T010, T013/T014 SS portions | Navigate to Core011, not a fresh implementation queue. Keep original unchecked alternatives; QUIC/DNS/writer portions retain their own owners |
| V-014 local SS soak subset | Core011 resource evidence only; whole-server THP A/B and production canary remain separate unclaimed Core010/Config gates |

Core010 keeps U-004..012 integration, V-013 whole-Core/build acceptance,
V-015 protocol-chain/remote compatibility, P10/P11 paired evidence and P12
adoption. Delegation neither completes T025 nor makes a public-chain/resource
failure pass. Core007 retains public regression facilities and its non-SS fixes.

## Frozen Task And Evidence Navigation

[tasks.md](tasks.md) and [protocol-chain-plan.md](protocol-chain-plan.md) are
historical source inputs in the protocol evidence
[SHA256SUMS](evidence/protocol-20261016/SHA256SUMS). Their bytes, checkboxes and
hashes stay unchanged. The original dated task statements are not rewritten to
pretend all historical alternatives passed. Current task ownership is the table
above plus the current phase ledger, not that research checklist in isolation.

Preserve all original dated reports, `.gz` archives, source manifests, checksums
and [execution/source manifest](evidence/protocol-20261016/manifest.json).
Experimental source paths/hashes stay attached to the actual executed revision,
not relabeled as Core011 source or refreshed to a documentation-only HEAD.

## Evidence And Remaining Gates

- SS safety: [Core011 validation](../011-ss2022-safety/validation.md) indexes the
  unchanged [SAFETY.md](../../proxy/shadowsocks_2022/SAFETY.md) and raw summaries,
  including failed first soak, later hour pass, half-close/first-read acceptance.
- Fixed-main baseline: [local verification](local-verification-20261015.md)
  records partitioned source revisions, skips and platform/network limitations;
  it is not a single final-source whole-tree PASS.
- Extended local protocol matrix: [Darwin ledger](protocol-chain-darwin-20261016.md)
  and [protocol evidence](evidence/protocol-20261016/README.md) remain their dated
  sources, counts and result sets. They are not SS-only replacement evidence.
- Paired Linux/public chains: [original T025 boundary](tasks.md) and
  [network execution ledger](protocol-chain-network-20261015.md) retain partial
  execution and resource failures. Core011 does not close these remote gates.
- P12 release adoption, consumer pins, deployment and 24-48h production canary
  remain separate. No code/test/dependency/build/network/SSH/pin/commit/push
  operation is performed for this documentation split.

Current entry points are the [Core spec index](../README.md),
[Core README](../../README.md) and [Core001 ownership map](../001-core-fork-contracts/spec.md).
