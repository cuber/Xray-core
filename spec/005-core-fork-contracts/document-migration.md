# Documentation Ownership Migration

Date: 2026-09-27. This is a documentation/history operation, not a runtime change
or a new execution of the historical acceptance suite.

## Preserved Inputs

- Original series tip: `cea5d5a14e6b36de6177786fd16433c8635ae06e`.
- Base: `b4f08981becb71eaa995fa98ed2098ade92566bb`.
- Backup ref: `backup/spec-final-pre-docs-20260927`.
- Destination: `refactor/spec-owned-docs-20260927`, isolated worktree
  `/Volumes/Linux/opensource/cuber/xray-core-spec-docs`.
- Original `develop` and `refactor/spec-final-20260927` are not moved.
- The documentation staging worktree `xray-core-spec-final` is left intact.

| Spec | Original Implementation | Documentation Added |
|---|---|---|
| 006 | `3ddd5b5c` | 006, spec README and top-level spec entry |
| 007 | `0c9b2d16` | 007 |
| 008 | `13a46cc5` | 008 |
| 009 | `13e82315` | 009 |
| 010 | `d220a582` | 010 |
| 011 | `5126d1af` | 011 |
| 012 | `cea5d5a1` | 012, shared 005 index/evidence, local AnyTLS README link |

Each new commit keeps its original implementation's tree, overlays the cumulative
owned documentation and retains the original author and subject. The commit body
records `Original-Implementation` with the full original hash, so mapping does
not require embedding a commit's own circular hash in its tree. Use:

```sh
git log --reverse --format='%h %s%n%b' b4f08981..refactor/spec-owned-docs-20260927
```

## Verification Contract

Exactly seven non-merge commits, 006 through 012, must follow the same base.
Each numbered directory first appears in its own implementation commit. 005 is
a shared document attachment in the final commit; it is not an eighth feature.
Final index links may refer forward before the complete series is applied.

For every pair of original/new commits, Git tree comparison must have no
differences outside `spec/`, `README.md` and `proxy/anytls/README.md`.
The top-level README exception is solely the requested spec entry. At the final
tip this proves all runtime source, tests, dependency files, workflows and other
tracked files are identical to `cea5d5a1`. No source build is required by this
documentation-only change, and no fresh runtime-test or binary-build result is
claimed. Historical build/test hashes remain untouched.

The local migration verifies seven-commit order, per-commit spec ownership,
runtime tree identity, local Markdown link targets and whitespace. It does not
push, deploy, move develop, modify consumer pins, or edit xray-config files.

## Local Results

The reconstruction checked all seven original/new runtime trees with Git's
`diff --exit-code`, checked every corresponding spec addition, and confirmed
exactly seven commits ending with AnyTLS. All checks passed. The completed
documentation set has 199 local Markdown link targets checked without missing
files; Git whitespace validation passed. These are documentation/history checks,
not a fresh run of the historical Go suite or build wrapper.
