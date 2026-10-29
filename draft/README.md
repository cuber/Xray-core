# Drafts

Undecided designs live here, separately from implementation specifications.
Draft IDs increase independently within this repository, starting at 001.
Do not derive them from spec IDs or another repository's IDs. Existing IDs remain
stable. When promoting a draft, allocate the next local spec ID, move its
documents and record the mapping here; never keep two authoritative copies.

| ID | Status | Proposal |
|---|---|---|
| 002 | Promoted 2026-10-14; ID reserved | [spec010: fixed-main upgrade](../spec/010-upstream-main-upgrade/spec.md) |
| 003 | Promoted 2026-10-22; ID reserved | [spec013: sing-mux inbound multiplexing](../spec/013-sing-mux-inbound/spec.md); consumer [Sidecar draft001](../../xray-sidecar/draft/001-entry-multiplex/spec.md) stays a draft |
| 004 | Promoted 2026-10-30; ID reserved | [spec015: observatory force down](../spec/015-observatory-force-down/spec.md); consumers [Sidecar spec015](../../xray-sidecar/spec/015-cdt-quota-guard/spec.md), [xray-config spec016](../../xray-config/spec/016-ahk-cdt-quota/spec.md) |

On 2026-10-05 the user explicitly assigned this proposal draft002 and requested
its installation in the canonical Core repository, not a cache clone. The earlier
[declarative node configuration draft001](../../xray-config/draft/001-declarative-node-config/spec.md)
remains owned by xray-config; its documents are not copied here. This explicit
allocation does not establish a shared cross-repository counter. Core draft001
has no allocated proposal; the next Core draft is 005.
