# Validation And Historical Navigation

## Current Status, 2026-10-16

Core011 is an ownership split of the existing locally verified SS2022 candidate,
not a new execution or production release. Existing runtime evidence remains
at its original paths. A documentation-only change does not alter experimental
source snapshots or make historical package runs describe the current HEAD.

## Original Number Mapping

| Original ID / location | Current Core011 owner | Retained boundary |
|---|---|---|
| Core007 FR-001 SS Close clause, V-032 SS portion, historical T007 and T005 SS coverage | FR-008, V-008/009 | Cache invalidation and all non-SS V-032 work stay in Core007; task checkmarks remain historical |
| Core007 SS lostcancel evidence | FR-008 | Shared vet/harness policy stays in Core007; no claim to own other five historical vet findings |
| Core010 US-002, FR-003, V-003 | US-001, FR-001, V-001/009 | Fixed-main attribution and migration selection stay in Core010 |
| Core010 FR-003/004, V-004 | FR-002/008, V-002/008 | General counters and other protocol policies keep their original owners |
| Core010 U-001 | FR-004/007, V-004/007 | HandlerService/paired usersync schema and consumer compatibility remain integration gates |
| Core010 U-002 | FR-005/007, V-005/007 | Controlled invalid-UDP allocation/quiet expiry, not a public flood test |
| Core010 U-003 | FR-005/006/007, V-005/006/007 | Identity, roaming, replay and retirement do not transfer shared transport lifecycle ownership |
| Core010 V-005 SS local safety/control portion | FR-003..008, V-003..008 | Cross-version/independent-client acceptance remains Core010's integration gate |
| Core010 V-014 local SS resource subset | NFR-001, V-009 | Whole-server THP A/B and 24-48h canary remain Core010/Config, not fulfilled by this soak |
| Core010 P09 SS portion and historical T010/T013/T014 SS portions | Core011 contract/test map | P09's integration decision stays in Core010; mixed QUIC/DNS/writer tasks do not move wholesale or become checked |

Old filenames containing `spec011` in Core007's earlier reconstruction evidence
refer to the then-used global numbering, not this newly allocated local Core011.
No historical file/test/requirement ID is renamed by this split.

## Evidence In Place

