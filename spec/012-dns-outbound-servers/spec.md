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
- Implementation status: selection (US-001–US-003) implemented on `develop`
  (`a0831478`, 2026-10-20); control plane (US-004) implemented 2026-10-20. Rollout is
  tracked in xray-config/spec/008.

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

### US-004 — Control-plane visibility (P1)

Independent acceptance: a started instance with `DNSService` enabled and a
gRPC client on its API listener.

1. Given restricted and unrestricted servers, When a client calls
   `DNSService.ListServers`, Then it receives every loaded server in configured
   order with its address, DNS tag, `outboundTags`, cache mode and current Core
   cache entry count, plus the global DNS options.
2. Given a Core without the service, or `DNSService` absent from `api.services`,
   Then the call fails with gRPC `Unimplemented`, and consumers show the data as
   unavailable instead of reading configuration files.

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
- FR-009: The existing DNS decision log (debug level) includes the outbound tag when present.
- FR-010: Read-only `xray.app.dns.command.DNSService/ListServers`, enabled by the
  `api.services` name `DNSService`. Per server, in configured order: index, Core
  server name, configured address, DNS tag, `outboundTags` (empty = unrestricted),
  effective query strategy, timeout, `skipFallback`, `finalQuery`, domain-rule count,
  whether expected/unexpected IP rules exist, `disableCache`, `serveStale` and the
  current number of cached names. Global: query strategy, `disableFallback`,
  `disableFallbackIfMatch`, `enableParallelQuery`, static-host count, script presence.
- FR-011: Values come from the loaded `app/dns` instance. When the DNS feature is
  not `app/dns` (no `dns` section), the response says the system resolver is in use
  and lists no servers.
- FR-012: Addresses carry no URL userinfo. The response size is bounded by the
  configured servers; reading cache sizes takes each cache's read lock only.

## Control Plane

Per the Core control-plane parity rules ([README](../README.md#control-plane-parity)):
selection and bindings are observable through `DNSService.ListServers` (FR-010–FR-012).
Consumers: xray-sidecar/spec/009 adds it to `/xray-debug` beside the resolver's own
state; xray-config/spec/008 shows it in a Portal DNS tab and enables `DNSService` on
nodes with bound servers. Per-query decisions stay in the debug log (FR-009); no
mutating RPC is added.

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
- SC-004: A contract test calls `ListServers` over a real gRPC listener of a started
  instance for a default configuration and for restricted servers with cached
  entries, and checks `Unimplemented` when the service is not enabled.

## Clarifications And Decisions

- 2026-10-20: Only `targetStrategy` carries the tag. Mother-host and freedom
  dials originate from the node itself, so their answers should not use an exit's view.
- 2026-10-20: Strict isolation without fallback; the user wants an exit to fail when
  its resolver path fails ("sidecar 挂了就让节点失败").
- 2026-10-20: Per-server caches keep working and are isolated by server. The Lazy
  consumer keeps them on for its Sidecar-backed servers: Sidecar controls what Core
  caches through the answers' TTLs (zero before an exit IP is known, capped
  afterwards) and reports its own failures as HTTP errors, which Core does not cache
  (xray-sidecar/spec/009). Core caches a SERVFAIL or empty answer for its fixed
  300 s default, so a failure must not reach it as a DNS message. This revises the
  earlier decision to disable these caches (user: "让xray-core不要缓存…等拿到了
  egress ip 的再返回正常的 ttl…这样就能复用 xray-core 的缓存了").

- 2026-10-20: The control plane joins this spec rather than a new one (user: "用B方案，
  spec应该合并到上个dns改造的里面"): a Core RPC reports loaded state, Sidecar does not
  parse configuration files for it.

## Authorization And Revisions

- 2026-10-20: Approved for implementation. Deployment scope and order belong to
  xray-config/spec/008.
- 2026-10-20: US-004 (control plane) approved and added under the parity rules.
