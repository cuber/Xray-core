# DNS Servers Selected By Outbound

- ID: 012-dns-outbound-servers
- Created: 2026-10-20
- Status: Approved, 2026-10-20. The user asked to write the spec and implement it
  ("加个spec改起来吧") after confirming that DNS server choice cannot name an outbound.
- Baseline: `develop` `1a05cc1`; `app/dns/dns.go` `LookupIP(domain, option)` and
  `sortClients(domain string)`; `app/proxyman/outbound/handler.go` `targetStrategy`.
- Consumers: [xray-sidecar/spec/009](../../../xray-sidecar/spec/009-egress-dns/spec.md)
  (per-exit DoH views) and
  [xray-config/spec/008](../../../xray-config/spec/008-lazy-egress-dns/spec.md) (Lazy rollout).
- Implementation status: not implemented.

## Problem And Goal

A LazyCat landing node reaches several residential exits through SOCKS outbounds
on one mother host. Outbounds with `targetStrategy` resolve the target in Core
before SOCKS CONNECT, so every exit's answer comes from the same DNS module.
`LookupIP` carries only the domain and IP families, and `sortClients` orders
servers by domain rules and configured order. No configuration can make a server
serve one outbound, so the answer cached for one exit is reused by another and an
EDNS Client Subnet (ECS) can only describe one of them.

Goal: a server may be restricted to named outbounds. A target lookup made by such
an outbound uses only its servers and their caches. Nothing changes for configs
that do not use the field.

## User Scenarios

### US-001 — Exit-specific resolution (P1)

Independent acceptance: a two-outbound fixture with two DNS servers that answer
the same name differently.

1. Given servers A (`outboundTags: ["exit-a"]`) and B (`["exit-b"]`) and outbounds
   `exit-a`/`exit-b` with `targetStrategy`, When each outbound dials the same
   domain, Then `exit-a` connects to A's answer and `exit-b` to B's answer.
2. Given A answered for `exit-a`, When `exit-b` resolves the same name before A's
   TTL expires, Then it receives B's answer, never A's cached record.

### US-002 — Fail closed per outbound (P1)

1. Given every server listing `exit-a` fails, When `exit-a` resolves a target with
   `ForceIPv4`, Then the lookup fails and the connection is not made; unrestricted
   servers are not consulted.
2. Given the same failure, When another outbound or a node-local lookup resolves,
   Then it uses unrestricted servers as before.

### US-003 — Unchanged default behavior (P1)

1. Given a config without `outboundTags`, When any lookup runs, Then server order,
   fallback, caches and results equal the baseline.
2. Given a restricted server exists, When an untagged lookup runs (routing
   `domainStrategy`, `sockopt.domainStrategy`, freedom, observatory, DNS outbound),
   Then the restricted server is never used, neither as a domain match nor as fallback.

## Functional Requirements

- FR-001: `dns.servers[]` objects accept `outboundTags`, a non-empty list of
  outbound tags (proto `NameServer.outbound_tag`, repeated string). Absent or empty
  means unrestricted. Entries must be non-empty and unique within one server; the
  same tag may appear on several servers, which then form its ordered list.
- FR-002: `targetStrategy` resolution in the outbound handler passes the handler's
  own tag with the lookup. No other lookup path carries an outbound tag.
- FR-003: For a lookup with tag T that at least one server lists: only servers
  listing T are eligible, in configured order, with their domain rules still
  prioritizing among them. Fallback to any other server never happens, regardless
  of `disableFallback`, `disableFallbackIfMatch` or `skipFallback`.
- FR-004: For an untagged lookup, or a tag no server lists: restricted servers are
  excluded from domain matching and fallback; selection among the rest is unchanged.
- FR-005: Each server keeps its existing per-instance cache. A record obtained by
  one server is never returned by another server's cache.
- FR-006: Static hosts and FakeDNS keep their current precedence for all lookups.
- FR-007: Reject `outboundTags` when `dns.script` is set; script selection is out of scope.
- FR-008: The parallel-query policy signature includes `outboundTags`, so servers
  that differ only by tags are not grouped together.
- FR-009: The DNS decision log at info level includes the outbound tag when present.

## Non-Goals

- Dynamic or per-query ECS; Sidecar owns per-exit subnets.
- Tagging `sockopt.domainStrategy`, freedom `domainStrategy`, routing
  `domainStrategy`, observatory or Lua lookups.
- Validating that a tag names an existing outbound (unknown tags never match).
- New transports such as DoT.

## Edge Cases

- Tag matched by several servers: try them in order; a total failure is final (FR-003).
- `queryStrategy`, `expectedIPs`, `timeoutMs` per server still apply.
- `UseIPv4` (non-force) targetStrategy keeps its existing behavior on failure:
  the domain is passed on; isolation only governs which servers are asked.
- The same restricted server used by two outbounds shares one cache by design.

## Success Criteria

- SC-001: Unit and integration tests cover FR-001–FR-009, including two fake DoH
  servers that answer the same name differently and a cache-isolation case.
- SC-002: The baseline ordering test proves configs without `outboundTags` select
  servers identically to `1a05cc1`.
- SC-003: `go test ./...` passes, and `./build.sh` produces both binaries from a
  clean committed tree.

## Clarifications And Decisions

- 2026-10-20: Only `targetStrategy` carries the tag. Mother-host and freedom
  dials originate from the node itself, so their answers should not use an exit's view.
- 2026-10-20: Strict isolation without fallback; the user wants an exit to fail when
  its resolver path fails ("sidecar 挂了就让节点失败").
- 2026-10-20: Per-server caches keep working and are isolated by server. The Lazy
  consumer disables them for its Sidecar-backed servers (`disableCache`), because
  Sidecar caches by exit subnet and handles cold start (xray-sidecar/spec/009).

## Authorization And Revisions

- 2026-10-20: Approved for implementation. Deployment scope and order belong to
  xray-config/spec/008.
