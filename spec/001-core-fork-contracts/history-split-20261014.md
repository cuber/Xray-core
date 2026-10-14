# Recent Increment Ownership Split (2026-10-14)

## Scope and Safety

User authorized reorganizing specifications and commits before a future upstream
upgrade. This is history/documentation work, not an upstream merge, protocol fix,
deployment or force-push authorization. The original baseline reconstruction
through `4d9abecd` is preserved; only its six later increments are rebuilt.

- Original tip: `446dd39d3137dee838f39b8771daa32fdd47e7fd`.
- Backup: `backup/develop-before-spec-split-20261014` at that exact tip.
- Assembly branch: `refactor/spec-split-20261014`, initially based on `4d9abecd`;
  removed after successful local develop adoption.
- Temporary worktree: `../xray-core-reorg-20261014`, removed after verification.
- Runtime, tests, protobuf, dependencies and build inputs must be byte-identical
  to the original tip. Only owning specifications, indexes and a stale draft
  next-ID reference may differ. Do not regenerate protobuf or format source here.
- Six increments become ten cohesive increments plus a documentation report.
  A spec may own multiple commits; one commit must not mix unrelated ownership.

## Mapping and Dependency Order

| Original | New | Owner and content |
|---|---|---|
| 2074c223 (part) | 6e96e1c1 | 007: terminal readv handling, independent reset/error tests, DoQ test socket ownership |
| 2074c223 (part) | 54dc7a3e | 002: optional sender/retirement capability, handler lifecycle, redirect cancellation, reader interruption |
| 2074c223 (part) | 90ebf60b | 004: OB connection-owned dispatch cancellation and independent HTTP test |
| 2074c223 (remainder) | 90548f83 | 008: native outbound, engine/pool/UoT, config registration and protocol/integration tests |
| 7d74e1b0 (part) | 2618ade5 | 008: address adapter removes AnyTLS's singbridge import |
| 7d74e1b0 (remainder) | 994bc8f7 | draft002: dated upstream inventory/research/tool, no runtime changes |
| a6d78b87 | f117c6be | 004: optional HTTP/2 negotiation |
| 10476854 | 6c241fec | 004: bounded retries and shared ProbePolicy |
| 9dee8572 | 66af38ab | 009: direct control UDS, exclusive lock/stale-socket behavior |
| 446dd39d | 55e22bd6 | 004: independent probe transport, original subject/result identity |

007 readv error handling and 002 interruption touch different hunks in
`common/buf/readv_reader.go`. They are deliberately separate, not directory-based
splits. Shared changes carry their independent tests before the AnyTLS consumer.
008 still owns the real protocol/chain/retirement integration matrix. Its original
impact and acceptance files remain historical discovery evidence, not competing
definitions of 002/004/007.

009 remains independent of 003: direct API ownership bypasses traffic routing;
003 owns traffic listeners, multi-address inbounds and authenticated SOCKS UDS.
No upstream-native SS migration occurred: SS-2022 still uses singbridge, while
AnyTLS's Sing/UoT dependencies remain intentional.

## Replay and Upstream Review

For future comparison, preserve the optional capabilities and behavior contracts,
not necessarily our concrete patch text. Check an upstream equivalent against the
owning tests before discarding any extracted fix. Public/shared fixes must not be
dropped with an AnyTLS engine replacement. Draft002 evidence is dated research,
not permission to rebase or a current upstream audit.

The chronological order above is replayable from 4d9abecd. Do not arbitrarily sort
all historical commits by spec number: 008 consumes 002/004/007, and later OB
increments evolve the same protobuf/HTTP transport. No dependency versions change.

## Intermediate Verification

All commands ran on Darwin in the assembly worktree, before advancing to the next
runtime increment. Each stage also compiled `./main` (007 used whole-tree test
compilation). This is compile verification, not a release build or Linux runtime test.

