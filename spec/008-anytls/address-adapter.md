# AnyTLS Address Adapter Isolation

Date: 2026-10-04; canonical adoption authorized on 2026-10-05. Owner: Core
spec008. Local refactor only, no upstream merge or deployment. Baseline:
`2074c22344fdd6f020fd0111d9c35b1f60c839a7`.

## Scope and Contract

AnyTLS only used `singbridge.ToDestination` and `singbridge.ToSocksaddr`. Move
their existing behavior to unexported `toDestination` and `toSocksaddr` in
`proxy/anytls/address.go`; replace all AnyTLS production/test imports. External
UDP response tests construct expected destinations with Core's parser instead
of exporting a protocol helper solely for tests.

- Preserve IPv4, IPv6, domain spelling, port and TCP/UDP destination identity.
- Preserve IPv4-mapped IPv6 normalization and valid-domain precedence over IP.
- Invalid Sing addresses continue to produce an empty Core destination.
- Port validation remains at the existing callers; address conversion does not
  resolve DNS, reject port zero, dial, dispatch or create new lifecycle state.
- Keep existing policy, cancellation, pool, TLS, routing, user/domain statistics,
  control API and UoT framing/payload limits unchanged.
- Keep pinned `sing` engine/UoT dependencies and all legacy bridge files. Current
  SS-2022 still imports the bridge, so deletion is not part of this refactor.
- No schema/protobuf/config/pin change, new generic bridge or dependency update.

This removes the AnyTLS dependency on a directory newer upstream deletes. It is
not proof of compatibility with a new upstream baseline or an SS memory fix.

## Verification Gates

| Gate | Test / oracle |
|---|---|
| A1 | `TestAnyTLSAddressRoundTrip`: independent Core/Sing address assertions and round trips for IPv4, IPv6, mixed-case domain, UoT magic domain, TCP/UDP and ports 0/1/65535. |
| A2 | `TestAnyTLSAddressInvalid`: empty, port-only and malformed-domain input stay invalid. |
| A3 | `TestAnyTLSAddressNormalization`: mapped IPv6 and IPv4 have the existing normalized result; domain/IP precedence is unchanged. |
| A4 | `TestAnyTLSDoesNotImportSingbridge`: parse Go imports in the complete AnyTLS subtree, including tests and engine. Reintroducing a bridge import must fail. |
| A5 | `TestClientUDPPacketBoundaries` and `TestAnyTLSOutboundUDPResponseBoundaries`: valid IPv4/IPv6/domain packets preserve destination and exact bytes; invalid payloads fail explicitly, including supported chained paths. |
| A6 | Full AnyTLS/engine tests with race detection: TCP/UoT, credentials, dynamic users, control API, exact statistics, OB, pool reuse, cancellation, limits and chains. Report external-test skips separately. |
| A7 | Existing SS-2022, bridge and configuration regressions; no changes to their implementation or dependencies. |
| A8 | `go list -deps ./proxy/anytls`: no runtime bridge dependency. Format/whitespace gate and normal wrapper Darwin/Linux builds, local binary version and VCS metadata. |

Run a negative A4 control by inserting an unused blank bridge import into a
temporary AnyTLS source file in an isolated clone, expect that single test to
fail, then remove the temporary file before other tests/builds. Do not alter the
approved sibling checkout for the control.

## Historical Isolated Evidence (2026-10-04)

The approved sibling checkout was read-only in that session. Implementation was
staged at `xray-config/.cache/anytls-address-decoupling/core`, an isolated clone
of the exact baseline, as commit `bec6097bec0039fb26b548a3751e3606e1667023`.
That run did not replace production binaries/configs, approved HEAD or consumer
pins. This section preserves its original limitations, not today's adoption state.

Go version: `go1.26.1 darwin/arm64`. Test caches stay in the isolated workspace.
Logs are saved under `xray-config/.cache/anytls-address-decoupling/logs/`.

- PASS: A1-A4 and `TestClientUDPPacketBoundaries`, initial focused run.
- PASS: the intentional blank bridge import makes A4 fail with the exact source
  filename; the control source was removed before all later tests/builds.
- PASS: 17 in-memory address/config/user/context/UoT/budget/backpressure tests,
  each repeated ten times with `-race`; 170 top-level passes and zero failures.
  Full structured evidence: `in-memory-race.jsonl`.
- PASS: AnyTLS engine, SS-2022 and `infra/conf` full package race regressions;
  `common/singbridge` compiles but has no tests, not a runtime acceptance claim.
- PASS: `go vet ./proxy/anytls/... ./proxy/shadowsocks_2022 ./common/singbridge
  ./infra/conf`, dependency-graph check and format/whitespace gates.
- PASS: shared bridge, SS-2022 implementation, `go.mod` and `go.sum` have no diff
  from the baseline.
- BLOCKED: the complete AnyTLS network suite cannot bind loopback TCP/UDP under
  this session's sandbox. It fails with `bind: operation not permitted`, including
  an httptest listener panic; it is not accepted or rewritten to skip. A minimal
  Python socket bind fails with the same EPERM independently of Core. Full suite
  diagnostics are saved as `full-regression.jsonl`.
