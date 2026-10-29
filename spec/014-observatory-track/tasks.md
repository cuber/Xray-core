# Tasks: Tracked Outbound Observation

Based on spec.md and plan.md. Record evidence when a task completes.

## Foundation

- [x] T001 Add `TrackGroup`/`Config.track` and `OutboundStatus.tracked_from`, regenerate
  the `.pb.go` files, and add JSON `track` with its shape checks.
  - Files: `app/observatory/burst/config.proto`, `app/observatory/config.proto`,
    `infra/conf/observatory.go`.
  - Depends: none. Covers: FR-002 (shape).
  - Verify: parser tests; the regenerated files are reproducible.

## Behaviour

- [x] T002 Tracked copies in `createResult` and the mapping in `Check`.
  - File: `app/observatory/burst/burstobserver.go`.
  - Depends: T001. Covers: FR-001, FR-004, NFR-001.
  - Verify: unit tests, including the no-result-yet case and an unchanged result without
    `track`.
- [x] T003 Load-time cross checks against the outbound tags (T5).
  - File: `infra/conf/xray.go`.
  - Depends: T001. Covers: FR-002.
  - Verify: one test for each refusal, and `run -test` on an accepted configuration.
- [x] T004 Balancers over tracked candidates: roundrobin falls back and returns; leastPing
  picks a tracked outbound.
  - Files: covered by the T005 instance test, which runs the real router, conf and observer.
  - Depends: T002. Covers: FR-003, SC-003.
  - Verify: `TestTrackContractWithoutTrack` shows the same roundrobin never falling back
    without `track`.

## Control plane

- [x] T005 `ObservatoryService.GetTrack`, `GetOutboundStatus` without tracked statuses, and the
  CP-7 contract test on a started instance, with and without `track`.
  - Files: `app/observatory/command` (proto, service), and the contract test there or in
    `testing/scenarios`.
  - Depends: T002. Covers: FR-005.
  - Verify: the test passes.

Evidence (2026-10-29):
- `app/observatory/burst/track_test.go`: copies, the no-result case, `Check` mapping, shape and
  cross checks, and the observer's result and `GetTrack`.
- `infra/conf/observatory_track_test.go`: the build and each T5 refusal through `Config.Build`.
- `app/observatory/command/track_contract_test.go` (CP-7, real instance and gRPC):
  - `GetTrack` groups and probe status, and `GetOutboundStatus` without tracked tags;
  - roundrobin falls back when the probe's endpoint fails and returns when it recovers;
    leastPing picks the tracked outbound;
  - without `track`, no groups, and the same roundrobin never falls back.
- `go test -race -count=3` over `app/observatory/...`, `infra/conf` and `app/router/...` passes.
- `go test ./... -count=1` passes: exit 0, 105 packages.
- The generated `.pb.go` record protoc v7.34.1 and protoc-gen-go-grpc v1.6.1, the local toolchain.

## Close

- [x] T006 Full suite, commit, `./build.sh` (Darwin and Linux), and the stability gate
  (plan.md).
  - Depends: T003, T004, T005. Covers: SC-002, SC-003.
  - Verify: logs and the binary revision.

Evidence (2026-10-29, recorded with the next Core change, spec015, so the gate and the deployment
saw the same revision): `31ef26bd` built by `xrayctl build core` with `vcs.modified=false`; asz.lite's
configuration passed `run -test` with the Darwin build; `switch upgrade asz.lite` (Core interruption
198 ms) and `acceptance check --baseline` PASS with `observatory_track` `runtime ok: ahk.ecs-hy2<-8`
(xray-config spec015 T005).
