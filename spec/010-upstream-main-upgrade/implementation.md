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

### 2026-10-14: initial lifecycle checkpoint

Ported from develop `1fd51943`: dispatch pipe ownership, reader interrupt
forwarding, redirect owner cancellation, sender validation and optional outbound
retirement. Preserved upstream core status locking and FinalRules test fixtures;
did not restore singbridge or removed proxySettings behavior. Hy2 lifecycle
adaptation remains open, so P02/P03 are not marked complete.

Pristine upstream race baseline failed in outbound TestTagsCache: the cache
pointer was read outside the registry lock; its test also raced on stop flags.
The restored fork manager reads the cache under RLock and test flags are atomic.
Broader candidate tests exposed the existing UDP fixture accepting-flag race;
ported the spec007 close-driven fixture rather than suppressing the race.

Passed with Go 1.27.0 on Darwin/arm64:

```sh
go test -race -count=1 -timeout=5m ./common/buf ./core ./app/proxyman/outbound ./transport ./transport/internet ./transport/internet/hysteria
go vet ./common/buf ./core ./app/proxyman/outbound ./transport ./transport/internet ./transport/internet/hysteria
bash .github/check-gofmt.sh
git diff --check
```

Core tests took 48.546s, outbound 11.225s; other packages completed in under 3s.
These are package regressions, not the full acceptance or interop matrix.
Config wrapper now accepts a Git worktree root without weakening its dirty-tree
or develop tracking guards; four build wrapper tests pass. Candidate build
output is isolated in `xray-config/build/rebase-20261014`.
