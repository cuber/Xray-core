# Difference Inventory

Current fixed-main comparison: [2026-10-14 audit](review-20261014.md) and
[redacted machine inventory](evidence/review-20261014.json). The following
counts/tool remain the separate, reproducible October 4 baseline.

## Reproduce

Run from the canonical Core root. The refs are fixed; the configuration scan
uses the current xray-config worktree. Write a new reproduction outside Core
instead of overwriting this draft's dated evidence:

```sh
python3 draft/002-upstream-sync/tools/inventory.py \
  --repo . --config ../xray-config \
  --evidence ../xray-config/build/upstream-sync-review/evidence \
  --diffs ../xray-config/build/upstream-sync-review/diffs
python3 draft/002-upstream-sync/tools/inventory.py \
  --repo . --config ../xray-config \
  --evidence ../xray-config/build/upstream-sync-review/evidence \
  --diffs ../xray-config/build/upstream-sync-review/diffs --check
```

The tool reads fixed objects and local JSON through structured APIs, never fetches,
changes refs, starts Core or deploys. The configuration scan includes dirty local
changes, not a claim about every deployed server. Its status hash records that scope.

## Complete Cached Comparison

| Direction | Commits from base | Paths, no rename detection | Purpose |
|---|---:|---:|---|
| Base -> fork `2074c223` | 8 | 326 | Features that must survive |
| Base -> cached origin `3519dfec` | 203 | 569 | Upstream additions/fixes |
| Fork -> cached origin | N/A | 827 | Direct endpoint difference, includes deletion of fork-only code |
| Path intersection of both base-relative diffs | N/A | 67 | Potential conflicts, not proof of actual textual merge conflicts |

Default Git rename detection reports 568/826 changed files in the latter two
comparisons; the reproducible inventory disables rename detection and represents
a rename as deleted and added paths (569/827). Do not mix these counting conventions.

The original full binary-capable Git diffs were generated to an untracked
research cache. The following paths are historical output provenance, not the
authoritative draft location; the commands above regenerate them under build/:

- `.cache/upstream-sync/base-to-fork.diff` (1,829,873 bytes).
- `.cache/upstream-sync/base-to-upstream-cache.diff` (2,119,521 bytes).
- `.cache/upstream-sync/fork-to-upstream-cache.diff` (3,892,224 bytes).

SHA-256 and exact direction are in [baselines.json](evidence/baselines.json).
These diffs are inspection evidence, **not** a patch to apply to develop: applying
the endpoint diff would remove AnyTLS and our other fork-only capabilities.
Do not track multi-megabyte diff bundles as source-code release artifacts.

Every cached upstream commit is in [upstream-commits.md](evidence/upstream-commits.md)
and [upstream-commits.json](evidence/upstream-commits.json), including changed paths.
`REVIEW/not-individually-audited` deliberately means inventory coverage, not a
claim of detailed acceptance review of that commit. Priorities reflect the selected
reviewed candidates; urgent findings are explained in [research](research.md).

Path tables: [fork](evidence/fork-files.tsv), [upstream](evidence/upstream-files.tsv),
[endpoint](evidence/endpoint-files.tsv), [intersection](evidence/overlap-files.txt).

| Kind | Fork | Cached upstream | Endpoint difference |
|---|---:|---:|---:|
| Runtime or schema | 79 | 407 | 438 |
| Tests | 157 | 113 | 257 |
| Generated protobuf | 9 | 35 | 41 |
| Documentation | 77 | 2 | 78 |
| Build/dependency/CI | 4 | 12 | 13 |

This path classification is mechanical: for example `.proto` is runtime/schema;
it is not a semantic risk estimate. Runtime drift is not 827 independent fixes.

## Newer Web-Observed Commits

