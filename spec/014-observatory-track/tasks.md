# Tasks: Tracked Outbound Observation

Based on spec.md and plan.md. Record evidence when a task completes.

## Foundation

- [ ] T001 Add `TrackGroup`/`Config.track` and `OutboundStatus.tracked_from`, regenerate
  the `.pb.go` files, and add JSON `track` with its shape checks.
  - Files: `app/observatory/burst/config.proto`, `app/observatory/config.proto`,
    `infra/conf/observatory.go`.
  - Depends: none. Covers: FR-002 (shape).
  - Verify: parser tests; the regenerated files are reproducible.

## Behaviour

- [ ] T002 Tracked copies in `createResult` and the mapping in `Check`.
  - File: `app/observatory/burst/burstobserver.go`.
  - Depends: T001. Covers: FR-001, FR-004, NFR-001.
  - Verify: unit tests, including the no-result-yet case and an unchanged result without
    `track`.
- [ ] T003 Load-time cross checks against the outbound tags (T5).
  - File: `infra/conf/xray.go`.
  - Depends: T001. Covers: FR-002.
  - Verify: one test for each refusal, and `run -test` on an accepted configuration.
- [ ] T004 Balancers over tracked candidates: roundrobin falls back and returns; leastPing
  picks by the probes' delay.
  - Files: `app/router` tests.
  - Depends: T002. Covers: FR-003, SC-003.
  - Verify: the tests fail when the tracked copies are removed.

## Control plane

- [ ] T005 CP-7 contract test: `GetOutboundStatus` on a started instance, with and without
  `track`.
  - Files: `app/observatory/command` or `testing/scenarios`.
  - Depends: T002. Covers: FR-005.
  - Verify: the test passes.

## Close

- [ ] T006 Full suite, commit, `./build.sh` (Darwin and Linux), and the stability gate
  (plan.md).
  - Depends: T003, T004, T005. Covers: SC-002, SC-003.
  - Verify: logs and the binary revision.