| Stage | Command / result |
|---|---|
| 007 | `go test -race -count=1 -timeout=5m ./common/buf ./app/dns`: PASS; `go test ./... -run '^$' -timeout=3m`: compile PASS, no functional claim |
| 002 | `go test -race -count=1 -timeout=5m ./common/buf ./core ./app/proxyman/outbound ./transport/internet ./transport`: PASS |
| 004 cancellation | `go test -race -count=1 -timeout=5m ./app/observatory/... ./app/router/...`: PASS after supplying existing DAT resources; see failure below |
| 008 outbound | `go test -race -count=1 -timeout=5m ./proxy/anytls ./proxy/anytls/internal/engine ./infra/conf -run 'Test(Client\|AnyTLSOutboundRetirementAdmissionBarriers\|AnyTLSClient)'`: PASS |
| 008 adapter | `go test -race -count=1 -timeout=3m ./proxy/anytls -run 'Test.*(Address\|Destination\|Singbridge)'`: PASS |
| 004 HTTP/2 | `go test -race -count=1 -timeout=5m ./app/observatory/burst ./infra/conf -run 'Test(HTTP2\|ContractBurstHTTP2Config\|ContractBurstJSONIndependentSettings\|NewHTTPClientKeepAlive)'`: PASS |
| 004 retry | `go test -race -count=1 -timeout=5m ./common/retry ./app/observatory/burst ./infra/conf -run 'Test.*(Retry\|ProbePolicy\|Backoff)'`: PASS |
| 009 | `go test -race -count=1 -timeout=3m ./app/commander ./transport/internet`: PASS |
| 004 transport | `go test -race -count=1 -timeout=3m ./app/observatory/burst ./infra/conf -run 'Test(ProbeOutbound\|ContractBurstProbeOutboundConfig)'`: PASS |

Markdown table escapes render regex alternation as `|`; use normal Go RE2
alternation, not a literal backslash-pipe, when copying the commands.

Initial failure: router `TestChinaSites` could not find `resources/geosite.dat` in
the new worktree. The canonical checkout already uses symlinked xray-config DATs.
The temporary worktree was supplied the same resources; the unchanged test and
full affected packages then passed. This was an environment failure, not a code
repair, skip or relaxed assertion. The temporary symlink is not a Git artifact.

Fixture asset SHA-256 at verification time:

- geosite.dat: `35ed26a24cafa1256bd7261414224b7bcef5c944cea7760e172b030a8b266450`
- geoip.dat: `1cba1f0982cf62502fa079c66047c3d0c608196da5b3305671e68f60e917a482`

## Final Gates

- At `90548f83`, non-spec tree matches original `2074c223` exactly.
- At `55e22bd6`, non-spec tree matches original `446dd39d` exactly.
- `go vet ./...`: PASS; `bash .github/check-gofmt.sh`: PASS; whitespace: PASS.
- Full `go test -json -race -count=1 -timeout=20m ./...`: PASS (exit 0) on Go
  1.26.1, Darwin/arm64. 1661 test/subtest pass events, 46 skip events, no failures.
  AnyTLS took 543.404s; scenarios took 281.759s. Skips include opt-in public DNS,
  ECH, external sing-box/Mihomo, source alias, diagnostic and soak/helper cases;
  this run does not claim those integrations or Linux/Windows runtime acceptance.
  Local raw log: `/tmp/xray-core-spec-split-20261014-race.json`, SHA-256
  `59103d259a77e18497218e022365d8479dfe22b644105aa48b3ecf3452418eb6`.
- Official clean-tree `xrayctl build core`: PASS after local adoption; Darwin
  arm64 version executes and Linux amd64 ELF is verified. The final docs-only
  evidence amendment is rebuilt through the same wrapper, keeping VCS metadata.
- Local develop adoption: complete, still tracks cuber/develop. The expected
  local/remote divergence is 11 new vs 6 old commits; do not pull/rebase it away.
  Sidecar's consumer pin is maintained in its own commit, not in this Core spec.
- Documentation link checks: 18 changed/report documents, zero missing local links.
- No push or deployment performed by this work.

Tree proof after the documentation-only report (including the draft next-ID fix):

```sh
git diff --exit-code backup/develop-before-spec-split-20261014 HEAD -- . ':!spec/**' ':!draft/**'
git diff --stat backup/develop-before-spec-split-20261014 HEAD
```

Review the excluded paths too: only documentation changes are allowed, not
changes hidden under a spec/draft tooling directory. Historical evidence hashes
and source refs stay untouched. Snapshot tests, actual skips and final results
must not be inferred from passing compile gates.