These 18 entries are visible between cached origin and the September 30 web
history. Only short SHAs were exposed; no local Git objects, complete endpoint diff
or present-day remote-head equality is asserted. Source:
[upstream history](https://github.com/XTLS/Xray-core/commits) and linked commits.

| SHA | Area | Initial disposition |
|---|---|---|
| [65e853e](https://github.com/XTLS/Xray-core/commit/65e853e) | Native SS2022, removes sing dependencies | Incident-relevant; high-risk migration, not standalone cherry-pick |
| [7a01883](https://github.com/XTLS/Xray-core/commit/7a01883) | MASQUE inbound | Defer, no current configured consumer |
| [47a2c2f](https://github.com/XTLS/Xray-core/commit/47a2c2f) | Linux TUN auto DNS | Defer; changes host DNS/routing |
| [5dda894](https://github.com/XTLS/Xray-core/commit/5dda894) | Windows Wintun adapter reuse | Conditional, not production Linux/macOS listener fix |
| [7b8ade3](https://github.com/XTLS/Xray-core/commit/7b8ade3) | TUN UDP close ownership | Conditional TUN use |
| [2953d44](https://github.com/XTLS/Xray-core/commit/2953d44) | MPH matcher compact storage | P1 memory research; fetch, semantic tests and benchmark |
| [7780db9](https://github.com/XTLS/Xray-core/commit/7780db9) | Finalmask udpHop/xicmp panic | Conditional, broader transport dependencies |
| [e5e85ca](https://github.com/XTLS/Xray-core/commit/e5e85ca) | Vision close notification | Conditional chain/TLS correctness review |
| [fc8f8a4](https://github.com/XTLS/Xray-core/commit/fc8f8a4) | XDNS masks | Defer, no current configured consumer |
| [0fc3792](https://github.com/XTLS/Xray-core/commit/0fc3792) | HTTPUpgrade request headers | Defer/conditional client compatibility |
| [48ad030](https://github.com/XTLS/Xray-core/commit/48ad030) | CLI dynamic Hy2 users | P2; preserve local AnyTLS CLI account registration |
| [08cb6e6](https://github.com/XTLS/Xray-core/commit/08cb6e6) | Core WireGuard startup races | Conditional; kernel wg-* is not this proxy implementation |
| [35e616d](https://github.com/XTLS/Xray-core/commit/35e616d) | PacketConnWrapper move | Shared dependency/refactor review, not standalone user feature |
| [6243d2a](https://github.com/XTLS/Xray-core/commit/6243d2a) | Matcher memory/regex policy | P1 research; platform/matcher-selection semantics gate |
| [e51b3c3](https://github.com/XTLS/Xray-core/commit/e51b3c3) | Noise exponential distribution | Defer, unrelated to memory incident |
| [0086362](https://github.com/XTLS/Xray-core/commit/0086362) | Native SS2022 follow-up | Mandatory with rewrite; old-client large buffers and UDP compatibility |
| [1f30491](https://github.com/XTLS/Xray-core/commit/1f30491) | TUN DNS / Windows leak policy | Defer, host/network policy changes |
| [b26a91d](https://github.com/XTLS/Xray-core/commit/b26a91d) | v26.9.30 version | Web checkpoint, not selected feature/backport |

## Local Configuration Impact

[config-usage.json](evidence/config-usage.json) scans 42 current node configuration
files, including generated local Xray. It deliberately contains only node names,
field paths, types and counts, no account/password/private-key data.

- `ipsBlocked`: 44 objects in 39 node configs. These are not automatically portable
  to finalRules. Existing empty lists intentionally bypass private-IP blocking.
- `sendThrough`: one object on isz5; interface settings occur on isz5,
  la.lazy.switch and la.lisa. Preserve source-IP/interface routing through upgrades.
- Inbound protocol objects: 87 Shadowsocks, 41 Hysteria, 12 VLESS, 5 AnyTLS,
  45 dokodemo-door and 4 SOCKS. These are objects, not listeners or server counts.
- Outbound protocol objects: 359 Hysteria, 56 VLESS, 10 Shadowsocks, 6 AnyTLS,
  55 Freedom, 6 SOCKS and 42 loopback, plus 32 blackhole.
- No configured Core TUN or Core WireGuard inbound/outbound was found in this scan.
  Server kernel WireGuard interfaces still matter for interface/dialer regressions.
- No scanned SS UoT or removed TLS fields were found; absence in node JSON does not
  prove absence in subscription generators, third-party clients or test fixtures.

Full upstream synchronization must rescan these counts against its approved
release/config state, rather than reuse a historical 41-node/46-object inventory.