- NOT RUN: independent sing-box/Mihomo client/server interoperability. Both
  binaries are present locally, but these fixtures also require forbidden local
  listeners. A5's dispatcher/chained UDP response case and A6's network/control/
  exact-statistics cases need rerunning in an environment with local networking.
- PASS: the normal `xray-config/xray-core/build.sh` wrapper built Darwin arm64
  and Linux amd64 artifacts in `xray-config/build/anytls-address-decoupling/`.
  Darwin executes `version`; Linux is a static x86-64 ELF. Both embed their
  candidate commit/time and `vcs.modified=false`. Final hashes/revision are
  recorded alongside `build.log`, not inferred from default production binaries.
- BLOCKED: validating the unmodified local config attempts to write its existing
  log path outside the workspace and receives EPERM. A generated validation-only
  JSON changes only access/error logs to `none`; its `run -test` passes. No
  generated local source/runtime config is edited, installed or activated.

Commands executed from the isolated Core root:

```sh
go test ./proxy/anytls -run 'TestAnyTLS(Address|DoesNotImportSingbridge)|TestClientUDPPacketBoundaries' -count=1 -timeout=2m
go test -race ./proxy/anytls/... ./proxy/shadowsocks_2022 ./common/singbridge ./infra/conf -count=1 -timeout=15m
go vet ./proxy/anytls/... ./proxy/shadowsocks_2022 ./common/singbridge ./infra/conf
go list -deps ./proxy/anytls
bash .github/check-gofmt.sh
git diff --check
```

The ten-round selected suite uses an anchored list of the 17 actual tests in
`in-memory-race.jsonl`, not a broad prefix that could admit listener fixtures.
All Go commands use `GOCACHE=xray-config/.cache/anytls-address-decoupling/go-build`
(absolute path). Isolation paths/logs are operational evidence, not shipped code.

The build uses an explicitly selected isolated source/branch/output, not the
approved sibling HEAD:

```sh
XRAY_CORE_SRC="$CONFIG/.cache/anytls-address-decoupling/core" \
XRAY_CORE_BRANCH=refactor/anytls-address-decoupling \
XRAY_BUILD_DIR="$CONFIG/build/anytls-address-decoupling" \
GOCACHE="$CONFIG/.cache/anytls-address-decoupling/go-build" \
  "$CONFIG/xray-core/build.sh"
```

`CONFIG` denotes the absolute xray-config root. The wrapper's clean-tree and
format gates are preserved. Read-only Go module stat-cache warnings are retained
in the log; they did not prevent successful builds. Network acceptance remained
blocked in that run regardless of compilation or syntax-validation success.

## Canonical Adoption (2026-10-05)

The user authorized adoption directly on the approved Core `develop`, tracking
`cuber/develop`, rather than retaining the change only in an isolated branch.
The exact refactor above is applied there; no upstream commits are imported.
SS-2022, `common/singbridge`, `go.mod` and `go.sum` remain byte-for-byte unchanged
from the baseline. The broader upgrade is still an undecided
[promoted spec010 (formerly draft002)](../010-upstream-main-upgrade/spec.md).

Environment: macOS Darwin arm64, Go 1.26.1, owned loopback TCP/UDP fixtures.
Raw logs and strict runner reports live outside Core in the ignored
`xray-config/build/anytls-address-adoption-20261005/` build directory.

- PASS: A1-A4 and the client UDP packet boundary test, repeated ten times with
  race detection. Runtime dependency inspection also excludes singbridge.
- PASS: strict independent sing-box 1.14.2/Mihomo 1.19.31 client/server
  interoperability, all sixteen external-server chain combinations, nine native
  separate-process paths and the real default idle timer. All four runner groups
  exit zero with no skipped cases or surviving child processes.
- PASS: `go test -race -json ./... -count=1 -timeout=15m` exits zero: 87 packages
  pass, 90 packages have no tests, and 1,598 test/subtest pass events are recorded.
  The 22 skipped events are retained in `core-race.jsonl`: opt-in public network,
  soak/diagnostic and network-namespace cases, the process-helper sentinel,
  default-idle opt-in and an unavailable Darwin source alias. The default-idle
  gate was separately executed successfully above. No new full ten-minute soak
  or Linux-only/source-alias capability acceptance is claimed.
- PASS: the full suite executes A5's dispatcher/chained UDP response boundary
  regression and existing user/control/statistics, OB, pool and SS-2022 tests.
- PASS: `go vet ./...`, Go formatting and whitespace checks; the strict runner's
  fourteen Python tests; draft links/JSON and fixed-ref diff/inventory checks.

- PASS: the official `xrayctl build core` wrapper builds Darwin arm64 and static
  Linux amd64 from committed canonical develop. Darwin executes `version` and
  `xrayctl test local` reports `Configuration OK`; both artifacts retain VCS
  revision/time with `vcs.modified=false`. Build logs are retained alongside the
  test evidence; artifacts are rebuilt when the acceptance documentation is
  amended so their metadata matches the final release commit.

No service deployment or consumer pin change is part of this adoption.
