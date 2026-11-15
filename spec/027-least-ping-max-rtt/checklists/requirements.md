# Design Checklist

- [x] Specification approval and implementation/deployment boundaries are explicit; Core owns this contract.
- [x] Core spec027 is allocated independently, promoted from draft005 whose ID remains reserved.
- [x] Current leastPing, leastLoad and weightedLeastPing behaviours are distinguished.
- [x] Strategy-settings placement is confirmed and the earlier root-level sketch is not called supported.
- [x] Exact threshold equality, precision, zero, unknown and invalid observations are specified.
- [x] Raw reachability and per-balancer qualification remain separate.
- [x] Startup, first sample, recovery, track, force down and override are covered.
- [x] All-slow degradation differs from all-unusable drop; down/forced/refused options cannot be revived.
- [x] Fallback balancer is one opaque bubbled result; parent never flattens its descendants or selects for it.
- [x] Roundrobin/random mean, measured coverage and side-effect-free reads are specified for the enabled view;
  non-opted-in bubbling and actual child scheduling remain unchanged.
- [x] Fallback is optional; explicit blackhole vs drop has defined choice/fallback flags and no default escape.
- [x] Immediate startup probe, pending, bounded business readiness and existing manual override are clarified.
- [x] Shares, selected target and fallback.active match degraded-primary vs degraded-fallback choices.
- [x] Control Plane section includes runtime authority, additive fields, older Core, bounded reads and owners.
- [x] Planned verification jointly covers candidate selection, status/shares/targets and RPC snapshot consistency.
- [x] Planned tests distinguish deterministic boundary, real native dispatch, real gRPC and consumers.
- [x] spec.md defines requirement-to-case mapping, independent fixtures, native/API/browser methods,
  regression commands, release boundaries and explicit evidence/pass-fail requirements.
- [x] The public spec uses no fleet addresses, credentials, domains or production config excerpts.
- [x] User clarified the design/schema on 2026-11-14; no default positive threshold is supplied.
- [x] Promoted all authoritative documents to spec027 on 2026-11-14; indexes and references updated.
- [x] 2026-11-15 amendment: hysteresis band (LP-7) with read-side-effect-free latches and a load-time
  observed-outbound check for enabled balancers (LP-8) are specified with their cases.
- [ ] Runtime implementation authorized for execution; operational thresholds are chosen separately.
- [ ] Feature code/tests/protobuf/consumers implemented and actual evidence recorded.
- [ ] Builds/stability gate and any separately authorized rollout/restore accepted.
