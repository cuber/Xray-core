# Feature Specification: Fork Lines

- Feature ID: 023-fork-lines
- Created: 2026-11-07
- Status: Approved (2026-11-07; user: "那就把 develop/ 改成 fork/ 核心记录一个分叉点信息"; "fork/* 是核心迭代分支吧，单搞一个
  develop 没啥意义吧，因为未来我们会 rebase 啊"; "日期我们就用今天来命名吧，后续每次 rebase 升级，两个 fork 仓库都重新拉分支，日期对齐";
  "顺带把这个fork规范落成spec或者sop"; "然后fork的默认分支改成我们最新的 fork")
- Baseline: Core `431e0868` on `develop`, fork point `0888beb38a9b`; quic-go `v0.61.0-cuber` at `184d081eef3e`
  ([spec022](../022-quic-go-fork/spec.md))
- Procedures: [sop.md](sop.md). Register of lines: [lines.md](lines.md).

## Problem and Goal

We maintain two forks:
- `cuber/Xray-core`, whose upstream is `XTLS/Xray-core` `main`;
- since spec022, `cuber/quic-go`, whose upstream is `apernet/quic-go` with one `vX.Y.Z-mod-rename` branch per
  release.

Each fork moves to a new upstream commit by rebase, which rebuilds our patches as a new linear series with new
hashes. A fixed branch name such as `develop` survives that in only two ways. One is a force-push. The other is an
`ours` merge, as in Core's 2026-10-17 promotion
([spec010](../010-upstream-main-upgrade/main-promotion-20261017.md)). Either way, the name never says where the series
starts. Earlier in-place rewrites of `develop`, on 2026-09-27 and 2026-10-14, needed `backup/develop-before-*`
branches to keep built commits reachable.

The two repositories also followed different conventions:
- branch names: `develop` against `v0.61.0-cuber`;
- remotes: Core used `origin` for upstream, quic-go used it for our fork.

Goal: one convention for both forks.
- Each series of our patches lives on its own branch, named after the day we opened it and the upstream commit it
  starts from.
- Branches are only appended to.
- Every upgrade opens a new pair of branches on the same date.
- Pins stay commit hashes.

## Contract

- **L1 Repositories and remotes.**

  | Fork | Upstream | Checkout |
  |---|---|---|
  | `github.com/cuber/Xray-core` | `XTLS/Xray-core`, `main` | `../xray-core` |
  | `github.com/cuber/quic-go` | `apernet/quic-go`, `vX.Y.Z-mod-rename` | `../quic-go` |

  In both checkouts `origin` is upstream and `cuber` is ours.
- **L2 Line name.** `fork/<YYYY-MM-DD>/<hash>`.
  - `<YYYY-MM-DD>` is the date, Beijing time, on which the line's generation was opened (L3).
  - `<hash>` is the first 12 hex digits of the fork point. The fork point is the upstream commit the line starts
    from: the merge-base of the line and its upstream branch.
  - A name is given once and never changes. Upstream's own date is read from the hash (`git log -1 <hash>`).
- **L3 Generations.**
  - An upgrade in either fork opens a new generation, and both repositories open a new line on the same date. An
    upgrade is a rebase onto a new upstream commit, or any rework of the patch history.
  - A fork whose upstream does not change opens its new line at the head of its previous line: same hash, new date,
    no rebase.
  - A date holds at most one generation.
  - The two lines of a generation go together: Core's `replace` on line D names a commit on quic-go's line D (L8).
- **L4 Life cycle.** A line moves through open, candidate, current and frozen.
  - **Candidate.** Tests and the gate run on the new lines while production still builds from the current ones.
    Fixes made on a current line meanwhile are carried to its candidate.
  - **Promotion.** `xray-config/xray-core/line` names the new Core line, and Core's `replace` names a commit on the
    new quic-go line. The previous lines freeze. Each fork's GitHub default branch moves to its new line.
  - **Frozen.** A frozen line takes no commits but stays buildable, so a rollback can return to it.
  - **Abandoned.** A candidate that is never promoted is frozen and marked abandoned in the register.
