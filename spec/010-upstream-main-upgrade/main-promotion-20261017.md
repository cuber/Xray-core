# Develop Promotion, 2026-10-17

The user explicitly authorized mainline adoption after the limited rollout.
The annotated and pushed tag `pre-main-upgrade-20261017` preserves original
develop1fd51943d81ed297636581724f6f2ebeaa50f62a. The verified runtime source is
candidate9a41135b87aff371e6181ba25cc599217574da55. No new upstream fetch/rebase
or runtime implementation change is part of this promotion.

Old develop and the rebased candidate are divergent histories. An ordinary
three-way merge would reintroduce superseded implementations (including the
removed singbridge tree) and conflict with their tested replacements. Instead,
the temporary promotion branch starts at the candidate, adds only these current
documentation pointers, and merges old develop using Git's ours strategy.
Develop then fast-forwards to that merge. Both histories remain ancestors;
neither the old develop nor the candidate is rewritten or force-pushed.

The promotion must pass an exact diff against9a41135 for all paths except
spec documentation. In particular protocol/runtime sources, generated proto,
go.mod/go.sum and format/build gates must be byte-identical. The canonical
develop must continue tracking cuber/develop. Consumer Core pins must reference
the final promotion merge SHA, not an uncommitted tree or the old mainline SHA.

Existing runtime acceptance remains tied to the recorded candidate source;
it is not relabeled as a new run or a Windows runtime PASS. Fresh Darwin/Linux
builds and integration checks run from the canonical sibling checkouts. The
configuration repository records the final merge SHAs, exact runtime-tree
comparison and cross-component results under Config003's
`main-promotion-20261017.md`. This code merge does not itself deploy any node,
replace local running Xray, alter DNS or invalidate the retained canary rollback
bundles/worktrees. Windows runtime and long-horizon/resource residuals retain
their existing documented boundaries.
