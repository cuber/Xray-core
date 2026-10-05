# WIP: Selective Upstream Synchronization

- Owner: xray-core; draft ID 002, explicitly assigned by the user on 2026-10-05.
- Created: 2026-10-04. Status: Draft / WIP, research only.
- Request: investigate upstream fixes related to pgz.luck memory/socket retention,
  inventory our differences from origin, and prioritize urgently needed changes.
- Authorization: upstream investigation and design only; no upstream merge,
  rebase, deployment or consumer-pin change. On 2026-10-05 the user separately
  authorized canonical documentation migration and adoption of the existing
  AnyTLS address-adapter refactor on develop, tracking cuber/develop; that narrow
  change belongs to spec008 and does not approve the upstream migration.

Read [research](research.md), [inventory](inventory.md), [plan](plan.md),
[tests](tests.md), [tasks](tasks.md), and [requirements](checklists/requirements.md).
Existing [Core contracts](../../spec/001-core-fork-contracts/spec.md) and
[AnyTLS](../../spec/008-anytls/spec.md) remain authoritative.

## Location and Evidence Boundary

The authoritative proposal lives in `xray-core/draft/002-upstream-sync/` on the
canonical develop branch. On 2026-10-05 its documents, inventory tool and redacted
evidence moved out of the temporary research clone; the old draft directory is
not retained as a second editable copy. xray-config provides navigation only.

The following comparison remains the dated 2026-10-04 research baseline. Moving
documents or adopting the separately scoped address adapter does not refresh its
upstream objects, inventories or runtime acceptance results.

| Baseline | Ref | Evidence |
|---|---|---|
| Fork base, v26.4.25 | `b4f08981becb71eaa995fa98ed2098ade92566bb` | Local Git object |
| Our develop | `2074c22344fdd6f020fd0111d9c35b1f60c839a7` | Clean, tracks cuber/develop; 8 fork commits |
| Cached origin/main, September 26 | `3519dfecbd65022ba71d9bc73e94063d0cbc8636` | Complete local Git comparison; 203 upstream commits since base |
| Newer web history, v26.9.30 pre-release | `b26a91d` | September 30 commit/release pages; full SHA/objects not fetched |

Git refresh failed because the configured proxy was unreachable and direct Git
DNS resolution failed. Web browsing exposed newer September 27-30 commits.
The complete saved diff therefore ends at **cached origin**, not current remote
HEAD. The September 30 web additions are separately listed, not silently counted
as fetched or fully diffed. Today's latest remote HEAD remains unverified.
This is a blocker for a full-upgrade decision, not for documenting narrow fixes.

## Findings and Recommendation