| Original record | Executed evidence / limit |
|---|---|
| [Native safety report](../../proxy/shadowsocks_2022/SAFETY.md) | Primary source for commands, defects, upstream attribution, failures and subsequent acceptance; read later dated sections with earlier limitations |
| [First source manifest](../../proxy/shadowsocks_2022/safety-source.sha256), [restart source manifest](../../proxy/shadowsocks_2022/safety-source-r2.sha256) | Frozen package sources for their respective experiments, not a checksum of today's documentation/source tree |
| [Failed first soak](../../proxy/shadowsocks_2022/soak-linux-20261014.jsonl) | 70.99s harness ordering failure; not a passing hour or proof of a native socket leak |
| [Restart hourly summary](../../proxy/shadowsocks_2022/soak-linux-20261014-r2.summary.jsonl) | Linux/arm64 race: 3,600.27s test, 3,549 waves/35,490 connections, owned resources drained each wave; source manifest embedded |
| [Darwin FD baseline](../../proxy/shadowsocks_2022/safety-baseline-20261014.json), [Linux FD baseline](../../proxy/shadowsocks_2022/safety-baseline-linux-20261014.json) | Old fork ends at 208 FDs from 8 under retained silent peers; pristine upstream/candidate remain 8 for each inbound variant; short comparisons, not old/new one-hour soak |
| [Darwin executable interop](../../proxy/shadowsocks_2022/safety-interop-20261014.json), [Linux executable interop](../../proxy/shadowsocks_2022/safety-interop-linux-20261014.json) | Darwin 20 old/candidate/independent-client cases; Linux/arm64 ten old/candidate cases; exact bytes/packets and binary hashes in original files |
| [Darwin duplicate-key results](../../proxy/shadowsocks_2022/safety-duplicates-20261014.json), [Linux results](../../proxy/shadowsocks_2022/safety-duplicates-linux-20261014.json) | Synthetic-key old/upstream/candidate behavior and independent identity rejection; not permission to rotate production keys |
| [Core007 historical V-032](../007-core-regression/tests.md#v-032), [reconstruction evidence](../001-core-fork-contracts/evidence-011.md) | Original early-outbound-failure Close and vet provenance; no relabeling as a new Core011 execution |
| [Core010 local verification](../010-upstream-main-upgrade/local-verification-20261015.md) | Partitioned source-specific Darwin/Linux candidate results, skips and paired boundaries; not one final-HEAD whole-tree run |
| [Core010 integration ledger](../010-upstream-main-upgrade/implementation.md) | P09/P10/P11 integration history; P12 adoption remains unchecked |
| [Protocol evidence manifest](../010-upstream-main-upgrade/evidence/protocol-20261016/manifest.json), [original SHA256SUMS](../010-upstream-main-upgrade/evidence/protocol-20261016/SHA256SUMS) | Broader Core010 chain evidence, not relabeled as an SS safety experiment; compressed archives, hashes and source mapping remain unchanged |

The hourly manifest SHA-256 remains
`d024858eea2ab9439292d70304721b53cee8a793f17dcabe9ccacbcdf8f7eed8`;
the original first-attempt manifest remains
`31f74ce9ad266f74ffc4ab78c2ad431f63d91c5f4e3752f185bbc0f4f4b29dfd`.
The full hourly raw log is an ignored local archive, not a promised tracked
artifact. Its original hash and command remain in the permanent summary/report.

## Subsequent Acceptance And Unclaimed Results

SAFETY.md records the later translated Linux/amd64 package execution, half-close
race x10 and Linux x20 with a failing timeout-selection mutation, and all eight
independent-client first-read race controls passing. Its earlier pending/limit
paragraphs are historical checkpoints, not the latest verdict on those cases.
The later first-read result required no runtime framing change.

The isolated Config candidate's distinct admin identity resolves its specific
duplicate-key migration blocker; production files/credentials remain separate.
Core010/Config own matching revisions, actual Stats/control/routing receipts and
eventual rollout. Neither the migrated spec nor a candidate parser pass proves
production credential migration completed.

No new runtime result, native-amd64 hourly race soak, Windows execution,
production canary, whole-server THP A/B or universal leak absence is claimed.
Core010 T025's paired Linux/public-chain/resource gates remain separate and
cannot be completed by importing this local safety evidence.

## Current Navigation

[Core spec index](../README.md), [Core README](../../README.md) and
[Core001 ownership map](../001-core-fork-contracts/spec.md) expose this contract.
Core006 retains attribution/RPC, Core008 retains AnyTLS, and Core009 retains
direct control listeners. Consumers link here for SS2022 safety and to Core010
for fixed-main integration/release gates; neither link changes operational state.

## Documentation Checks

Executed locally on 2026-10-16 for this documentation split:

- Read-only Markdown target/heading-anchor check over Core007/010/011:
  40 Markdown files, 202 local links and eight anchor links; zero errors.
- Requirement/test-reference audit: all eight FRs and both NFRs represented,
  seven unique documentation task IDs, 20 referenced test functions present;
  no template placeholders. This is static coverage, not runtime execution.
- `git diff --check -- spec/007-core-regression spec/010-upstream-main-upgrade
  spec/011-ss2022-safety`: passed. Seven new Markdown files also pass read-only
  no-index whitespace/EOF checks; they are not covered by ordinary tracked diff.
- Pre/post SHA-256 comparison of 113 existing tracked files in Core007/010 and
  `proxy/shadowsocks_2022`: only the eight intended current documentation files
  changed, leaving 105 protected files identical. Dated reports, source/test
  files, manifests, compressed logs and historical task/protocol plans are unchanged.
- `shasum -a 256 -c spec/010-upstream-main-upgrade/evidence/protocol-20261016/SHA256SUMS`:
  all 26 entries OK, including the frozen task/plan, source, manifest and `.gz`
  evidence. The two SS source-manifest file hashes also match the values above.

Other agents' changes outside this scope were left untouched. No Go tests,
external clients, builds, network/SSH operations, dependencies, pins, commits,
pushes or deployments were performed.
