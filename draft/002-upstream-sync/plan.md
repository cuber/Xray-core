# Technical Plan (Proposed, Not Implemented)

## Decision

Prefer **selective, evidence-driven backports**, not a wholesale rebase to origin.
The incident fix, upstream security/correctness changes, and architectural migration
have different acceptance and rollback boundaries. Do not hide finalRules, SS wire
framing or a Go/toolchain upgrade in an alleged memory-only fix.

Each future implementation increment should own a named contract/test and cite its
upstream provenance. Preserve the current develop baseline; do experiments on a
separate branch when a broad upstream migration is approved. This draft itself
does not authorize implementation or deployment. The user separately approved the
narrow spec008 address-adapter adoption on canonical develop on 2026-10-05;
that does not authorize the proposed backports below. Promotion moves draft002
to the next Core spec ID (currently 009).

## Phase A: Evidence and Freshness

1. Reproduce fixed-ref inventories and read all 67 overlapping paths before editing
   their modules. Add fetched September 30+ full objects when network is available.
2. Reconcile web short SHAs to full SHAs/parents; re-pin the intended target.
   Keep the old evidence dated rather than silently replacing its baseline.
3. Review native SS InitServerStream/TransportTCP: deadline setup/clear, early
   data, buffer ownership, replay protection, dispatch failures, UDP user sessions.
4. Record current config and dependencies separately from server/binary versions.

Gate: full native-engine adoption cannot proceed with web fragments alone. Narrow
fixes whose full source/patch is already present locally can be evaluated first.

## Phase B: Small Independent Fixes

| Increment | Files / ownership | Gate |
|---|---|---|
| B1 QUIC malformed sample | common/protocol/quic | V-002; real decoder corpus, no panic; no protocol feature bump |
| B2 SS handshake deadline | proxy/shadowsocks_2022; existing policy | V-003/004/005; no generic all-protocol worker timeout |
| B3 DNS name errors | app/dns buildReqMsgs and resolver call sites | V-006; correct A/AAAA error fan-out, no blocked queries |
| B4 Buffered writer | common/buf | V-007; exact payload and partial/error count semantics |
| B5 Stats atomic registration | features/stats, app/stats, dispatcher/handler callers | V-008/012; all managers/mocks updated, proto compatibility retained |
| B6 Router snapshots | app/router + command API | V-009/012; coherent generations, cleanup, detailed ListRule |

Order B1 first for security. B2 is directly related to pgz.luck and should precede
speculative broad memory rewrites. B3/B4 can be independent. Do not deploy any
increment based only on unit pass; run the full fork gates and build both platforms.

### SS Deadline Boundary

Set policy.ForLevel(0).Timeouts.Handshake before SS authentication because the user
is not yet known. After authentication selects a MemoryUser, clear that deadline
before entering dispatcher/long-running copy and select that user's session policy.
For single-user and relay variants, explicitly verify which configured level applies
and keep the pre-auth boundary bounded; do not silently use arbitrary user levels
before authentication. Existing policy semantics, including zero/disabled timeout
if supported, require a documented decision and matching tests.

The sing engine calls our NewConnection callback only after authenticating. A
narrow fix must clear the **underlying accepted connection** at that boundary,
including wrapper forwarding of SetReadDeadline; do not simply defer clearing until
service.NewConnection returns. Cleanup on failed authentication/dispatch is owned
by the inbound and existing worker lifecycle. Reuse that ownership, no global
socket registry or per-connection unbounded timer goroutines.

No mass user/key/route changes. Valid UDP and dynamic AddUser/RemoveUser remain
covered even though the timeout is TCP-only. Any needed UDP revocation change must
be explicit, not an incidental new behavior in a memory fix.

### Router Publication Boundary

Prefer an immutable **combined routing state** or a proven coherent snapshot
generation rather than independently publishing rules and balancers. Build/validate
before publishing; failed replacement leaves the prior state functional and closes
all newly owned resources. Never close resources still used by a reader without
an explicit lifetime strategy. Do not hold a global router lock across network work.
Preserve existing balancer locks where they protect mutable selection state.

## Phase C: Measured Memory Improvements

1. Adapt weak matcher cache and registry as a pair. Review domain case-normalization
   deliberately and retain the safety of matchers referenced by live routing state.
2. Evaluate compact MPH memory work only after newer objects are fetched. Use an
   independent matcher oracle and pinned production-like DAT, not only upstream tests.
3. Measure build peak, post-build/post-GC retained heap, matcher/object counts and
   latency/allocation per lookup. Retired matchers should become collectible;
   actively referenced rules are expected to retain storage.
4. Run a separate THP A/B experiment. Different binary/toolchain/kernel changes in
   one run make attribution invalid. GOMEMLIMIT is a soft heap budget, not an RSS
   cap and not a socket-leak repair.

Gate: no live-rule correctness regression, no monotonically retained retired matcher
set, no unacceptable throughput/latency regression. Define performance budgets from
the reproducible baseline before approval; no invented measured percentage benefit.

## Phase D: Conditional Protocol/Transport Backports

- Audit XHTTP explicit body closure and c.closed race. These are not already fixed
  by WaitReadCloser. Review active use before prioritizing above public entry defects.
- Review SS bridge post-auth timer changes if retaining sing SS. Existing normal
  policy timeouts are preferred over blindly importing 300s/2s/5s literals. If the
  native engine supersedes these paths, do not backport a bridge rewrite just to delete it.
- Hy2 upgrades must keep sendThrough/interface binding, user validator mappings,
  UDP packet size/ChromeParrot, manager lifecycle and certificate API compatible.
- Preserve burst 3s probing, per-prefix URLs, query ob=, health hysteresis and
  Close/join. Review overlapping-round cancellation separately from interval clamps.
- Keep Mux.Cool open issue evidence separate from merged fixes. Add fault-injection
  tests without assuming native AnyTLS pools and Mux.Cool share implementations.

## Phase E: Full Upstream Migration (Deferred)

Prerequisites: a full fetched/pinned target, accepted test results for specs002-008,
config migration for all ipsBlocked instances, AnyTLS/sing coexistence, native SS
interoperability, Hy2/Finalmask transport migration, Go/dependency build support and
Sidecar/TUI protobuf compatibility. clients/network aliases alone do not require
mass rewrites. New unused TUN/MASQUE/XDRIVE features are not migration goals.

Retain backup ref, compare parentage/tree changes, and do not reset/rebase/push
develop without a separately explicit request. A future full migration should be
an independent scoped spec, not a continuation implied by this WIP.

## Future Release and Rollback Boundary

After implementation approval and local tests, commit Core changes before the
official xrayctl/build.sh wrapper; build Darwin and Linux with VCS metadata. No
dirty-tree release build, upstream zip, injected display version or implicit fetch.
Update consumer pins only when authorized. Deployment requires its own scope/SOP.

If pgz.luck is later authorized as canary, preserve config/usersync/admin path and
compare installed revision plus socket/heap counters before and after. Monitor
24-48h with representative workload. A failed canary rolls back the previous
locally built artifact/config through xrayctl, not an upstream version substitution.
