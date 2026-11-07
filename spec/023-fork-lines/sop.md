# SOP: Fork Lines

- Spec: [spec.md](spec.md). Register: [lines.md](lines.md).
- Paths are siblings under `/Volumes/Linux/opensource/cuber`: `xray-core`, `quic-go`, `xray-config`. In both forks
  `origin` is upstream and `cuber` is ours (L1).
- Pushing, deleting a branch and changing GitHub settings need the user's consent each time.

## 0. Set up a checkout

Every write goes to `cuber`; upstream is only read (L1).

```bash
git remote add cuber git@github.com:cuber/<fork>.git   # Xray-core or quic-go, if missing
git remote set-url --push origin no_push               # a push to upstream fails
gh repo set-default cuber/<fork>                       # gh otherwise targets the fork's parent
```

## 1. Read the state

```bash
cat xray-config/xray-core/line                                    # the current Core line
git -C xray-core for-each-ref refs/remotes/cuber/fork --format='%(refname:short) %(objectname:short=12)'
git -C quic-go   for-each-ref refs/remotes/cuber/fork --format='%(refname:short) %(objectname:short=12)'
grep 'cuber/quic-go' xray-core/go.mod                             # the quic-go pin; its last 12 hex are the commit
```

- **Fork point check.** The hash in the name must equal the merge-base:
  - Core: `git merge-base <line> origin/main`;
  - quic-go: `git merge-base <line> origin/vX.Y.Z-mod-rename`.
- **A line's own patches:** `git log --oneline --first-parent <hash>..<line>`.
- **Two lines side by side:** `git range-diff <old hash>..<old line> <new hash>..<new line>`. It pairs each patch with
  its counterpart and shows what was dropped or changed.

## 2. Daily work

- **Commit and push.** Commit on the current line, then `git push cuber <line>`. Pushes are fast-forward only.
- **"Pull latest"** (xray-config AGENTS rule 23):
  - `git fetch cuber`, then compare the local line with `cuber/<line>`;
  - fast-forward if behind;
  - report anything dirty, ahead or diverged instead of resolving it silently.
- **Back-porting** an upstream commit with `git cherry-pick -x <upstream commit>` stays on the current line. It does
  not change the fork point.

## 3. Open a generation

Do this when either fork moves to a new upstream commit, or when the patch history is to be reworked. Both forks
open their lines on the same date.

```bash
D=$(TZ=Asia/Shanghai date +%F)
```

1. **Core.**
   - With a new upstream: `git fetch origin` and pick the upstream commit `U`. Then
     `git switch -c fork/$D/<U:12> <U>` in a worktree, for example `../xray-core-$D`.
   - Carry the patches spec by spec. The list comes from
     `git log --reverse --first-parent --no-merges <old hash>..<old line>`. Cherry-pick and rework them, keeping each
     commit owned by its spec.
   - Without a new upstream: `git switch -c fork/$D/<old hash> <old line>`, and rework there if that is the point of
     the generation.
2. **quic-go.**
   - With a new upstream: the base `B` is the head of `origin/vX.Y.Z-mod-rename` for the version Core's new upstream
     `require` names. `git switch -c fork/$D/<B:12> <B>`, then carry the patches the same way, following the fork's
     own specs.
   - Without a new upstream: `git switch -c fork/$D/<old hash> <old line>`.
   - Run the fork's tests with Core's Go toolchain.
3. **Push both lines** (consent): `git push -u cuber fork/$D/<hash>` in each fork.
4. **Pin quic-go in the Core candidate.**

   ```bash
   Q=$(git -C ../quic-go rev-parse cuber/fork/$D/<quic-go hash>)  # the line's head
   go list -m github.com/cuber/quic-go@$Q                          # its pseudo-version
   go mod edit -replace=github.com/apernet/quic-go=github.com/cuber/quic-go@<pseudo-version>
   go mod tidy && go mod verify
   ```

   Go also resolves a branch query such as `@fork/$D/<hash>`, but the module proxy may answer with an older head of
   the branch, so these steps look the commit up locally. The `require` keeps upstream's version (spec022 F3). Commit
   on the Core candidate line.
5. **Compare the lines.** Run `git range-diff` for each fork (section 1). Every old patch is carried, reworked or
   dropped with a reason in its spec.
6. **Register.** Add a row to [lines.md](lines.md) with status `candidate`.

## 4. Candidate

- **Build.** The candidate is built in isolation and never into the approved `build/`:

  ```bash
  XRAY_CORE_SRC=../xray-core-$D XRAY_CORE_BRANCH=fork/$D/<hash> XRAY_BUILD_DIR=build/candidate-$D xray-core/build.sh
  ```

  The wrapper still checks that the quic-go pin lies on `cuber/fork/$D/*`.
- **Verify.** Run the Core suite, the quic-go suite and the stability gate (`xrayctl gate plan`, the `stability-check`
  skill).
- **Carry fixes.** A fix committed on a current line meanwhile is carried to the candidate. `git range-diff` shows
  whether anything is missing.

## 5. Promote

1. In xray-config, write the new Core line into `xray-core/line` and commit. From then on, `xrayctl build core` builds
   it.
2. Switch the main checkouts:
   - `git -C xray-core switch fork/$D/<hash>`;
   - in quic-go, the line that holds the new pin.
3. Move pins that follow Core, such as Sidecar's `check-xray-core-version.sh` and xray-tui's `XRAY_CORE_COMMIT`,
   with their next release, as usual.
4. In the register, mark the new generation `current` and the previous one `frozen`, with dates.
5. Move each fork's GitHub default branch to its new line (L4; consent):
   `gh repo edit cuber/Xray-core --default-branch fork/$D/<hash>`, and the same for `cuber/quic-go`.

## 6. Freeze, roll back, abandon

- **Freeze.** A frozen line takes no commits and is never deleted, renamed or force-pushed (L5).
- **Roll back.**
  - The deployed Core: `xrayctl switch revert`.
  - The build source: restore the previous line in `xray-core/line`. A frozen line stays buildable as it was.
- **Abandon.** A candidate that will not be promoted keeps its branches and is marked `abandoned` in the register.
  The next generation takes a later date.
