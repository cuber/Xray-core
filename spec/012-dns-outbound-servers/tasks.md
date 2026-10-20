# Tasks: DNS Servers Selected By Outbound

Based on spec.md and plan.md. Record evidence when a task completes; [P] does not
authorize sub-agents.

## Foundation

- [x] T001 Add `outbound_tag` to `NameServer`, regenerate `config.pb.go`, add JSON
  `outboundTags` with validation and policy signature — `app/dns/config.proto`,
  `infra/conf/dns.go`; depends: none; covers FR-001, FR-007, FR-008; verify:
  parser tests, regenerated file reproducible.

## US-001 / US-002

- [x] T002 [US-001] Add `IPOption.OutboundTag` and pass the handler tag from
  `targetStrategy` resolution — `features/dns`, `transport/internet/dialer.go`,
  `app/proxyman/outbound/handler.go`; depends: T001; covers FR-002; verify: unit test
  that the tag reaches the DNS client.
- [x] T003 [US-001] Restrict server eligibility by tag in `sortClients`, keep caches
  per server, log the tag — `app/dns/dns.go`, `app/dns/nameserver.go`; depends: T002;
  covers FR-003, FR-004, FR-005, FR-006, FR-009; verify: selection matrix tests.
- [x] T004 [US-002] Integration test with two DoH fixtures and two outbounds:
  per-outbound answers, cache isolation, fail closed — `app/dns` or `testing/scenarios`;
  depends: T003; covers US-001, US-002; verify: test passes and fails when the
  restriction is removed.

## US-003

- [x] T005 [US-003] Baseline ordering fixture equal to `1a05cc1` for configs without
  the field — `app/dns`; depends: T003; covers SC-002; verify: test.

Evidence (2026-10-20): `infra/conf/dns_outbound_test.go`, `app/dns/outbound_bound_test.go`
(isolation, cache, fail-closed, order, end-to-end targetStrategy; the end-to-end test fails
when the handler drops its tag). Existing ordering tests unchanged and passing.

## Close

- [x] T006 `go test ./...`, commit, `./build.sh` (Darwin + Linux) — depends: T004,
  T005; covers SC-001, SC-003; verify: logs and binary revision. Evidence: `a0831478`,
  built through `xrayctl build all` (`ff62acf` after a spec-only commit).
- [x] T007 Update status, index and fork inventory (`spec/README.md`,
  `001-core-fork-contracts`) — depends: T006; verify: links. The dated 2026-09-27
  `inventory.md` audit stays historical; the 001 contract table routes to 012.

## US-004 Control plane

- [x] T008 [US-004] Sanitized address on `Client`, `CacheController.size()`,
  `DNS.ControlSnapshot()` — `app/dns`; depends: T003; covers FR-010–FR-012.
- [x] T009 [US-004] `app/dns/command` proto/service, `api.services` name `DNSService`,
  distro registration — depends: T008; covers FR-010, FR-011.
- [x] T010 [US-004] Contract test over real gRPC (SC-004), `go test ./...`, build —
  depends: T009. Evidence: `app/dns/command/command_test.go` starts JSON instances and
  calls the API listener: loaded servers in order, userinfo stripped, sorted
  `outboundTags`, per-server cache counts after real lookups (1 bound, 2 unrestricted,
  0 with `disableCache`), default port 53, system resolver without `dns`, and
  `Unimplemented` without the service. Keeping userinfo fails it. `go test -race`
  for `app/dns/...` and `infra/conf`, and full `go test ./...`, pass.
