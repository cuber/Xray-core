# Tasks: Observatory Force Down

Based on spec.md and plan.md. Record evidence when a task completes.

## Foundation

- [x] T001 Proto: `OutboundStatus.force_down*` and the `ForceDown` messages and RPCs; regenerate the
  `.pb.go` files.
  - Files: `app/observatory/config.proto`, `app/observatory/command/command.proto`.
  - Depends: none. Covers: FR-005 (shape).
  - Verify: the regenerated files are reproducible; `go build ./...`.

## Behaviour

- [x] T002 `forceSet`, `SetForceDown`/`ForceDown` on the burst observer, and the selection and
  measured views.
  - Files: `app/observatory/burst/forcedown.go`, `burstobserver.go`.
  - Depends: T001. Covers: FR-001, FR-002, FR-003, NFR-001.
  - Verify: unit tests (plan.md).
- [x] T003 Balancers over forced probes.
  - Files: covered by the T005 instance test, which runs the real router, conf and observer.
  - Depends: T002. Covers: FR-002, SC-003.
  - Verify: roundrobin moves to the partner, then `fallbackTag`; leastPing never picks a forced probe.

## Control plane

- [x] T004 `ObservatoryService.GetForceDown`/`SetForceDown`; `GetOutboundStatus` and `GetTrack`
  report the measured view with the force fields.
  - File: `app/observatory/command/command.go`.
  - Depends: T002. Covers: FR-005.
  - Verify: unit tests.
- [x] T005 CP-7 contract test on a started instance (plan.md), including restart and the classic
  observatory.
  - Files: `app/observatory/command/forcedown_contract_test.go`.
  - Depends: T003, T004. Covers: FR-001–FR-005, SC-001.
  - Verify: the test passes.

Evidence (2026-10-30):
- T001: `OutboundStatus.force_down = 9`, `force_down_reason = 10`, `force_down_since = 11`; `ForceDownEntry`,
  `Get/SetForceDown`. Regenerated with protoc 34.1, protoc-gen-go v1.36.11, protoc-gen-go-grpc v1.6.1 (the
  versions already in the headers).
- T002: `app/observatory/burst/forcedown.go`; `forcedown_test.go`: each refusal leaves the set unchanged, `since`
  kept across re-sends and reason changes and renewed after a release, the selection view (dead, reason, the
  measurement kept, tracked copy inherits, a synthetic status for a probe without a result), the measured view
  and `GetTrack`, and the release restoring the exact earlier statuses.
- T003–T005: `app/observatory/command/forcedown_contract_test.go` (CP-7, real instance and gRPC): refusals are
  `InvalidArgument`; with one probe forced, roundrobin, the tracked route copy and leastPing all take the partner;
  `GetOutboundStatus` and `GetTrack` stay alive with `force_down` set; both forced fall back to `block`, not the
  default outbound; release restores roundrobin over both; a new instance starts empty; the classic observatory
  answers `GetForceDown` empty and `SetForceDown` `FailedPrecondition`.
- `go test -race -count=3` over `app/observatory/...` and `app/router/...` passes; `go test ./... -count=1`
  passes (exit 0, 105 packages).

## Close

- [ ] T006 Full suite, `-race -count=3` over the touched packages, commit, `./build.sh` (Darwin
  and Linux), `xrayctl test asz.lite --all` with the Darwin build.
  - Depends: T001–T005. Covers: SC-002, SC-003.
  - Verify: logs and the binary revision (`vcs.modified=false`).
