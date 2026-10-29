# Plan: Observatory Force Down

Based on [spec.md](spec.md). Baseline `31ef26bd`.

## Changes

| Area | File | Change |
|---|---|---|
| Proto | `app/observatory/config.proto` | `OutboundStatus.force_down = 9`, `force_down_reason = 10`, `force_down_since = 11` |
| Proto | `app/observatory/command/command.proto` | `ForceDownEntry`, `Get/SetForceDown{Request,Response}`, two RPCs on `ObservatoryService` |
| Set | `app/observatory/burst/forcedown.go` (new) | Immutable snapshot `forceSet` (tag → reason, since) behind an `atomic.Pointer`; `Observer.SetForceDown` validates (F3, F7), keeps `since` per F2, swaps once; `Observer.ForceDown` returns the entries |
| Result | `app/observatory/burst/burstobserver.go` | `probeResult` (measured) gains the force fields; `createResult` applies the selection view (F4): forced probes `alive=false` and `last_error_reason`, a synthetic dead status for a forced probe with no result yet, and tracked copies of the effective status; a new `measuredResult` keeps the measurement for the command service |
| Command | `app/observatory/command/command.go` | `GetOutboundStatus` and `GetTrack` read the measured view when the observer offers it; `GetForceDown`/`SetForceDown` through an optional `forceDowner`, `FailedPrecondition` on `Set` and an empty `Get` otherwise |
| Validation | `Observer.SetForceDown` | A tag is a probe when the observer's `HandlerSelector` selects it through a ping group selector and `track` does not list it |

The regenerated `*.pb.go` keep the header versions already in the tree (protoc-gen-go
v1.36.11, protoc v6.33.5 / the local toolchain recorded in spec014).

## Notes

- `forceSet` is immutable after construction; readers `Load()` it without a lock, writers build a
  new one and `Store()` it, serialised by a mutex held only by writers.
- Selection view versus measured view is one switch in one function (`resultOf(measured bool)`),
  so the two reads cannot drift apart.
- `GetObservation` keeps returning the selection view because every strategy and the Metrics handler
  call it; no strategy changes. With an empty set the function returns exactly today's result (NFR-001).
- The command service finds the measured view with an optional interface, so the classic
  observatory and the Rust observatory keep working unchanged.

## Verification

- Unit tests (`app/observatory/burst`): set validation (each refusal and the atomic no-change),
  `since` kept across re-sends and reason changes, selection view versus measured view, synthetic
  status before the first result, tracked copies, empty set unchanged.
- Router: roundrobin and leastPing balancers over forced probes with the fake observatory, moving
  to the other candidate, then to `fallbackTag`.
- CP-7 contract test (`app/observatory/command`): a started instance with `burstObservatory.track`,
  a real gRPC `ObservatoryService`: empty set by default, Set/Get round trip, `TestRoute` moves
  off the forced probe and its tracked outbounds, `GetOutboundStatus`/`GetTrack` measured with
  `force_down` set, release returns at once, restart starts empty, classic observatory refuses Set.
- Full suite: `go test ./... -count=1` with the geo data present; `-race -count=3` over
  `app/observatory/...` and `app/router/...`.
- Build: `./build.sh` (Darwin and Linux).
- Stability gate: observatory and router code is outside the `stability-check` protocol rows, so the
  gate is the local full suite plus `xrayctl test` of the target configs with the candidate Darwin
  build.
