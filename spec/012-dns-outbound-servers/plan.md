# Implementation Plan: DNS Servers Selected By Outbound

- Spec: [spec.md](spec.md)
- Status: Implemented (selection `a0831478`; control plane US-004).

## Technical Context

- `features/dns/client.go`: `IPOption{IPv4Enable, IPv6Enable, FakeEnable}` and
  `Client.LookupIP(domain, option)`.
- `app/dns/dns.go`: `LookupIP` (line 255) and `sortClients(domain)` (line 309).
- `app/dns/nameserver.go`: `Client` wraps one server and its `CacheController`.
- `app/dns/config.proto`: `NameServer` fields up to 18; next free number 19.
- `infra/conf/dns.go`: JSON `NameServerConfig`, policy signature (line 300-365).
- `app/proxyman/outbound/handler.go` `Dispatch`: `targetStrategy` calls
  `internet.LookupForIP(domain, strategy, nil)`.
- `transport/internet/dialer.go`: `LookupForIP` / `lookupForIP` build `IPOption`.

## Principle Check

- Declared intent: only the selection rule changes; caches and transports stay.
- Compatibility first: an absent field reproduces the baseline order (SC-002).
- Boundaries: Core restricts eligibility; Sidecar owns subnets and upstreams.
- Real behavior: integration test with real outbound handlers and DoH fixtures.

## Design

1. Proto/JSON (FR-001, FR-007, FR-008): add `repeated string outbound_tag = 19`
   to `NameServer`; `NameServerConfig.OutboundTags []string` with
   `json:"outboundTags"`; validate non-empty unique entries; reject with
   `dns.script`; include sorted tags in the policy signature. Regenerate
   `config.pb.go` with the pinned protoc toolchain (`infra/vprotogen`).
2. Carry the tag (FR-002): add `OutboundTag string` to `dns.IPOption` (still a
   comparable value). Add `internet.LookupForIPWithTag(domain, strategy, src, tag)`
   or an equivalent parameter, used only by `handler.Dispatch`.
3. Select (FR-003, FR-004): `app/dns.Client` records its tag set. `sortClients`
   receives the tag: when any client lists it, start from those clients only and
   skip fallback assembly; otherwise drop restricted clients from domain matching
   and fallback. Keep the existing order computation for the remaining clients.
4. Logging (FR-009): append the tag to the existing decision log record.
5. Caches (FR-005): no change; each `Client` keeps its own `CacheController`.
6. Control plane (FR-010–FR-012): `Client` keeps its sanitized configured address;
   `app/dns` exposes a read-only `ControlSnapshot()` (servers in order, global
   options, cache sizes via a read-locked `CacheController.size()`). New package
   `app/dns/command` with `command.proto` (`DNSService.ListServers`, `Config`),
   generated with the repository protoc toolchain, registered in `infra/conf/api.go`
   (`dnsservice`) and `main/distro/all`. The service requires `dns.Client` and
   type-asserts the snapshot provider; otherwise it reports the system resolver.

## Verification

- Unit: config parsing/validation, policy signature, `sortClients` matrix
  (tagged/untagged, matched/unmatched, domain rules, fallback flags).
- Integration: two in-process DoH servers answering one name differently, two
  SOCKS-like outbounds with `targetStrategy: ForceIPv4`; assert destinations,
  cache isolation and fail-closed behavior.
- Regression: baseline order fixture; `go test ./...`; `./build.sh`.
- Control plane: contract test over a real gRPC listener (SC-004): default config,
  restricted servers with cached names after a lookup, and `Unimplemented` without
  the service; userinfo stripped from addresses.

## Risks

- Upstream merge conflicts in `app/dns`: keep the change localized and covered.
- A consumer config deployed to an older Core silently ignores the field; the
  config rollout deploys Core first and verifies its revision (xray-config/spec/008).

## Actual Results

- 2026-10-20 `a0831478`: selection, caches and isolation (US-001–US-003); full
  `go test ./...` passed apart from a flaky inbound port test that passed on rerun;
  `./build.sh` built Darwin and Linux. Deployed on la.lazy.verizon (xray-config/spec/008).
- 2026-10-20 control plane (US-004): `DNSService.ListServers` with the contract tests in
  tasks.md T010.
