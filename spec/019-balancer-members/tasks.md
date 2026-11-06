# Tasks: Balancers As Members

- [x] T001 spec, plan and tasks; index row in `spec/README.md` (reviewed 2026-11-06: "可以，开始吧").
- [x] T002 M1–M3: proto and JSON field, candidates, picks that continue in a member; FR-001.
- [x] T003 M4, M5: member status and the member observer; FR-002.
- [x] T004 M6: observation subjects and the checks at start, `AddRule` and `AddOutbound`; FR-003.
- [x] T005 M7–M9: the balancer graph check and diagnostic, the pick guard, spec018 S2 over members; FR-004, FR-005.
- [x] T006 M10: control plane fields and the CP-7 contract; FR-006; FR-007.
- [x] T007 Full suite, race, format; `xrayctl build core`; `xrayctl gate plan` and its gate.
- [x] T008 spec001 inventory; consumers in xray-config spec021.

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
- 2026-11-06 T007, T008 (on `47200883`; carried here with the next Core change, stability-check):
  - `go test ./... -count=1`: 108 of 108 packages; `-race -count=3` on the touched packages; `gofmt` clean.
  - `xrayctl build core` from the committed tree: `build/xray-linux-amd64` `vcs.modified=false`, SHA-256
    `99680b386620c3adaf9c3682ac734beb8d8c8112e883e9be2c555244a88d7a53`. `xrayctl gate plan --node hq` (base hq's
    `1d3186e`) gave L0: the sing-mux workload went through no balancer, so none of this code ran there. The L0 smoke
    (`singmux-check`, server hk.akile.lite, client la.akile.lite, chaos 60 s, quiet 30 s, candidate only) passed in
    349 s with no TCP failure in the quiet phase, every UDP datagram echoed, no panic, handlers and carriers at 0.
  - T008: the spec001 row came with `47200883`; xray-config spec021 (`87a5536a`) generates hq's node balancers and
    checks them (N1, N2, N7, `route trace`, `route loops`, firewall use, the deploy gate). hq runs `4720088` since
    2026-11-06: a 229 ms interruption, acceptance PASS; `xray api bi` shows each node balancer on its faster
    live protocol (AnyTLS where a Hy2 does not come back to the home line).
  - The gap the L0 showed, closed in the commit carrying this record: the sing-mux fixture's bulk target
    `alpha.test` goes through hq's shape (weightedLeastPing over an outbound and a leastPing node balancer,
    observed through a probe the echo answers), so `gate plan` now measures balancer picks as hot. Over 8
    instances, per instance:
    `Router.resolveChain` 87, `statusReader.balancer` 117, `LeastPingStrategy.PickOutbound` 105,
    `WeightedLeastPingStrategy.PickOutbound` 52 (the threshold is 16). xray-config's `singmux-check` server has
    the same shape and requires a Core with this spec to carry the echo through the node balancers.
    `common/mux/singmux` with the external clients, strict: pass, and `-race -count=3`: pass.
