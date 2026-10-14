# Implementation Ledger

## Boundaries

User authorization: 2026-10-14, publish existing draft audit then create a rebase
branch and start implementation. The published audit is develop `1fd51943`.
Candidate worktree: `../xray-core-rebase-20261014`; branch:
`rebase/main-20261014`; exact upstream base:
`0888beb38a9ba45440557175e71ce21cf8ba3b13`.

This is a contract-led rebase: start at upstream, import source specifications,
then port cohesive changes. Do not run a blind rebase that resurrects removed
singbridge or legacy proxySettings implementation. Each adaptation records its
original commit and why an upstream equivalent replaces part of it.

Source specs and evidence are imported from develop, then draft002 is moved to
spec010. References inside immutable dated evidence remain historical. The
canonical develop checkout and approved build/pin/runtime remain untouched.

## Phases

- [x] P01 Publish draft audit; lock source/upstream, create isolated branch,
  promote spec010; Go 1.27.0 is available on Darwin/arm64.
- [ ] P02 Establish targeted upstream baseline; port spec002 dispatch lifecycle
  and independent tests without restoring removed SS bridge/legacy proxy path.
- [ ] P03 Port shared spec007 readv/regression changes and later spec002
  retirement/validation capabilities; adapt Windows readiness behavior.
- [ ] P04 Port spec003 multi-listener, SOCKS/Unix redirect contracts and tests.
- [ ] P05 Port spec004 OB and prerequisites, preserving 3s and full cancellation.
- [ ] P06 Port spec005 routing metadata/weighted selection and spec006 domain
  traffic against atomic upstream APIs; regenerate reviewed protobuf.
- [ ] P07 Port spec009 API UDS ownership beyond upstream's basic UDS support.
- [ ] P08 Port spec008 AnyTLS, required Sing dependencies and new chain matrix.
- [ ] P09 Resolve native SS gates U-001/002/003 and shared U-004..012;
  no source-level concern is declared fixed without a regression reproducer.
- [ ] P10 Integrate isolated Sidecar/config migrations; no production pin change.
- [ ] P11 Full uncached/race/interop/chain tests, format/vet, Darwin/Linux builds
  through an explicitly candidate-aware wrapper; do not weaken normal develop
  release protections. Record every failure and skip.
- [ ] P12 Review/adopt candidate and release pins only on explicit approval;
  production rollout is a separate operation.

## Verification

Each phase runs its owning spec tests; the additional U-* matrix in tests.md
applies throughout. Partial phase success is not full upgrade acceptance.
Never copy a prior fork's green report into this ledger as candidate evidence.

Go source changes require the official dual-platform build workflow before
release; this branch is an incomplete integration candidate, not a release.
No candidate artifact may overwrite xray-config/build approved release artifacts
or restart the local Xray service during integration.