- **L5 Append only.** A `fork/` branch is never force-pushed, rewritten, renamed or deleted. Every commit a build or
  a pin ever used stays reachable.
- **L6 Pins are hashes.**
  - **Core `go.mod` → quic-go.** A pseudo-version, which carries 12 hex digits, plus the `go.sum` hashes. It is
    looked up from the line's head with `go list -m github.com/cuber/quic-go@<commit>` and never stored as a branch
    name.
  - **Full hashes:** Sidecar (`scripts/check-xray-core-version.sh`), xray-tui (`Makefile`), and xray-config → Auth
    (`xray-auth/revision.txt`).
  - **The only branch selector is `xray-config/xray-core/line`.** It names the Core line that `xrayctl build core`
    builds. The build takes the line's head, and the binary's `vcs.revision` records the commit. The file changes
    only at promotion.
  - **quic-go's current line has no selector.** It is the line that holds Core's replace commit.
- **L7 Other branches.**
  - Upstream branches copied into our forks are left as they are and carry none of our commits: about 180 in quic-go,
    and Xray-core's `main`.
  - Short-lived branches keep their prefixes (`backup/`, `rebase/`, `fix/` and others), and new ones use
    `YYYY-MM-DD` dates. Existing names stay.
  - `develop` (Core) and `v0.61.0-cuber` (quic-go) became the first lines on 2026-11-07 and are retired.
- **L8 Build checks.** `xray-core/build.sh` in xray-config refuses to build when:
  - the line file is missing or does not hold one line name;
  - the checkout is not on that line, or the line does not track `cuber/<line>`;
  - the source `go.mod` replaces `github.com/apernet/quic-go` with `github.com/cuber/quic-go`, and the replace commit
    is on no `cuber/fork/<same date>/*` branch of `../quic-go`.

  An explicit `XRAY_CORE_BRANCH`, used for isolated candidate builds, skips the line file and the tracking check.
  The quic-go check still applies when that branch is a `fork/` line.

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | Both checkouts have `origin` = upstream and `cuber` = ours | `git remote -v` |
| FR-002 | Generation 2026-11-07, both lines pushed and tracked, `cuber/develop` and `cuber/v0.61.0-cuber` deleted. Core: `fork/2026-11-07/0888beb38a9b` continues `develop`'s history from `431e0868`. quic-go: `fork/2026-11-07/184d081eef3e` at `184d081eef3e` | `git ls-remote` |
| FR-003 | `xray-config/xray-core/line` names the Core line, and `build.sh` enforces L8 | unit (`test/xrayctl/test_xrayctl_build.py`) |
| FR-004 | The rename changes no pin: the Core `go.mod`/`go.sum`, Sidecar, xray-tui and Auth hashes stay as they are | diff |
| FR-005 | Current-state documents name the line, not `develop`. xray-config: AGENTS rule 23 and repository table, `README.md`, `xray-core/README.md`, `docs/server/paths.md`, stability-check, `.codex/COMPACT_CONTEXT.md`. Core: spec001 FR-012, spec021, spec022 | review |
| FR-006 | The register [lines.md](lines.md) lists every line with its fork point and status | docs |
| FR-007 | Each fork's GitHub default branch is its current line: `fork/2026-11-07/0888beb38a9b` in `cuber/Xray-core`, `fork/2026-11-07/184d081eef3e` in `cuber/quic-go` | `gh repo view cuber/<fork> --json defaultBranchRef` |

## Control Plane

No new runtime state (CP-1). This spec names branches and selects what the build wrapper builds. A binary's identity
is still its `vcs.revision`, which the Portal shows, and `go version -m` still names the quic-go replacement.

## Non-goals (2026-11-07)

- Renaming historical branches (`backup/`, `rebase/`, `promotion/`, `refactor/`, `cube-develop`).
- Deleting upstream branches copied into the forks.
- Checks in Sidecar and xray-tui that their pinned Core hash is on the current line.
- Auth, which is not a fork.
- The quic-go fork's own spec-kit (spec022 T007). It will link the register here, not keep a copy.
