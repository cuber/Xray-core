# Core Specifications

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
| [006](006-user-domain-traffic/spec.md) | Bounded user/domain traffic and consumer contract |
| [007](007-core-regression/spec.md) | Regression infrastructure and shared fixes |
| [008](008-anytls-inbound/spec.md) | AnyTLS inbound, routing, statistics and control API |

Each implementation commit owns its corresponding numbered spec; AnyTLS remains
last. 001 is the cross-feature index, not a separate runtime feature.
Identifiers are retained from the original inventory for traceability.

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

The documentation-bearing series is `refactor/spec-owned-docs-20260927`.
The untouched pre-migration series is preserved as
`backup/spec-final-pre-docs-20260927` at `cea5d5a1`. See the
[migration record](001-core-fork-contracts/document-migration.md) for invariants.
