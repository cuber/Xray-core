# Feature Specification: Loops Refused Before Start

- Feature ID: 018-startup-loop-checks
- Created: 2026-11-06
- Status: Approved (2026-11-06, scope); implemented 2026-11-06 (T002–T006); stability-check gate T007 open.
- Baseline: Core `5305dea7` (develop, spec017)
- Origin (user, 2026-11-06), after xray-config spec019 had planned to read spec017's loop guard counters through the
  Sidecar: "环路检查应该是一个xray-core启动的检查或者check的检查，出现环路禁止启动，而不需要改sidecar", then
  "启动检查还欠缺什么？" and "开 018".
- Consumers: [xray-config spec019](../../../xray-config/spec/019-balancer-fallback-chain/spec.md) (xrayctl's
  per-node checks N3/N4 refuse the same loops earlier, at render). No Sidecar or Portal change (Control Plane).

## Problem and Goal

Spec017 refuses a balancer fallback cycle before anything starts (L1–L5), and its table of loop classes leaves two
that one configuration also decides:

- **L-B loopback re-entry** is stopped only at run time: a connection passes each loopback once (spec017 L7).
- **L-C transport chain** (`streamSettings.sockopt.dialerProxy`, outbound to outbound) is a non-goal.

Both start without complaint. With Core `5305dea7` on macOS (2026-11-06, a temporary instance, one SOCKS inbound):

| Configuration | `xray run -test` | One connection |
|---|---|---|
| `a.dialerProxy = b`, `b.dialerProxy = a` | exit 0 | RSS 31 MB to 468 MB in 3 s, still growing; SIGTERM did not end the process within 50 s, `kill -9` did |
| rule `inboundTag: [in]` → loopback `lo` (`inboundTag: in`) | exit 0 | closed at once by L7; RSS unchanged |

A `dialerProxy` cycle has no guard at all: every dial opens the next hop's dial through an in-memory pipe, without
end, on every connection that reaches it.

Goal: a loop that one Core can decide from its own configuration forbids starting (`core.New` fails, so
`xray run -test` exits 23 and a service does not start), and the runtime APIs that change the same graphs refuse
a change that would create one. The runtime guards stay as the last defence only. What one Core cannot decide stays
with the consumer's static checks.

## Contract

### Loop classes (spec017's table, updated)

| Class | Path | Spec017 | This spec |
|---|---|---|---|
| L-A fallback cycle | fallback edges between balancers | refused at publish | unchanged |
| L-B loopback | routing (rules, balancers, default outbound) sends a connection to a loopback whose inbound routes it back | L7 at run time | refused before start and on change (S2); L7 stays |
| L-C transport chain | `dialerProxy` from outbound to outbound | non-goal | refused before start and on change (S1) |
| L-D across nodes | an outbound reaches another node whose routing comes back | consumer | unchanged |
| L-F scripted routing | a routing script (`routing.script`, which replaces the rules) reaches a loopback | L7 | unchanged: a script is not decidable; L7 ends it |
| L-G address self-loop | an outbound dials this instance's own listener by address | — | non-goal: Core cannot tell which addresses reach itself (DNS, NAT); the consumer infers it from its topology (xray-config spec019 F3) |

### S1 Transport chain

- **S1.1 Graph.** The vertices are the outbound handlers of the outbound manager. A → B when A's
  `streamSettings.sockopt.dialerProxy` names B, a self-reference included. Each handler has at most one edge.
- **S1.2 Refused.** The graph has no cycle. A `dialerProxy` naming no handler is not a loop and stays as today
  (the dial fails), because a handler may be added later through `HandlerService`.

### S2 Loopback routing

The question S2 answers: can a connection that a loopback feeds back into routing reach that loopback again?

- **S2.1 States.** One state per inbound tag that some loopback outbound feeds (its `inboundTag`). Only these
  states can be on a loop: a connection can come back to routing only through a loopback.
- **S2.2 From a state, in rule order.** A rule whose `inboundTag` excludes the state is skipped. Every other rule may
  match: `user` and every other condition (domain, IP, port, network, protocol, source, local address, attributes,
  process) are taken as possibly true. The rule leads to its `outboundTag`, or to every outbound its balancer may
  pick: each candidate its selectors match among the current handlers, each level's override target (which
  `SetOverrideTarget` does not restrict to candidates), and every level of its fallback chain (spec017) with the
  outbound that ends it; a last level without a `fallbackTag` may also end at the default outbound, since a pick
  that finds no candidate there goes to it (spec017 B4). A rule whose only condition is an `inboundTag` that includes the state
  certainly matches and ends the walk; otherwise the walk ends with the default outbound, which follows.
