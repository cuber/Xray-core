# Plan: Tracked Outbound Observation

Based on [spec.md](spec.md). Baseline `cf979cbf`.

## Changes

| Area | File | Change |
|---|---|---|
| Proto | `app/observatory/burst/config.proto` | `message TrackGroup {string probe = 1; repeated string outbounds = 2;}`, `Config.track = 5` |
| Proto | `app/observatory/config.proto` | `OutboundStatus.tracked_from = 8` |
| JSON | `infra/conf/observatory.go` | `BurstObservatoryConfig.Track []*trackGroupSettings` (`track`), built into `burst.Config.track`; shape checks (probe set, non-empty `outbounds`, no duplicate tag across groups, probe not tracked) |
| Load check | `infra/conf/xray.go` (`Config.Build`) | With the outbound tags in hand: probe and tracked tags exist, the probe is selected by a subject selector (prefix match over tags, as the observer's `Select`), no tracked tag is selected |
| Result | `app/observatory/burst/burstobserver.go` | `createResult` appends a copy of each probe's status per tracked tag with `OutboundTag` and `TrackedFrom` set; `Check` maps tracked tags to their probes, deduplicated |
| Command | `app/observatory/command` | none: `GetOutboundStatus` returns `GetObservation`'s result |

The regenerated `*.pb.go` keep the header versions already in the tree (protoc-gen-go
v1.36.11, protoc v6.33.5).

## Notes

- The tracked index (tag → probe) is built once from the config when the observer is
  created. `createResult` reads it without a lock, because it is immutable after start.
- Probes and tracked tags are disjoint (T5), so the health ping manager never sees a
  tracked tag. Its `Start` and `Select` stay as they are.
- `leastLoad` reads `health_ping` from the result. The tracked copies carry it, so no
  strategy needs a branch.

## Verification

- Unit tests (`app/observatory/burst`):
  - result copies, including the no-result-yet case;
  - `Check` mapping;
  - nothing new without `track`.
- Conf tests (`infra/conf`): each T5 refusal, and an accepted configuration.
- Router tests (`app/router`): roundrobin and leastPing balancers over tracked
  candidates, with a fake observatory that turns the probe dead and back alive.
- CP-7 contract test: a started instance with `burstObservatory.track` and a real gRPC
  `ObservatoryService`.
- Full suite: `go test ./... -count=1` with the geo data present.
- Build: `./build.sh` (Darwin and Linux).
- Stability gate: the `stability-check` skill selects suites by the code a change
  reaches. Observatory and router code is outside its protocol rows, so the gate is
  the local full suite plus `xrayctl test` of the target configs with the candidate
  Darwin build. The skill's "when in doubt" applies if a change touches dispatch.
