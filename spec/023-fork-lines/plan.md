# Plan: Fork Lines

- Spec: [spec.md](spec.md)

## Local renames (no push)

- **Core.** `git branch -m develop fork/2026-11-07/0888beb38a9b`. The branch keeps tracking `cuber/develop` until
  the push sets `cuber/fork/2026-11-07/0888beb38a9b`.
- **quic-go.**
  - `git remote rename origin cuber`, then `git remote rename apernet origin`;
  - `git branch -m v0.61.0-cuber fork/2026-11-07/184d081eef3e`.
- **Fork point.** `0888beb38a9b` is the only merge-base of `develop` and `origin/main` (`git merge-base --all`). The
  previous base `b4f08981becb` is its ancestor.

## xray-config

- **`xray-core/line`** (new): one line, `fork/2026-11-07/0888beb38a9b`.
- **`xray-core/build.sh`:**
  - Without `XRAY_CORE_BRANCH`, it reads the line from `$XRAY_CORE_LINE_FILE` (default `xray-core/line` beside the
    script) and requires a `fork/YYYY-MM-DD/<12 hex>` name.
  - The tracking check moves from `develop`/`cuber/develop` to `<line>`/`cuber/<line>`. It applies only to the
    line from the file, as the `develop` check applied only to `develop`.
  - The quic-go check reads the `replace` of `github.com/apernet/quic-go` in the checked-out `go.mod`. When the
    target is `github.com/cuber/quic-go`, it takes the commit from the pseudo-version. When the branch being built is
    a `fork/` line, it requires a `refs/remotes/cuber/fork/<same date>/*` branch in `$XRAY_QUICGO_SRC` (default
    `../quic-go`) that contains the commit.
  - The wrapper still never fetches, resets or rebases, here or in `../quic-go`.
- **Tests** (`test/xrayctl/test_xrayctl_build.py`), on temporary git fixtures that reach the format gate fixture
  (exit 77) only when every check passes:
  - a malformed line file;
  - a line without upstream, or tracking another ref;
  - the line tracking `cuber/<line>`;
  - a quic-go commit only on another generation's line, then on the same generation's line.
- **Documents:** AGENTS rules 23 and the Validation paragraph, the repository table (adds `cuber/quic-go`) and the
  Portal pin note; `README.md`; `xray-core/README.md`; `docs/server/paths.md`; stability-check; `.codex/COMPACT_CONTEXT.md`.

## Core

- **This spec,** [sop.md](sop.md) and [lines.md](lines.md); index row 023, "Next spec: 024".
- **spec022:**
  - F1 remotes;
  - F2 points to L2–L5;
  - F6 opens a generation per L3;
  - the T002 evidence keeps the original name with a note.
- **spec021:** the line name in its plan and tasks.
- **spec001 FR-012:** the sentence on the quic-go fork.

## quic-go

- No file changes. Its spec-kit (spec022 T007) links the register here.

## Pending the user's consent

- Commits in xray-core, xray-config, xray-sidecar (spec019 docs) and quic-go (none yet).
- Pushes:
  - `git push -u cuber fork/2026-11-07/0888beb38a9b` (Core);
  - `git push -u cuber fork/2026-11-07/184d081eef3e` (quic-go).
- Deleting the old names: `git push cuber --delete develop` (Core), `git push cuber --delete v0.61.0-cuber`
  (quic-go). The commits stay reachable from the new lines.
- GitHub: a ruleset that blocks force-pushes and deletion of `fork/*` in both forks.
- Default branches: they follow the current line (user, 2026-11-07, "然后fork的默认分支改成我们最新的 fork"), so both
  move from their stale upstream copies (`main`, `master`) to the 2026-11-07 lines once those are pushed.

## Tests

- `python3 test/run.py xrayctl` in xray-config.
- After the pushes and commits: `xrayctl build core` builds `fork/2026-11-07/0888beb38a9b` with
  `vcs.modified=false`, and the quic-go check finds `184d081eef3e` on `cuber/fork/2026-11-07/184d081eef3e`. This is
  also spec022 T006.

## Rollback

- Before the pushes: rename the branches and remotes back, and revert the files.
- After the pushes: the convention is additive on GitHub. The old names can be pushed again at the same commits.
