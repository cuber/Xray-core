# Tasks: Loops Refused Before Start

- [x] T001 spec, plan and tasks; index row in `spec/README.md`; spec017's loop table and Control Plane point here.
- [x] T002 S1: `DialerProxy()`, `checkDialerProxyCycles`; unit FR-001.
- [x] T003 S2: loopback states, rule classification with the `protoreflect` test, balancer levels and overrides,
  `Router.CheckLoops`, S4 diagnostics; units FR-002, FR-003.
- [x] T004 S3.1: `Manager.CheckLoops` from `initInstanceWithConfig`, the routing-less default case; CLI FR-004.
- [x] T005 S3.2–S3.4: `graphMu`, checks in `reloadRules`, `RemoveRule`, `SetOverrideTarget` and `AddHandler`;
  started-instance FR-005, FR-006; FR-007 (L7 still ends a routing script's loop).
- [x] T006 spec001 fork inventory (S6 divergence).
- [x] T007 Full `go test ./... -count=1`, race and format checks; `xrayctl build core` from the committed tree;
  stability-check gate (dispatcher row).
- [x] T008 Consumers: xray-config spec019 N4 takes the S4 wording; no Sidecar or Portal change (Control Plane).

T007 results go to xray-config first; they come here with the next Core code change (stability-check: no doc-only
Core commit between the gate and the deployment).

## Evidence

- 2026-11-06 (before implementation, Core `5305dea7` darwin, temporary instances, scratchpad configs): a `dialerProxy`
  pair a ↔ b passes `run -test` (exit 0); one SOCKS connection grows RSS from 31 MB to 468 MB in 3 s, SIGTERM does not
  end the process within about 50 s, `kill -9` does. A rule sending its own inbound to a loopback of that inbound
  passes `run -test` (exit 0); L7 closes the connection at once (curl exit 56) and RSS stays at 31 MB.
- 2026-11-06 fleet (xray-config `e30bbdfd`): no node `config.json` has a `dialerProxy` or a routing script; the only loopback
  is the local source's `api`; `xrayctl route loops` 131 states, 234 edges, 0 loops.
- 2026-11-06 T002–T006 (on `cf2d80a1`):
  - `common/loopcheck` (`TestFunctional`, a 64-vertex chain); spec017's `checkFallbackCycles` moved onto it, its
    `TestFallbackCycleCheck` unchanged and passing.
  - FR-001 `TestDialerProxyCycles`, routing-less default `TestDefaultLoopWithoutRouting`; FR-002
    `TestLoopbackLoopsRefused` (11 shapes, exact texts), FR-003 `TestLoopbackLoopsAccepted`,
    `TestRoutingRuleConditions`; FR-004 `TestRunTestRefusesLoops` (three refused with exit 23, two accepted with
    exit 0); FR-005 `TestLoopChecksOnChange`; FR-006 `TestLoopChecksAreSerialised` (with the router's lock removed
    it failed 5 of 10 runs, "both the rule and the loopback were accepted"); FR-007 `TestLoopbackReentry`.
  - Changed for this spec: spec017's `TestLoopbackReentry` and `TestFallbackChainContract` made their loop with
    rules, which start now refuses; the loop now comes from a routing script (`TestLoopGuardsContract` holds the
    CP-7 counter). spec008's `TestAnyTLSOutboundFailureRecovery` installed `dialerProxy` cycles through
    `AddOutbound`; those cases now expect the refusal (spec008 coverage C-05 notes it).
  - Found on the way: the routing and DNS script engines published their pool without synchronisation; fixed
    separately as spec007 FR-004.
  - Fleet: the 46 node `config.json` give the same `run -test` result with this build as with `5305dea7`;
    none names a loop.
  - `go test -race -count=3` on `common/loopcheck`, `app/proxyman/outbound`, `app/router`, `app/router/command`,
    `proxy/loopback`, `app/observatory/command`, `core`, `main`, `app/dns`: pass; `vformat` applied.
- 2026-11-06 T007, T008 (on `1d3186e0`, with spec007 FR-004 `4307097b`; carried here with the next Core change):
  - `go test ./... -count=1`: 108 of 108 packages once spec008's AnyTLS failure-recovery cases expected the
    refusal (above); race as listed for T002–T006.
  - `xrayctl build core`: `vcs.modified=false`, SHA-256 `857919b02174f87b203157dc746df18884ed6c5b0c6b06d365ddc50769c58452`.
    `singmux-check` A/B against asz.lite's `5305dea`, chaos 600 s + settle 15 s + quiet 120 s, the server rotated over
    hk.akile.lite and la.akile.lite: baseline and candidate PASS in both runs, no panic, handlers and carriers at 0,
    no corrupted byte, no TCP failure in the quiet phase and at least 97.5% UDP echoed there; candidate peaks within
    the baseline's (goroutines 1123/1162 and 1166/1163). asz.lite runs `1d3186e` since 2026-11-06 (Core interruption
    323 ms, acceptance PASS); the full record is xray-config spec019's tasks.
  - T008: xray-config `1dfc0fd0`: N4 reports a `dialerProxy` cycle in S4's wording.