Upstream has a **directly relevant newer SS-2022 handshake-timeout implementation**:
the September 27 native rewrite `65e853e` (#6831), followed by `0086362` (#6866)
on September 30. The observed native multi-user source sets a policy handshake
read deadline before reading authentication data. Our fork and cached September
26 implementation do not. This addresses the observed silent-connection mechanism
in principle; no candidate binary has been tested here, and it does not prove
that all high RSS on pgz.luck was a live-object leak.

Do not immediately import the whole rewrite: it deletes singbridge used by our
SS-2022 and by AnyTLS at the recorded comparison baseline, changes SS wire
handling, and required follow-up interoperability/UDP fixes. The separately
approved [AnyTLS address adapter](../../spec/008-anytls/address-adapter.md) removes
AnyTLS's bridge import only; its Sing engine/UoT dependencies and SS-2022 bridge
remain. First evaluate a narrow policy-based handshake fix; evaluate the complete
native SS engine as a separately gated migration. Do not copy only the first rewrite.

Urgent shortlist, ordered by risk and relevance:

| Priority | Work | Disposition |
|---|---|---|
| P0 | Malformed QUIC sniffer panic, `8f15190c` #6471 | Missing narrow upstream security fix |
| P1 urgent | SS silent/partial authentication lifetime, native rewrite evidence | Direct incident fix; prefer narrow backport/adaptation first |
| P1 | DNS oversized-name panic, `a2cec2e5` #6207 | Missing; includes all resolver error paths |
| P1 | Atomic Stats GetOrRegister, `0bafca94` #6468 | Missing; retain ordinary and user-domain statistics |
| P1 | Partial-buffer writer correctness, `6ce8dc53` #6723 | Missing narrow shared fix |
| P1 | Router API/read-path races, `598bde74` #6678 | Missing snapshot semantics; adapt, do not overwrite fork APIs |
| P1 memory | Weak Geodata cache/registry, `56bb6366` + `be8009c6` | Missing; live-matcher and GC regression gates |
| P1 research | New MPH compact storage, `2953d44` + matcher policy `6243d2a` | Newer web evidence; fetch/review before selecting |
| P2 | SS post-auth bridge lifecycle `d878fc83`, XHTTP cleanup, Hy2 fixes | Production-use/dependency gates; not interchangeable with handshake fix |

The existing SS outbound-close fix `2776ea6d`, WaitReadCloser protection, leastLoad
tolerance and formatting gate have equivalents in our fork. Do not label them
missing merely because their upstream commit hashes are absent.

## User Scenarios

### US-001: Reproducible Difference Review

Given fixed base/fork/upstream objects, when the inventory is regenerated, then
every changed path and cached upstream commit is represented, overlaps are explicit,
and the direct endpoint diff is not mistaken for an additive merge patch.

### US-002: Bounded Unauthenticated Connections

Given a configured handshake timeout, when a TCP peer is silent or sends an
incomplete SS-2022 authentication header, then it is closed within the bounded
timeout budget without retained goroutines, dispatch links or user counters.
Given successful authentication, normal traffic remains usable beyond that
handshake timeout and respects the authenticated user's normal session policy.

### US-003: Preserve Fork Features During Backport

Given every approved Core contract, when a selected upstream fix is adapted,
then AnyTLS inbound/outbound, UDS, multi-address listeners, dynamic users,
user-domain traffic, detailed ListRule, weightedLeastPing and OB health still work.

### US-004: Separate Memory Mechanisms

Given repeatable idle/traffic/churn workloads, when old and candidate builds run,
then sockets, goroutines, post-GC live heap, retained matchers, RSS and THP are
measured separately. A restart or lower RSS alone is not leak-fix acceptance.

## Requirements

- FR-001: Pin base/fork/upstream; save full cached diffs, commit/file inventories,
  hashes, and the newer web-only delta with explicit freshness limitations.
- FR-002: Distinguish missing, equivalent, partial, conditional, breaking and
  individually-unreviewed changes; rank with source evidence, not commit titles alone.
- FR-003: Bound SS-2022 pre-authentication lifetime in single-user, multi-user
  and relay variants. Use existing Xray policy and clear the deadline at the
  authenticated boundary, not after the whole long-lived stream returns.
- FR-004: Maintain exact protocol/user routing and normal inbound/outbound/user
  byte counters; optional DomainTraffic remains optional and wire-compatible.
- FR-005: Preserve fork specs 002-008; shared-API and protobuf changes require
  dependency/consumer checks, including Sidecar and TUI.
- FR-006: Security/correctness backports need a baseline-failing reproducer,
  fixed-version pass, negative/positive/boundary tests and race checks.
- FR-007: Geodata changes must preserve live matchers through GC and match
  semantics; cleanup must not remove a replacement value under the same key.
- FR-008: RSS mitigation uses a controlled THP A/B experiment. No global THP
  change, memory-limit assumption or runtime flag is bundled into a source backport.
- FR-009: Full upstream synchronization is blocked until finalRules, transport,
  SS engine, Hy2, dependency/toolchain and client compatibility are validated.
- FR-010: Future deployment remains separate, through xrayctl and the existing
  SOP; never replace patched Core with an upstream release binary.

## Non-Goals

- No runtime implementation or production edits in this research task.
- No upstream reset/rebase of develop, destructive history rewriting or pin update.
- No opportunistic MASQUE/XDRIVE/TUN adoption or wholesale dependency changes.
- No claim that upstream fixes kernel THP behavior or pgz.luck's entire RSS gap.
- No imported deployment credentials, private keys or raw node configs in evidence.

## Acceptance and Open Gates

- SC-001: Cached Git inventories reproduce byte-for-byte, including all 203
  upstream commits, 326 fork paths, 569 cached-upstream paths, and 67 overlaps
  under the explicitly documented no-rename accounting convention.
- SC-002: Urgent selections map to a test oracle and existing fork contracts.
- SC-003: Equivalent fixes have local source/test references; unrun tests are not PASS.
- SC-004: Native SS upgrade is not approved until complete fetched objects, deadline
  clearing, old-client framing, UDP multi-user/revocation and AnyTLS coexistence pass.
- SC-005: The complete draft can be moved to Core without a duplicate Config spec.

Open gates: current remote HEAD/full SHA, complete newer native SS source, bounded
deadline-clearing verification, and upstream implementation/deployment authorization.
Draft002 is not an implementation commitment; promotion allocates the next Core spec ID.
