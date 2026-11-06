# Tasks: Loops Refused Before Start

- [x] T001 spec, plan and tasks; index row in `spec/README.md`; spec017's loop table and Control Plane point here.
- [ ] T002 S1: `DialerProxy()`, `checkDialerProxyCycles`; unit FR-001.
- [ ] T003 S2: loopback states, rule classification with the `protoreflect` test, balancer levels and overrides,
  `Router.CheckLoops`, S4 diagnostics; units FR-002, FR-003.
- [ ] T004 S3.1: `Manager.CheckLoops` from `initInstanceWithConfig`, the routing-less default case; CLI FR-004.
- [ ] T005 S3.2–S3.4: `graphMu`, checks in `reloadRules`, `RemoveRule`, `SetOverrideTarget` and `AddHandler`;
  started-instance FR-005, FR-006; FR-007 (L7 still ends a routing script's loop).
- [ ] T006 spec001 fork inventory (S6 divergence).
- [ ] T007 Full `go test ./... -count=1`, race and format checks; `xrayctl build core` from the committed tree;
  stability-check gate (dispatcher row).
- [ ] T008 Consumers: xray-config spec019 N4 takes the S4 wording; no Sidecar or Portal change (Control Plane).

T007 results go to xray-config first; they come here with the next Core code change (stability-check: no doc-only
Core commit between the gate and the deployment).

## Evidence

- 2026-11-06 (before implementation, Core `5305dea7` darwin, temporary instances, scratchpad configs): a `dialerProxy`
  pair a ↔ b passes `run -test` (exit 0); one SOCKS connection grows RSS from 31 MB to 468 MB in 3 s, SIGTERM does not
  end the process within about 50 s, `kill -9` does. A rule sending its own inbound to a loopback of that inbound
  passes `run -test` (exit 0); L7 closes the connection at once (curl exit 56) and RSS stays at 31 MB.
- 2026-11-06 fleet (xray-config `e30bbdfd`): no node `config.json` has a `dialerProxy` or a routing script; the only loopback
  is the local source's `api`; `xrayctl route loops` 131 states, 234 edges, 0 loops.
