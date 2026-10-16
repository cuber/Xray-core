# SS2022 Safety

- Feature ID: 011-ss2022-safety
- Status: Implemented on the isolated candidate, with source-specific local
  verification recorded below; not adopted on develop and not deployed.
- Documentation split authorized: 2026-10-16, by the user's explicit request
  to separate the existing SS2022 safety contract from Core007/Core010.
- Candidate: `rebase/main-20261014`, fixed upstream
  `0888beb38a9ba45440557175e71ce21cf8ba3b13`; historical fork
  `1fd51943d81ed297636581724f6f2ebeaa50f62a`.
- This is ownership of existing implementation and evidence, not a new runtime
  increment, new test execution, release approval or production-state claim.

## Ownership

Core011 owns native SS2022 authentication deadlines, authenticated session policy,
single-user/multi-user/relay safety, dynamic-user identity, UDP admission/replay/
retirement, and SS-specific outbound early-return Close/resource ownership.
The current implementation is in [proxy/shadowsocks_2022](../../proxy/shadowsocks_2022/SAFETY.md).

[Core010](../010-upstream-main-upgrade/spec.md) retains fixed-main difference
review, cross-protocol/client compatibility integration, paired consumer/config
migration, whole-Core gates, build/release and adoption decisions. Its historical
U-001..003 and V-003..005 remain valid provenance IDs, not parallel SS contracts.
[Core007](../007-core-regression/spec.md) retains shared regression infrastructure,
readv/DoQ, DNS/TLS/VLESS/XHTTP/cache fixes and common tooling. Only the SS portion
of its historical V-032 and lostcancel evidence is delegated here.

Shared dispatch/pipe/worker lifecycle belongs to
[Core002](../002-core-connection-lifecycle/spec.md); traffic/control schema belongs
to [Core006](../006-user-domain-traffic/spec.md). SS identity must preserve those
consumer contracts without transferring their implementation ownership here.

## User Scenarios

### US-001: Bounded Authentication, Usable Sessions (P1)

Given a configured handshake budget and single-user, multi-user or relay entry,
when a peer is silent, partial, invalid or trickles later authentication bytes,
then the server retires the connection within the absolute budget, with no
successful dispatch or authenticated-user bytes. Given valid authentication,
when traffic continues beyond that budget, then exact early/late bytes still
flow under the authenticated user or relay destination's normal session policy.

### US-002: Independent Dynamic Identities (P1)

Given A and B managed through HandlerService and sharing a UDP transport,
when A roams, is removed or is re-added with a new key, then each dispatcher
context/reply retains its own identity, later old-key admission fails and B
continues. Already admitted/in-flight I/O is distinguished from new admission.

### US-003: Bounded Resource Ownership (P1)

Given invalid UDP waves, concurrent replay, quiet expiry or shutdown during
dispatch, when the wave or session retires, then no stale link/session/cipher is
republished and exact owned resources drain. Given a successful outbound Dial,
when an early TCP/UDP failure occurs in normal or TimeoutOnly mode, then the
connection is closed and owned cancellation/timer state is retired.

## Requirements

- FR-001: Single-user, multi-user and relay TCP set the underlying accepted
  connection's read deadline from level-0 Handshake policy before authentication.
  The budget is absolute, not renewed per byte. Zero means an immediate deadline,
  not disabled authentication protection. Deadline setup failure closes the socket.
- FR-002: Clear that deadline successfully at the authenticated boundary before
  dispatch/long-lived transfer; clearing failure must not dispatch. Normal idle
  and half-close policies use the authenticated user's or relay destination's
  level. Preserve exact early payload, complete data returned with EOF and later
  traffic; failure/cancellation releases owned buffers, links and sockets.
- FR-003: Preserve SIP022's native single-read salt/fixed-header rule. A short
  first read is rejected consistently via the native reset path, not repaired
  with ReadFull, authentication retry or an extended budget. Later variable-header
  reads remain under the original deadline. Relay removes only its outer EIH and
  preserves the downstream handshake/session/destination semantics.
