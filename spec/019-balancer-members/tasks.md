# Tasks: Balancers As Members

- [x] T001 spec, plan and tasks; index row in `spec/README.md` (reviewed 2026-11-06: "可以，开始吧").
- [x] T002 M1–M3: proto and JSON field, candidates, picks that continue in a member; FR-001.
- [x] T003 M4, M5: member status and the member observer; FR-002.
- [x] T004 M6: observation subjects and the checks at start, `AddRule` and `AddOutbound`; FR-003.
- [x] T005 M7–M9: the balancer graph check and diagnostic, the pick guard, spec018 S2 over members; FR-004, FR-005.
- [x] T006 M10: control plane fields and the CP-7 contract; FR-006; FR-007.
- [ ] T007 Full suite, race, format; `xrayctl build core`; `xrayctl gate plan` and its gate.
- [ ] T008 spec001 inventory; consumers in xray-config spec021.

## Evidence

- 2026-11-06 T002–T006 (on `9844a9ff`):
  - T002: `BalancingRule.balancers` (config.proto, regenerated with protoc 33.5 as the file was), JSON `balancers`
    (`TestBalancerMembersJSON`: alone or beside a selector, empty or repeated refused, a member sharing an
    outbound's tag refused); `SelectOutbounds` adds the members; `resolveChain` continues into a member; an
    override target naming a balancer is refused. `TestMembersArePickedAsNodes`: route (weightedLeastPing,
    weights a 2, b 1, x 1) over node balancers a, b (leastPing) and x: 400 picks give `a-hy2` 200, `b-anytls` 100,
    `x` 100, the faster protocol inside each node.
  - T003: `TestMemberStatusBubblesUp` (level, override, fallback outbound, fallback balancer, blackhole, unknown);
    `TestOuterStrategiesSkipADownMember` (weightedLeastPing, leastPing, roundrobin, random as the outer level);
    `TestAFreshLeastPingMemberUsesItsFallback` (no observation: the member's fallback, never the default).
  - T004: `main/run_balancer_members_test.go`: exit 23 for a member without a fallback, an unobserved candidate,
    an unobserved fallback and a member cycle; exit 0 for the accepted shape with a blackhole fallback.
  - T005: `checkBalancerGraph` (Tarjan over member and fallback edges); spec017's fallback-only texts and tests
    unchanged; `TestBalancerGraphWithMembers` (self-member, two members, member falling back, fallbacks alone, no
    balancer, listed twice, no fallback, a 32-level DAG); `TestLoopbackThroughAMember` (spec018 S2).
  - T006: `BalancerMsg.members`, `.status` (command.proto, protoc 34.1); `TestBalancerMembersContract` through a
    real gRPC server: flat `ListBalancers`; a's faster protocol forced down keeps a up, both down make a down and
    route stops picking it, everything down sends route to its blackhole fallback; `AddOutbound` of an unobserved
    outbound under route and `AddRule` of an unobserved member are refused. `TestMembersUnderConcurrentReloads`
    (FR-007): picks during 50 rounds of flat/nested replaces and a refused member cycle, `-race`.
  - Found on the way: the classic observatory handed out statuses its prober updates in place (spec007 FR-005,
    fixed separately); the fleet uses burstObservatory.
