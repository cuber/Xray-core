# Plan: quic-go Fork Dependency

- Spec: [spec.md](spec.md)

## Repository

- `gh repo fork apernet/quic-go --clone=false`: done on 2026-11-07. `cuber/quic-go` is public, and its parent is
  listed as `HyNetworks/quic-go`.
- Clone to `../quic-go` with `origin` (`git@github.com:cuber/quic-go.git`) and `apernet`
  (`https://github.com/apernet/quic-go.git`).
- `184d081eef3e` is the head of `apernet/v0.61.0-mod-rename`, not of `master`. Create the line branch
  `v0.61.0-cuber` there and push it to `origin`, so the commit stays reachable from a ref of the fork for the module
  proxy.
- 2026-11-07, [spec023](../023-fork-lines/spec.md): the remotes became `origin` (apernet) and `cuber` (the fork),
  and the branch became the line `fork/2026-11-07/184d081eef3e`.

## Core

- `go mod edit -replace=github.com/apernet/quic-go=github.com/cuber/quic-go@184d081eef3e`, then `go mod download`.
  Go resolves the pseudo-version and records the fork's hashes in `go.sum`. The `require` line is unchanged.
- FR-002: compare the two extracted module trees in the module cache with `diff -r`. They come from the same commit,
  so they must be equal.
- FR-003: `core/quic_go_fork_test.go`. It finds `go.mod` by walking up from the package directory, as
  `transport/internet/finalmask/sudoku` does, and asserts the replace line and its target with a line scan, so no
  new dependency is needed.
- Spec001 FR-012 gets a dated sentence. The spec index gets row 022 and "Next spec: 023".

## Tests

- `go mod verify`; `go build ./...`.
- `go test ./transport/internet/hysteria/... ./transport/internet/splithttp/... ./transport/internet/masque/...
  ./app/dns/...`, plus the guard test.
- After the commit: `xrayctl build core` and `go version -m build/xray-linux-amd64 | grep quic-go`.

## Gate and rollout

- The code is unchanged, so no gate tier beyond the build applies, and nothing is deployed for this spec alone. The
  next Core build (spec021) carries the replacement.

## Rollback

- `go mod edit -dropreplace=github.com/apernet/quic-go`, then `go mod tidy`.