- FR-004: Real HandlerService add/list/count/remove/re-add operations preserve
  independent identities. Reject duplicate email/key aliases rather than silently
  overwriting another principal. RemoveUser denies subsequent UDP admission,
  retires that identity's cached sessions, cancels pending dispatch and interrupts
  owned links. A new key creates a new identity; old keys stay denied and B is
  unaffected. Already admitted I/O may finish; authenticated TCP keeps its normal
  lifetime. No instantaneous wire-level revocation guarantee is added.
- FR-005: Single/multi-user UDP publishes session, cipher and replay state only
  after valid authentication and parsing. Invalid EIH/AEAD waves do not retain
  state proportional to rejected packets. Replay check-and-admit is serialized;
  concurrent duplicates admit exactly once and preserve sliding-window semantics.
  Relay validates its EIH destination only; it cannot authenticate the downstream
  AEAD payload and does not claim the same end-server authentication guarantee.
- FR-006: Each UDP session owns copied inbound metadata and its authenticated
  identity. Session-ID collision across users or relay destinations is rejected,
  not reused across principals. Roaming changes only that session's reply
  transport. Replies bind to the original session, not a lookup that can create
  or select its replacement. Retiring a session does not close shared UDP transport.
- FR-007: Session Close cancels pending dispatch without waiting behind a mutex
  held across network work; late returned links are interrupted, never published.
  Pointer-conditional removal cannot delete a replacement. Quiet sessions expire
  on timers without another packet; response completion retires its own session.
  Closed sessions/managers cannot recreate link, cipher or admission state.
- FR-008: SS outbound owns a successful Dial result immediately and closes it on
  early failure for TCP/UDP in normal/TimeoutOnly modes. Owned contexts and
  inactivity timers are stopped on return. SS transport retains correct idle,
  uplink-only and downlink-only transitions without disabling timeout handling.
- NFR-001: Use exact owned socket/session/window/worker counts and joined work as
  primary resource oracles; FD/goroutine/post-GC heap samples are diagnostics with
  documented warm-up/tolerances. Do not equate RSS reduction with leak repair.
- NFR-002: Record source revision/manifests, toolchain, platform, instrumentation,
  commands, failures, skips and limits. Historical experiments keep their original
  sources, IDs, dates and hashes. No prior result becomes a rerun at today's HEAD.

## Success Criteria

- SC-001: Deadline tests cover all three inbound variants, setup/clear failures,
  zero policy and successful traffic beyond the budget. Independent first-read
  controls establish the tested native multi-user/client combinations, not every
  possible relay/client configuration.
- SC-002: Dynamic RPC, UDP admission/replay/identity/expiry and shared-transport
  tests assert exact identities/bytes, B continuity and non-revival after removal.
- SC-003: Outbound failure and half-close tests plus repeated resource waves
  drain exact owned resources. The existing one-hour candidate soak remains
  scoped evidence, not an old/new hour-long comparison or universal leak proof.
- SC-004: [Tests](tests.md) map each requirement to an oracle and original IDs;
  [validation](validation.md) distinguishes executed evidence from unestablished
  coverage. Cross-client/multi-hop acceptance is still integrated by Core010.

## Non-Goals And Limits

No new runtime/test/dependency changes, release build, pin update, commit/push,
SSH/network test or deployment is authorized by this documentation split.
Do not restore singbridge, introduce a generic all-protocol handshake timeout,
change credentials, add a shared-key compatibility mode or tune host TCP/THP.
Duplicate rejection is an intentional candidate compatibility change, not an
SS2022 protocol requirement or the cause of the historical TCP FD repair.

Candidate evidence includes Darwin/arm64, Linux/arm64 race, and translated
Linux/amd64 package execution. Broader native-amd64 public-chain checks are
separate source-specific Core010/Config gates; they are not the same hourly
safety soak. No Windows execution, production canary/24-48h observation or
whole-server THP A/B is claimed here.

## Navigation

[Plan](plan.md) · [Tasks](tasks.md) · [Tests](tests.md) ·
[Validation and historical mapping](validation.md) ·
[Requirements checklist](checklists/requirements.md)
