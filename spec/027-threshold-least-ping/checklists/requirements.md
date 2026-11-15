# Design Checklist

- [x] Core owns this contract; approval and implementation/rollout boundaries are explicit.
- [x] A new strategy type; leastPing and every other strategy unchanged (H06, E10).
- [x] An older Core rejects the configuration instead of ignoring it (C10).
- [x] Required maxRTT, optional recoverRTT, exact duration precision, zero/unknown/invalid measurements.
- [x] Raw reachability and per-balancer eligibility are separate.
- [x] Primary, fallback, degraded, pending and terminal outcomes; never the default outbound.
- [x] Hysteresis latches change only in a computation, follow the observation sequence and are local.
- [x] Observation changes trigger computations; one observation per pass; children before parents;
  pre-publication pass on reload; coalescing without goroutines or timers; picks read only.
- [x] One level of lookahead; child results opaque; roundRobin/random member mean with coverage.
- [x] Every choosable outbound observed, enforced at load time; blackhole exempt.
- [x] Override precedence with true state; track and force-down provenance; flows survive changes.
- [x] Control plane: strategy name as the capability, additive fields, one pass per response, observation age.
- [x] Every requirement maps to cases; consumers and rollout have separate owners.
- [x] Compared with the original `291fde47` text: reported status per mode, override bubbling, no fabricated
  child target, rollout requirements, read-only observation, bounded state, test hygiene restored.
- [x] Every source of a decision change has a trigger: probe results, cleanup, force down, Hy2 relay status,
  runtime outbound changes, rule publication, override.
- [x] leastPing cannot silently accept the earlier draft's `maxRTT` syntax (TL-9).
- [x] Every design decision confirmed (D1–D16, 2026-11-15); no open question.
- [x] No fleet addresses, credentials, domains or production snippets in this public spec.
- [ ] Implementation and recorded evidence.
- [ ] Build, stability gate and any separately authorized rollout.
