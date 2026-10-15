# U012 Combined Independent Semantic Oracle

## Scope and Independence

Added `app/router/u012_semantic_oracle_test.go`, test-only, on candidate
`f4466f6f08192d688d5bc178955ca39968e1c568`. No runtime, shared ledger, release
pin or production configuration changes. Other agents' SS changes are separate.
This supplements spec010 U-012/V-010 and the existing
[resource evidence](../010-upstream-main-upgrade/evidence/router-u012-20261014/README.md).

`TestU012CombinedIndependentSemanticOracle` writes small real protobuf GeoSite
and GeoIP DAT files to a private temporary directory. Candidate selectors pass
through `ParseDomainRules`/`ParseIPRules`, the real DAT loader, geodata registries,
router condition construction, rule reload and `Router.PickRoute`.

The reference side separately reads those files with generic `proto.Unmarshal`.
It does not invoke the production rule parser, DAT loader, matcher, registry,
condition or route selector. A deliberately restricted interpreter uses string
equality/suffix/substring operations, standard-library regexp, explicit attribute
intersection and `netip.Prefix.Contains`. Matching lists are OR; domain and IP
conditions within a route are AND; the first matching route in configuration
order wins. Unsupported custom selector kinds fail loudly. This is an independent
semantic oracle, not agreement between two optimized matcher implementations.

## Covered Combinations

- Mixed-case input, custom full/domain/keyword patterns, DAT category selectors
  and attribute selectors; input/domain normalization does not lowercase regex
  patterns. An uppercase-only regex remains a negative control.
- Full, suffix, keyword and regex matches with overlapping rule IDs; suffix
  label-boundary rejection, regex near misses, absent domain and no-match fallback.
- GeoSite attribute subsets and multi-attribute intersection, including an
  ads-only entry that must not satisfy blue-and-ads.
- Repeated ext selectors within a route and across routes, including selectors
  differing only in category case. The flattened domain matcher is checked by
  all matching indices, not just MatchAny. Duplicate returned indices are allowed
  by its contract, but distinct indices for repeated selectors must survive.
- IPv4 and IPv6 DAT ranges, narrower custom prefixes, misses, missing IPs and
  multiple resolved addresses with only the second address matching.
- Domain/IP overlap both across routes and as a conjunction within one route.
  Real router rule IDs and first selected rule/outbound are compared separately.
- Two opposite rule orders (fallback last), each after three reload/GC rounds.
  DNS is disabled for this fixture: supplied addresses are deterministic, with
  no resolver, socket, remote endpoint or subprocess Core required.

There are 10 domains x 7 IP lists x 2 orders x 3 rounds = 420 routing comparisons
per invocation, each checking the full ordered matching rule-ID list and the
first selected route/outbound. Another 60 comparisons check flattened matcher
indices. Handwritten reference anchors prevent vacuous success.

Four additional negative controls change only candidate fixture configuration:
remove the attribute constraint, drop a duplicate-reference route, swap the first
two routes, or remove the IP conjunct. All must disagree with the unchanged
reference rule IDs. All except dropping the non-first duplicate must also change
the observed first route. These are sensitivity controls, not discovered runtime
defects or runtime mutation experiments.

## Executed Evidence

Darwin/arm64, Go 1.27.0, from candidate root:

```sh
go test -race -count=10 -timeout=2m -v ./app/router -run '^TestU012CombinedIndependentSemanticOracle$' > /tmp/core-u012-semantic-oracle-final.log 2>&1
go vet ./app/router
```

Both commands exited zero. Focused race: **3.071s, 70 test/subtest PASS events,
zero failures/skips**. This represents 4,200 routing comparisons, 600 flattened
matcher-index comparisons and 40 negative controls. No runtime defect was found.
An earlier smaller version passed race count=3 in 1.941s; the final count=10 run
supersedes it. No large socket workload or unrelated acceptance test was started.

Source SHA-256:

```text
903bc44751b5af722a3a7cefb1b3c05b14c0c05e23a3af8350cabdb3296e98e3  app/router/u012_semantic_oracle_test.go
```

DAT inputs logged by the final test:

| Input | Bytes | SHA-256 |
| --- | ---: | --- |
| Synthetic sites.dat | 186 | `0ffbcee7f7ee04253af5844c32c7277a96aee5ef8326c03dad41772d3ce59cce` |
| Synthetic ips.dat | 39 | `2a88b9f79d0aec011a09020b86f882af33d5d2b2f7e3a08df5cb77e666569511` |
| Production geosite.dat | 2288716 | `35ed26a24cafa1256bd7261414224b7bcef5c944cea7760e172b030a8b266450` |
| Production geoip.dat | 23330258 | `1cba1f0982cf62502fa079c66047c3d0c608196da5b3305671e68f60e917a482` |

Production files are hashed for provenance, not used as the synthetic reference
truth and not exhaustively semantically verified by this test. Temporary DAT
files are owned by `t.TempDir`; routers are closed at the end of each subtest.

## Boundaries

This closes the requested combined-case independent oracle gap for the tested
Darwin default MPH/router path. It does not claim mobile compact-factory or
Windows execution, exhaustive arbitrary-regex/CIDR semantics, DNS-strategy
integration, or new heap/RSS/performance acceptance. Existing separate factory,
collision, regex, collectibility and dual-platform resource evidence remains
necessary; three reload/GC rounds are not a replacement for the 300-round
resource tests. Final full scenario/AnyTLS/Sidecar gates remain main-owned.

Correction to the preceding read-only review: the SS authenticated TCP
RemoveUser boundary already exists in Sidecar's
`usersync_ss2022_integration_test.go` and awaits its final rerun; it is not an
absent implementation. Windows readv runtime acceptance remains unverified.

No commit, push or deployment was performed by this worker. All focused test
processes completed; no additional test run is pending from this task.
