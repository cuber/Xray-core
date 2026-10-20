# Tasks: DNS Servers Selected By Outbound

Based on spec.md and plan.md. Record evidence when a task completes; [P] does not
authorize sub-agents.

## Foundation

- [ ] T001 Add `outbound_tag` to `NameServer`, regenerate `config.pb.go`, add JSON
  `outboundTags` with validation and policy signature — `app/dns/config.proto`,
  `infra/conf/dns.go`; depends: none; covers FR-001, FR-007, FR-008; verify:
  parser tests, regenerated file reproducible.

## US-001 / US-002

- [ ] T002 [US-001] Add `IPOption.OutboundTag` and pass the handler tag from
  `targetStrategy` resolution — `features/dns`, `transport/internet/dialer.go`,
  `app/proxyman/outbound/handler.go`; depends: T001; covers FR-002; verify: unit test
  that the tag reaches the DNS client.
- [ ] T003 [US-001] Restrict server eligibility by tag in `sortClients`, keep caches
  per server, log the tag — `app/dns/dns.go`, `app/dns/nameserver.go`; depends: T002;
  covers FR-003, FR-004, FR-005, FR-006, FR-009; verify: selection matrix tests.
- [ ] T004 [US-002] Integration test with two DoH fixtures and two outbounds:
  per-outbound answers, cache isolation, fail closed — `app/dns` or `testing/scenarios`;
  depends: T003; covers US-001, US-002; verify: test passes and fails when the
  restriction is removed.

## US-003

- [ ] T005 [US-003] Baseline ordering fixture equal to `1a05cc1` for configs without
  the field — `app/dns`; depends: T003; covers SC-002; verify: test.

## Close

- [ ] T006 `go test ./...`, commit, `./build.sh` (Darwin + Linux) — depends: T004,
  T005; covers SC-001, SC-003; verify: logs and binary revision.
- [ ] T007 Update status, index and fork inventory (`spec/README.md`,
  `001-core-fork-contracts`) — depends: T006; verify: links.
