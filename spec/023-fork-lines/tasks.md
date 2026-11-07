# Tasks: Fork Lines

- [x] T001 Spec, plan, SOP and register; index row in `spec/README.md`.
- [x] T002 Local renames: Core `develop` → `fork/2026-11-07/0888beb38a9b`; quic-go remotes (`origin` = apernet,
  `cuber` = ours) and `v0.61.0-cuber` → `fork/2026-11-07/184d081eef3e` (FR-001, local half of FR-002).
- [x] T003 xray-config: `xray-core/line`, `build.sh` checks (L8) and their tests (FR-003).
- [x] T004 Current-state documents in xray-config and Core (FR-005); spec022, spec021 and spec001 updated.
- [x] T005 Commits, with the user's consent.
- [ ] T006 Pushes of both lines (FR-002).
- [ ] T007 `xrayctl build core` on the line: `vcs.modified=false`, quic-go check passes (with spec022 T006).
- [ ] T008 GitHub default branches follow the current lines (FR-007).
- [ ] T009 Deletion of the old remote names `cuber/develop` and `cuber/v0.61.0-cuber`, with the user's consent (FR-002).
- [ ] T010 GitHub ruleset that blocks force-pushes and deletion of `fork/*`, with the user's consent.

## Evidence

- 2026-11-07 T002:
  - `git merge-base --all develop origin/main` gives only `0888beb38a9b`, committed 2026-10-14 11:05:45 UTC.
    `b4f08981becb` is its ancestor, and `1fd51943` (the promotion merge's second parent) is reachable from `develop`.
  - `git worktree list`: `develop` was checked out only in the main checkout.
  - After the renames:
    - Core is on `fork/2026-11-07/0888beb38a9b`, still tracking `cuber/develop`;
    - quic-go is on `fork/2026-11-07/184d081eef3e` at `184d081eef3e`, still tracking `cuber/v0.61.0-cuber`, with
      remotes `cuber` = `git@github.com:cuber/quic-go.git` and `origin` = `https://github.com/apernet/quic-go.git`.
  - GitHub default branches before the change: `cuber/Xray-core` `main` (2026-03-03), `cuber/quic-go` `master`
    (2025-04-20). `develop` was never the default.
  - `go list -m github.com/cuber/quic-go@v0.61.0-cuber` failed with "v0.61.0-cuber is not a tag": Go reads that
    name as a semantic version. `GOPROXY=direct go list -m github.com/cuber/xray-core@rebase/main-20261014` resolved a
    branch name with slashes to `v0.0.0-20261016075813-9a41135b87af`.
- 2026-11-07 T003:
  - `python3 test/run.py xrayctl --pattern 'test_xrayctl_build.py'`: 6 tests OK. The two new tests cover a malformed
    line file, a line without upstream or tracking another ref, the line tracking `cuber/<line>`, a missing quic-go
    checkout, and a quic-go commit only on another generation's line, then on the same generation's.
  - `python3 test/run.py xrayctl`: 1350 tests OK.
- 2026-11-07 T004: xray-config AGENTS rule 23, the Validation paragraph, the repository table (adds `cuber/quic-go`)
  and the Portal pin note; `README.md`, `xray-core/README.md`, `docs/server/paths.md`, stability-check and
  `.codex/COMPACT_CONTEXT.md`. Core spec001 FR-012, spec021, spec022 and the index.
- 2026-11-07 T005: Core `build(spec022, spec023)` and `spec(021)`; xray-config `build(core): fork lines`, with the user's
  consent ("提交").
