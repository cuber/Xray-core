# Register: Fork Lines

- Spec: [spec.md](spec.md). Procedures: [sop.md](sop.md).
- Each generation adds a row when it opens and changes its status at promotion or abandonment. Rows are never removed.

## Generations

| Generation | Core line | quic-go line | Status |
|---|---|---|---|
| 2026-11-07 | `fork/2026-11-07/0888beb38a9b` | `fork/2026-11-07/184d081eef3e` | current since 2026-11-07 |

## Fork points

| Line | Upstream branch | Fork point |
|---|---|---|
| `fork/2026-11-07/0888beb38a9b` | `XTLS/Xray-core` `main` | `0888beb38a9b`, committed 2026-10-14 11:05 UTC |
| `fork/2026-11-07/184d081eef3e` | `apernet/quic-go` `v0.61.0-mod-rename` | `184d081eef3e`, committed 2026-08-06 01:09 UTC; quic-go `v0.61.0` plus apernet's 7 commits |

## Notes

- **Core 2026-11-07.** This line is the former `develop`, renamed with its history.
  - Its own series began as the candidate `rebase/main-20261014` on `0888beb38a9b`. That candidate was promoted on
    2026-10-17 by an `ours` merge, `25786bbe`
    ([spec010](../010-upstream-main-upgrade/main-promotion-20261017.md)).
  - The merge's second parent `1fd51943` is the last state of the series before it. That series was based on
    `b4f08981becb` (2026-04-25) and had no `fork/` name. It stays reachable from this line.
  - `git log --first-parent 0888beb38a9b..fork/2026-11-07/0888beb38a9b` lists this line's own commits. Without
    `--first-parent`, the 19 commits of the earlier series also appear.
  - Later lines are linear.
- **quic-go 2026-11-07.** This line is the former `v0.61.0-cuber`, at the base itself with no patches yet. apernet's
  `v0.61.0-mod-rename` points at the same commit.
- **Before this convention.** `backup/develop-before-*` hold states of `develop` that in-place rewrites replaced
  (2026-09-27, 2026-10-14, 2026-10-20). They are not ancestors of any line.
