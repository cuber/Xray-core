# Core Specifications

## Numbering

This repository allocates its own IDs independently of other repositories.
`spec/` and `draft/` are separate sequences: use the highest allocated ID in
the same repository and directory plus one (001 when empty). Do not reuse retired
IDs or consult another repository's counter. IDs stay stable after this migration.
Cross-repository references include repository and directory, not a bare number.
Promotion from draft assigns the next spec ID; deferral assigns the next draft
ID. Record the source-to-destination mapping and reserved old ID in the index,
update links, and move rather than duplicate the authoritative documents.

This repository owns the contracts, implementation plans and verification
evidence for its fork-specific features. They do not depend on the configuration
repository to define Core behavior. Deployment records and consumer tests may
reference sibling repositories as integration evidence, not implementation inputs.

| ID | Contract |
|---|---|
| [001](001-core-fork-contracts/spec.md) | Fork inventory, reconstruction and acceptance index |
| [002](002-core-connection-lifecycle/spec.md) | Dispatch and Hy2 connection lifecycle |
| [003](003-core-listeners-uds/spec.md) | Multi-address listeners and Unix sockets |
| [004](004-core-observatory/spec.md) | Probe groups, URLs and health recovery |
| [005](005-core-routing-balancing/spec.md) | Routing control API and weighted scheduling |
| [006](006-user-domain-traffic/spec.md) | Core user/domain recording, bounded buckets, cursor and RPC; aggregation belongs to Sidecar007 |
| [007](007-core-regression/spec.md) | Regression infrastructure and shared fixes |
| [008](008-anytls/spec.md) | AnyTLS inbound/outbound, routing, statistics and control API |
| [009](009-direct-control-listener/spec.md) | Direct control-plane UDS listener without traffic inbounds/routes |
| [010](010-upstream-main-upgrade/spec.md) | Fixed upstream integration, verification and mainline promotion; node rollout remains explicit |
| [011](011-ss2022-safety/spec.md) | Permanent SS2022 authentication, handshake, UDP/session and resource safety contract |
| [012](012-dns-outbound-servers/spec.md) | DNS servers restricted to named outbounds for `targetStrategy` lookups; per-server caches, strict isolation; read-only `DNSService` |
| [013](013-sing-mux-inbound/spec.md) | Opt-in per-inbound sing-mux server for mihomo/sing-box multiplexing; stream parity, bounds, revocation, read-only `GetInboundMuxStats` (promoted from draft003) |
| [014](014-observatory-track/spec.md) | Burst observatory `track`: an outbound's observation follows another outbound's probe, so balancers fail over on one probe per path; read-only `ObservatoryService.GetTrack`, Portal display (approved 2026-10-29, no draft) |
| [015](015-observatory-force-down/spec.md) | Burst observatory force down: a control-plane-owned runtime set of probes that balancers treat as dead (and their `track` followers); `OutboundStatus.force_down*`, `ObservatoryService.Get/SetForceDown`; probing and measured health unchanged (promoted from draft004, approved 2026-10-30) |

The 2026-10-17 mainline review keeps feature boundaries stable rather than
splitting AnyTLS inbound/outbound or merging traffic listeners003 with direct
API009. Their ownership, rollback and verification differ. Spec007 remains the
shared regression/fix owner, not one numbered spec per narrow bug. Spec010's
current contract is now separated from its
[initial investigation](010-upstream-main-upgrade/initial-investigation-20261004.md);
historical Draft/auth restrictions do not override later explicit adoption.

The verified `rebase/main-20261014` runtime is promoted to `develop` on
2026-10-17; see [promotion and code-identity boundaries](010-upstream-main-upgrade/main-promotion-20261017.md).
The original candidate worktree and its exact rollback bundles remain frozen.
Current feature contracts are maintained by their
owning specs; dated acceptance remains bound to its tested revisions. Candidate
integration/release progress is recorded in [spec010](010-upstream-main-upgrade/implementation.md),
not inferred from the imported source fork's historical PASS results.
The consumer half of former Core006 is now
[Sidecar007](../../xray-sidecar/spec/007-domain-traffic-aggregation/spec.md).
Spec010 tracks upstream migration and integration; spec011 owns the lasting
SS2022 safety contract. Existing requirement IDs and frozen evidence stay stable.