- **S2.3 From an outbound.** A loopback outbound leads to its `inboundTag`'s state. An outbound whose `dialerProxy`
  chain (S1) contains a loopback leads to that loopback's state too, since its transport re-enters routing there.
  Any other outbound leaves the instance and ends the path (L-D and L-G are not this check's).
- **S2.4 Refused.** The graph over the states has no cycle, a state reaching itself included.
- **S2.5 Why possibly-true is close to exact.** A loopback keeps the connection's destination, so a domain, IP, port
  or network condition that matched before the loopback matches again after it; only the inbound tag changes for
  certain. A configuration that relies on a source or local address condition to leave the loop is refused. This
  is stricter than upstream, deliberately (user: "出现环路禁止启动"); the fleet has no such configuration (S6).

### S3 Check points

- **S3.1 Start.** `core.New` runs S1 and S2 after every inbound and outbound handler is added
  (`initInstanceWithConfig`), on the complete instance. A failure fails `core.New`: `xray run -test` prints
  `Failed to start` and exits 23, and a service does not start (as spec017 L3).
- **S3.2 Routing changes.** `RoutingService.AddRule` (both modes) runs S2 on the rules and balancers it would publish,
  next to spec017's L1, before anything is published. `RemoveRule` runs S2 too: removing a rule that ended a walk can
  expose a later rule or the default outbound. `OverrideBalancerTarget` runs S2 with the new target, which may name
  any outbound; clearing an override removes an edge only. A refused change returns the error; the published rules
  and overrides stay.
- **S3.3 Handler changes.** `HandlerService.AddOutbound` runs S1 and S2 with the new handler before it becomes
  visible: it can add a `dialerProxy` edge, a loopback, a candidate a selector matches, or the default outbound when
  the manager has none. A refusal returns the error and the handler is not added. `RemoveOutbound` removes vertices
  and edges only, and cannot create a loop. `AlterOutbound` has no operation (nothing implements
  `OutboundOperation`), so it changes no edge.
- **S3.4 One check at a time.** Every check that guards a change and the change's publication run under one lock,
  so two concurrent changes cannot each pass against the other's old state (an `AddRule` routing to `lo` and an
  `AddOutbound` adding `lo` at the same moment). Connections never take this lock.

### S4 Diagnostics

The error ends with one of these (Xray prefixes the package path):

- `dialerProxy cycle: a -> b -> a`: as spec017 L4, every cycle from its smallest tag, the cycles in that order,
  joined by `; `.
- `loopback loop: in -> [rule sys:x] lo -> in`: each step names how the state leads on, then the outbound, then
  the state it reaches. The step is `rule <ruleTag>` (else `rule #<position>`, from 1), with
  `, balancer b` for a candidate, `, balancer b override`, `, balancer b1 -> b2 fallback` for the outbound
  ending a chain, or `, balancer b default`; `[default]` when no rule ended the walk. An outbound reaching a
  loopback through its `dialerProxy` chain reads `x via y via lo`. One cycle is printed per strongly connected set
  of states: the shortest through its smallest state, the sets in that order, joined by `; `.

### S5 Runtime guards

Spec017 L6, L7 and L8 stay unchanged. After this spec only L-F (scripted routing) can reach L7 in a configuration Core
accepted, and L6 still only a Core defect.

### S6 Compatibility

- Upstream accepts both refused shapes; a configuration that worked only because conditions S2 takes as possibly true
  never held at once is refused too. This is a fork divergence, recorded in spec001.
- Fleet (xray-config, 2026-11-06): no node configuration has a `dialerProxy` or a routing script; one loopback, the local
  source's `api` outbound, which feeds the empty inbound tag that no rule names. xrayctl `route loops` reports no loop
  for any of them, and spec019 N3/N4 refuse both shapes at render.

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | S1: a self-reference, two- and three-handler cycles and a handler leading into a cycle are refused with the S4 text; a 32-handler chain and a `dialerProxy` naming no handler are accepted | unit |
| FR-002 | S2: a rule to a loopback of its own inbound; a cycle through two loopbacks; a loop only through a balancer's candidate, only through a second fallback level, only through the default outbound, only through a `dialerProxy` chain ending at a loopback; each refused | unit |
| FR-003 | S2 accepts: an earlier rule with only the state's `inboundTag` that leads elsewhere; a loopback whose inbound no rule reaches again; the local source's `api` shape | unit |
| FR-004 | S3.1: `xray run -test` exits 23 with the S4 text for an S1 and an S2 configuration, and 0 for every accepted shape | CLI test |
| FR-005 | S3.2, S3.3: through a started instance's real gRPC server, `AddRule`, `RemoveRule`, `OverrideBalancerTarget` and `AddOutbound` that would create a loop are refused and leave routing, overrides and handlers as they were; accepted changes still apply | started-instance test |
| FR-006 | S3.4: concurrent `AddRule` and `AddOutbound` that only together close a loop: at most one succeeds, under `-race` | started-instance test |
| FR-007 | S5: L7 still ends a loop made by a routing script, which S2 cannot see | started-instance test |

## Control Plane

- **Runtime state (CP-1).** None. The checks refuse a configuration or a change; nothing a running instance loaded or
  decides is new. The error text of `AddRule`, `RemoveRule`, `OverrideBalancerTarget` and `AddOutbound` is the S4
  diagnostic, through the existing RPCs.
- **Consumers (CP-8).** Sidecar `/xray-debug` and Portal: non-goal (2026-11-06): there is no state to show, and a loop
  never runs (user: "出现环路禁止启动，而不需要改sidecar"). xray-config: xrayctl's render checks keep refusing the
  same shapes before deployment (spec019 N3, N4), N4 with S4's wording.

## Non-goals (2026-11-06)

- L-D, L-F and L-G (table above).
- A `dialerProxy` naming a balancer (Core never resolves one: the outbound manager looks up handlers only).
- An exception list for S2 (as xray-config spec019 F6).

## Notes

- Spec017's L-B text ("not decidable within one config") stays true for an exact answer; S2 answers the safe
  question instead: whether a loop is possible.
- `proxySettings` was removed upstream (`infra/conf` refuses it in favour of `dialerProxy`), so S1 has one edge kind.
