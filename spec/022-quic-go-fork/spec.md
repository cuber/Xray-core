# Feature Specification: quic-go Fork Dependency

- Feature ID: 022-quic-go-fork
- Created: 2026-11-07
- Status: Approved (2026-11-07; user: "确认，先把仓库和依赖搞好，独立 quic-go 先做一个 xray-core 的 spec"; "quic-go 也要使用
  spec-kit 不然后续 rebase 升级就无从下手了")
- Baseline: Core `431e0868` (develop), `github.com/apernet/quic-go` `v0.61.1-0.20260806010916-184d081eef3e`
- Current pin (2026-11-07): `0162af63`, the head of the fork's spec001 series for [spec021](../021-quic-loss-recovery/spec.md).
  FR-001 and FR-002 describe the first pin, at the base itself.
- Consumers: [spec021](../021-quic-loss-recovery/spec.md), the first patch series to land on the fork. The fork's own
  specifications (its `spec/`, set up after this spec) own every patch on it.

## Problem and Goal

Core spec021 needs changes inside quic-go: timer constants that `quic.Config` cannot reach, a send path for copies,
and loss counters that do not depend on the congestion controller. Core cannot patch a dependency in place. A fork
needs a home, a pin, a provenance trail and an upgrade procedure, and all of these outlive any single feature on it.

Goal: Core builds against our fork through a `replace`. The first pin is the exact commit Core pins today, so the
switch changes no code. Patch series land on the fork later, each under a specification of its own, and stay
rebaseable onto new upstream commits.

## Contract

- **F1 Repository.**
  - `github.com/cuber/quic-go` is a public GitHub fork of `apernet/quic-go`. GitHub now lists the parent as
    `HyNetworks/quic-go`; the old name redirects, and the module path stays `github.com/apernet/quic-go`.
  - The local checkout is the sibling `../quic-go`, with remotes `origin` (upstream apernet) and `cuber` (the fork),
    as in Core's checkout ([spec023](../023-fork-lines/spec.md) L1).
- **F2 Branches.** Upstream is not `master`. apernet keeps its line on one branch per quic-go release,
  `vX.Y.Z-mod-rename`: the upstream quic-go tag plus apernet's patches (the module rename to
  `github.com/apernet/quic-go`, the Hysteria changes, the Chrome parrot and others; 7 commits on `v0.61.0`).
  apernet's `master` is a stale mirror of quic-go (2025-04-20, module `github.com/quic-go/quic-go`).
  - Ours follow [spec023](../023-fork-lines/spec.md): each line is `fork/<date>/<hash>`, opened together with
    Core's line of the same date. With a new upstream it starts at the head of apernet's `vX.Y.Z-mod-rename`
    (the hash). It carries our patches linearly, each owned by one of the fork's specifications. The first is
    `fork/2026-11-07/184d081eef3e`, the former `v0.61.0-cuber`.
  - Lines are only appended to, never rewritten, renamed, force-pushed or deleted (spec023 L5). Every commit a
    Core build ever pinned stays reachable, so an old Core can be rebuilt.
  - The fork's `master` and the other branches copied from upstream are left as GitHub copied them and carry none
    of our commits.
- **F3 Pin.**
  - `go.mod` keeps `require github.com/apernet/quic-go <upstream version>` as the record of the upstream base, and
    adds `replace github.com/apernet/quic-go => github.com/cuber/quic-go <pseudo-version>` naming a commit on the
    quic-go line of Core's generation (spec023 L3). The pin is a commit, never a branch name (spec023 L6).
  - The fork's base commit equals the commit the `require` names. The first pin is that commit itself, with no
    patches.
- **F4 Identity.**
  - `go version -m build/xray-linux-amd64` lists the replacement after the upstream version.
  - The build wrapper, `vcs.modified=false` and Core's own `vcs.revision` (what the Portal shows) are unchanged.
- **F5 Availability.**
  - The fork is fetched like any public module: through the default proxy and checksum database, with its hashes
    in `go.sum`.
  - `go mod verify` passes. No `GOPRIVATE`, `GOFLAGS` or vendored copy is needed.
- **F6 Upgrade.**
  - Every new generation (spec023 L3) opens a quic-go line. When an upstream integration (spec010) raises
    `apernet/quic-go` to a new `vX.Y.Z-mod-rename` head, the line starts there and our patches are carried over
    spec by spec ([spec023 SOP](../023-fork-lines/sop.md) section 3), with the fork's tests passing. Otherwise the
    line starts at the head of the previous one. The old line stays as it is.
  - The same Core change raises the `require` and moves the `replace` to the new head, so the two never name
    different bases.
  - An upstream merge that rewrites `go.mod` must not drop the `replace`; a guard test fails if it does.
- **F7 Rollback.** Removing the `replace` returns to upstream's module at the same base. With only the base pinned,
  nothing changes. Once patches land, rolling back also removes their features, and their specifications say what
  that means.

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | The `replace` pins the fork at the base commit. `go mod verify`, `go build ./...` and the tests of every QUIC user (Hy2, splithttp HTTP/3, masque, DNS over QUIC) pass | local |
| FR-002 | The module source at the pin is byte-identical to upstream's at the base: the extracted module trees of `cuber/quic-go` and `apernet/quic-go` at that commit compare equal file by file | local |
| FR-003 | A guard test fails when `go.mod` lacks the `replace` to `github.com/cuber/quic-go` | unit |
| FR-004 | `xrayctl build core` produces `vcs.modified=false` and `go version -m` names the replacement | build |
| FR-005 | Spec001 FR-012 records the forked dependency and points to this spec; the index row is added | docs |

## Control Plane

No new runtime state (CP-1). The dependency's source changes, but the code it builds does not. The replacement is
visible in the build information (`go version -m`). Core's version reporting reads `vcs.revision` and is
unchanged. Features that change behavior on the fork define their own control plane (spec021).

## Non-goals (2026-11-07)

- The patches themselves (spec021 and later).
- The fork's spec-kit content: it lives in the fork's `spec/`, set up next.
- Forking other dependencies.