Each implementation commit owns its corresponding numbered spec; a spec may have
multiple cohesive increments. Shared prerequisites precede their consumers, and
research-only draft commits remain separate from runtime implementation.
The [2026-10-14 split](001-core-fork-contracts/history-split-20261014.md) records
the recent six-to-ten increment mapping and unchanged runtime-tree proof.
AnyTLS remains
last in the original 001-008 reconstruction. Later features allocate new IDs.
001 is the cross-feature index, not a separate runtime feature.
On 2026-09-27 the user authorized repository-local renumbering: old global
005/006/007/008/009/010/011/012 map to local 001/002/003/004/005/006/007/008.
Historical hashes, requirement IDs, test names and evidence filenames remain
provenance, not allocations in the new sequence. Next spec: 015; next draft: 004.
Core draft002 was promoted to [spec010](010-upstream-main-upgrade/spec.md)
on 2026-10-14 following explicit implementation authorization.

## Control-Plane Parity

Adopted 2026-10-20 (user: "后续所有对core的改动，都需要考虑控制面，你得记录一套规约").
These rules bind every Core spec and change; a spec that does not satisfy them is
incomplete.

- CP-1 Scope: a change that adds or alters configuration, loaded runtime state,
  selection or scheduling decisions, caches, health, sessions or counters defines
  how the control plane observes it, in a `Control Plane` section of its spec. A
  change with no new observable state says so with the reason (for example an
  internal bug fix).
- CP-2 Runtime truth: report what the running instance loaded and does, through
  Core gRPC. Consumers do not reconstruct runtime state from configuration files
  when Core can report it.
- CP-3 Read-only first: observation RPCs have no side effects. A mutating RPC needs
  its own requirement, authorization, failure and rollback semantics.
- CP-4 Placement: extend the service that owns the feature (Handler, Routing,
  Observatory, Stats, Logger); add a service only for a new feature domain, register
  it in `infra/conf/api.go` and `main/distro/all`, and document its `api.services`
  name. Proto changes are additive; field numbers are never renumbered or reused.
- CP-5 Safety and bounds: a new RPC returns no secret the caller could not already
  read (strip URL userinfo; no user keys), bounds its response by configured
  objects, and holds only short read locks that never stall the data plane.
- CP-6 Compatibility: an older Core answers `Unimplemented`; consumers render
  "unavailable", never a guess, an empty list presented as healthy, or file data.
- CP-7 Tests: a contract test calls the RPC through a real gRPC server on a started
  instance, covering a default configuration and the feature's own state.
- CP-8 Consumers: the same spec family names the Sidecar `/xray-debug` field and the
  Portal presentation (in the Sidecar and Config specs), or records them as a dated
  non-goal. Nodes enable a new service through tracked `api.services`, and
  acceptance covers it where the feature is deployed.

Existing specs adopt these rules when they are next changed.

Approved, Implementing, Implemented and Superseded specs live here. Undecided
proposals belong in `draft/`, not in the implementation backlog. Move documents
when promoting or deferring them; do not maintain duplicate authoritative copies.

Read `spec.md`, the plan, tests and tasks before changing a contract. Record
actual test commands, source revisions, failures and skips. A compiled binary,
an existing test file or a deployment is not by itself proof of acceptance.
Runtime changes require targeted tests, full relevant regressions, race checks,
format checks and supported-platform builds. Integration/deployment authority is
separate from implementation approval. Historical paths and hashes in evidence
remain historical facts; they are not commands to reset the current branch.

The 001-008 documents moved from xray-config on 2026-09-27. Core source and
verification code are unchanged by that documentation migration.

## Paths and Historical Evidence

Current Core source paths and Go commands are relative to this repository root.
Sidecar and configuration integration remain in `../xray-sidecar` and
`../xray-config`; xrayctl deployment/build commands run from xray-config, not
from Core. The Core contracts do not make those repositories runtime inputs.

Historical reports retain their original execution cwd, branch names, commit
hashes, commands, timestamps and outcomes. In particular `../xray-core`,
`../xray-core-spec` and `.cache/...` inside past evidence may be relative to the
then-current xray-config cwd. They are historical provenance, not instructions
to switch the current checkout. Research tables describe their dated baseline,
not necessarily today's dependency versions. Migration does not claim tests
were rerun against newly numbered commits. Shared 001 evidence is included with
the final 008 commit; earlier commits intentionally contain forward references
to that final series index and to later specs.

The original seven-commit reconstruction was adopted on local `develop`; later
increments and their current ownership are recorded in the split report above.
The numbering backup is
`backup/develop-before-local-spec-ids-20260927` at `4a93cc18`.
The earlier documentation-bearing series is `refactor/spec-owned-docs-20260927`.
The untouched pre-migration series is preserved as
`backup/spec-final-pre-docs-20260927` at `cea5d5a1`. See the
[migration record](001-core-fork-contracts/document-migration.md) for invariants.
