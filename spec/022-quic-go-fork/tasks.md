# Tasks: quic-go Fork Dependency

- [x] T001 spec, plan and tasks; index row in `spec/README.md`.
- [x] T002 Fork, clone with both remotes, line branch `v0.61.0-cuber` at the base, pushed. Renamed
  `fork/2026-11-07/184d081eef3e`, with the remotes swapped to `origin` = apernet and `cuber` = ours, by spec023.
- [x] T003 `replace` at the base; `go mod verify`, build, QUIC users' tests; module trees equal (FR-001, FR-002).
- [x] T004 Guard test (FR-003).
- [x] T005 Spec001 FR-012 sentence (FR-005).
- [ ] T006 Commit (with the user's consent); `xrayctl build core` identity check (FR-004).
- [ ] T007 Next: the fork's spec-kit (its `spec/`), then spec021's library contract moves there. It links spec023's
  register and SOP instead of keeping copies.

## Evidence

- 2026-11-07 T002:
  - **Fork.** `gh repo fork apernet/quic-go --clone=false` created the public `cuber/quic-go`, whose parent GitHub
    lists as `HyNetworks/quic-go`.
  - **Checkout.** Cloned to `../quic-go` with remotes `origin` and `apernet`.
  - **Base.** `184d081e` is the head of `apernet/v0.61.0-mod-rename` (committed 2026-08-06 01:09 UTC; first recorded
    here as 2026-08-05, a local-time date; "fix: chrome parrot packet number
    length, Initial ACK and coalescing"). It sits 7 commits above the quic-go tag `v0.61.0`:
    - `b494d6d6` Hysteria fork modifications;
    - `497dae41` module rename script, and `eb994cc4` the module rename;
    - `0ad2f221` Chrome parrot;
    - `bc12a60f` `Conn.InitialPacketSize`;
    - `cdc8dd7f` `Transport.DisableGSO`;
    - `184d081e` the Chrome parrot fix.
  - **Line branch.** `git switch -c v0.61.0-cuber 184d081eef3e` and `git push -u origin v0.61.0-cuber`; `git
    ls-remote` shows `refs/heads/v0.61.0-cuber` at `184d081eef3e9edd5cb7c0ddf2460c91f2e6adb1`.
  - **Base suite.** The fork's own suite on the unmodified line: `go test ./... -count=1` (go1.26.1, darwin/arm64)
    exits 0, with 25 packages ok and 13 without tests, `integrationtests/self` included. `integrationtests/fips` and
    `gomodvendor` are separate modules and were not run. Core builds with go1.27.0, so the fork's tests should also
    run with that toolchain; the fork's spec-kit fixes this rule.
- 2026-11-07 T003–T005 (on `431e0868`):
  - **Proxy resolution.** `go list -m github.com/cuber/quic-go@184d081eef3e` through `proxy.golang.org` gives
    `v0.61.1-0.20260806010916-184d081eef3e` (origin hash `184d081eef3e9edd5cb7c0ddf2460c91f2e6adb1`). This is the same
    pseudo-version as the upstream `require`.
  - **go.mod and go.sum.** The `replace` line is added. `go mod tidy` drops only the two upstream `go.sum` lines and
    adds the fork's.
    - The fork's `go.mod` hash equals upstream's: `h1:x7qxEvX6MCVtDuBKHj3E+88+BtrbEMuAL5qGUKItjW8=`.
    - The module hash differs only because the module path is part of it: `h1:gZ7RBgOk…` against upstream's
      `h1:5mgtR5gw…`.
    - `go mod verify`: all modules verified.
  - **FR-002.** `diff -r` of the two extracted module trees (507 files) finds no difference.
  - **FR-001.** `go build ./...` passes. These pass with `-count=1`: `go test ./transport/internet/hysteria/...` (with
    `congestion/bbr` and `brutal`), `./transport/internet/splithttp/...`, `./transport/internet/masque/...`,
    `./app/dns/...` and `./core/...`.
  - **FR-003.** `core/quic_go_fork_test.go` (`TestQuicGoIsReplacedByTheFork`) failed before the `replace` and passes
    with it. It reads both `replace m => r v` and `replace m v => r v`.
  - **FR-004, in part.** A throwaway Linux build from the working tree, kept out of `build/` so the approved artifacts
    stay as they are. `go version -m` lists `dep github.com/apernet/quic-go v0.61.1-0.20260806010916-184d081eef3e`
    followed by `=> github.com/cuber/quic-go v0.61.1-0.20260806010916-184d081eef3e h1:gZ7RBgOk…`.
    `vcs.modified=false` is checked by `xrayctl build core` after the commit.
