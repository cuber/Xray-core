# Router and U012 Geodata Evidence

Date: 2026-10-14. Isolated candidate `xray-core-rebase-20261014`, based on
`0888beb38a9ba45440557175e71ce21cf8ba3b13`. This evidence covers scoped package
tests and resource measurements, not a full release or deployment acceptance.
Darwin results below were archived from the earlier worker runs; Linux results
were executed after source freeze using the same cached Go 1.27.0 toolchain.
This archival/Linux increment changed only this evidence directory. No source,
test, runtime, DAT, canonical checkout, release pin or readv changes were made.

## Results

Counts include test and subtest pass events, not only top-level tests.

| Run | Passes | Failures / skipped tests | Evidence |
| --- | ---: | --- | --- |
| Darwin full router/conf/geodata race | 658 | 0 / 0 | [JSON](darwin-full-race.json.gz) |
| Darwin U012 race, ten repeats | 90 | 0 / 0 | [JSON](darwin-u012-repeat.json.gz) |
| Darwin routing contracts, ten repeats | Both packages PASS | No failures reported | [log](core-spec005-repeat-final.log) |
| Linux geodata + strmatcher full race | 329 | 0 / 0 | [JSON](linux-full-race.json.gz) |
| Linux U012 race, ten repeats | 90 | 0 / 0 | [JSON](linux-u012-repeat.json.gz) |

Darwin full suite includes TestChinaSites with no exclusion. The three
features/routing packages report no test files, not skipped test cases.
Darwin full package times: router 1.399s, command 2.844s, conf 1.737s,
geodata 4.207s, strmatcher 4.802s. Darwin U012 repeats took 11.425s.
Linux full package times: geodata 6.508s, strmatcher 4.015s;
Linux U012 repeats took 40.868s. Linux geodata vet exited 0.
Scoped Darwin vet, formatting and diff checks passed in the original runs.

## Router / U010

The spec005 port preserves upstream atomic rule snapshots and script support,
adds independently cloned detailed ListRule metadata and caller-input ownership,
user-domain matching, and weightedLeastPing. Weighted JSON loading preserves the
OB agent's HealthCheckSettings/probeRetry changes. Owned protobuf regeneration
was checked byte-for-byte idempotent in the original implementation run.

The real TCP gRPC tests cover append/remove/replace, duplicate rejection,
concurrent mutation/listing, nested-response mutation isolation, and legacy wire
fields. Weighted tests cover exact 600-selection distributions, thresholds,
fallback/recovery and finite-positive literal first-match weights.

[Reload tests](../../../../app/router/reload_contract_test.go) cover failed
append/replace cleanup, published webhook ownership, coherent concurrent
selection/metadata, and validation before balancer dependency registration.
[Negative control](core-spec005-u010-negative.log) disabled only the rollback
close call and failed all eight append/replace error scenarios. The restored
implementation passes. This was a candidate negative control, not a full old
binary run. Webhook construction does not start a worker until Fire: the test
proves unpublished notifier closure, not a measured pre-existing goroutine leak.

## U012 Resource Ownership

[Collectibility tests](../../../../common/geodata/collectibility_test.go) and
[resource tests](../../../../common/geodata/resource_test.go) assert:

- 300 retired domain dynamic matchers and states become unreachable per
  scenario, while the active matcher remains usable across reload and GC.
- 299 replacement domain factory caches become collectible; the original
  factory is deliberately retained to test weak-value ownership. Both initial
  factory variants are exercised. Registry Reload then uses the platform's
  normal factory, so this is not 300 compact-only reloads on desktop/Linux.
- 300 retired IP dynamic matchers, states, IP sets and factory caches are
  collectible; exactly one active registry entry remains.
- Weak-cache Store/Load/Range with GC preserves a live replacement under the
  same key; concurrent registry reload/GC preserves active matching.
- Actual production-CN compiled domain objects become collectible even while
  their factory/cache remains alive. Bounded GC retries, not a single-GC timing
  assumption, are used for all weak-pointer collection assertions.

Upstream `common/utils/weak_cache.go` uses weak values and AddCleanup with a
weak-pointer identity guard before deleting a key. Registries also hold dynamic
matchers weakly. No cache/matcher algorithm fix was needed. The historical
spec007 IPv6 test was ported to an owned /128 DAT fixture rather than asserting
the changing public country assignment of Google DNS.

## Resource Measurements

Non-race standalone test binaries, production GeoSite CN rules, with factories
explicitly selected. Heap values are process bytes; peaks are sampled every
1 ms during construction, not exact allocator maxima. Retained/released values
follow GC. Both compiled matcher objects were observed collectible on each OS.

| OS / factory | Baseline heap | Sampled build peak | Held post-GC | Released post-GC | Lookup ns/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| Darwin MPH | 386968 | 1285672 | 533944 | 396496 | 15.69 |
| Darwin compact | 400344 | 912576 | 535520 | 397968 | 16.88 |
| Linux MPH | 376256 | 1449424 | 529592 | 392240 | 16.02 |
| Linux compact | 396088 | 1436648 | 533072 | 395648 | 17.71 |

All lookup cases reported 0 B/op and 0 allocs/op. Darwin standalone process
maximum RSS was 42,778,624 bytes (`/usr/bin/time -l`). Linux child maximum RSS
was 36,852 KiB (37,736,448 bytes), measured by Python RUSAGE_CHILDREN for the
standalone test/benchmark child only, excluding Go compilation. The Linux
measurement exited 0 in 2.740s.

Raw observations: [Darwin](core-u012-resource-darwin.log),
[Linux](linux-resource-bench.log). Each RSS measurement combines both factory
cases and benchmarks; it is not a per-factory RSS comparison. CPU resources
were not reserved from other work. These are candidate observations, not
historical savings, a throughput guarantee, or evidence of lower production RSS.
The memory benchmark constructs GeoSite CN, not a whole production route table
or production GeoIP country table; IP collectibility uses controlled fixtures.

## Inputs and Integrity

DAT hashes were checked on the host and inside Linux, matching approved Config:

```text
geosite.dat  2288716 bytes  35ed26a24cafa1256bd7261414224b7bcef5c944cea7760e172b030a8b266450
geoip.dat   23330258 bytes  1cba1f0982cf62502fa079c66047c3d0c608196da5b3305671e68f60e917a482
```

`SOURCE_SHA256SUMS` records geodata/router sources and DAT inputs.
`FREEZE_SHA256SUMS` records the files hashed before Linux execution; rechecking
all entries after execution succeeded. `SHA256SUMS` covers archived evidence.
Verify each manifest from candidate root with `shasum -a 256 -c <manifest>`.
Complete JSON events are gzip-compressed, not replaced by summary-only output.

## Reproduction

Original Darwin commands from candidate root:

```sh
go test -json -race -count=1 -timeout=5m ./app/router/... ./features/routing/... ./infra/conf ./common/geodata/...
go test -json -race -count=10 -timeout=5m ./common/geodata -run '^TestU012'
go test -race -count=10 -timeout=5m ./app/router ./app/router/command -run '^Test(Contract|Reload|WeightedLeastPing|ServiceListRule)'
go test -c -o /tmp/core-geodata-u012.test ./common/geodata
# Run from common/geodata so the existing relative resources fixture resolves:
/usr/bin/time -l /tmp/core-geodata-u012.test -test.run '^TestU012ProductionDATResources$' -test.v -test.bench '^BenchmarkU012ProductionLookup$' -test.benchmem -test.benchtime=1s
```

Linux used a dedicated container, leaving the existing SS soak untouched:

```sh
docker run --rm -d --name xray-geodata-u012-20261014 --read-only --network none \
  --mount type=bind,source=/Volumes/Linux/opensource/cuber/xray-core-rebase-20261014,target=/src,readonly \
  --mount type=volume,source=xray-upgrade-go-mod,target=/go/pkg/mod,readonly \
  --mount type=volume,source=xray-upgrade-go-build,target=/root/.cache/go-build \
  --tmpfs /tmp:exec,size=2g -w /src -e GOTOOLCHAIN=go1.27.0 \
  -e GOPROXY=off -e GOSUMDB=off golang:1.26.1 sleep 1800
```

The launcher rejected toolchain checksum verification with GOSUMDB disabled.
No download or permission relaxation was attempted. Subsequent commands invoked
the already cached toolchain explicitly with GOTOOLCHAIN=local; `go env`
confirmed Go 1.27.0, linux/arm64. This is not a Go 1.26.1 test result despite
the bootstrap image tag. [Image identity](linux-image.log) and
[mount/environment inspection](linux-container.log) preserve setup evidence.

```sh
GO=/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.0.linux-arm64/bin/go
docker exec -e GOTOOLCHAIN=local xray-geodata-u012-20261014 "$GO" test -json -race -count=1 -timeout=5m ./common/geodata/...
docker exec -e GOTOOLCHAIN=local xray-geodata-u012-20261014 "$GO" test -json -race -count=10 -timeout=5m ./common/geodata -run '^TestU012'
docker exec -e GOTOOLCHAIN=local xray-geodata-u012-20261014 "$GO" vet ./common/geodata/...
docker exec -e GOTOOLCHAIN=local xray-geodata-u012-20261014 "$GO" test -c -o /tmp/core-geodata-u012.test ./common/geodata
docker exec -w /src/common/geodata xray-geodata-u012-20261014 python3 -c 'import subprocess,resource,time,sys; start=time.monotonic(); p=subprocess.run(["/tmp/core-geodata-u012.test","-test.run=^TestU012ProductionDATResources$","-test.v","-test.bench=^BenchmarkU012ProductionLookup$","-test.benchmem","-test.benchtime=1s"]); usage=resource.getrusage(resource.RUSAGE_CHILDREN); print("maximum_resident_set_kib=%d elapsed_seconds=%.3f exit_code=%d"%(usage.ru_maxrss,time.monotonic()-start,p.returncode),flush=True); sys.exit(p.returncode)'
docker rm -f xray-geodata-u012-20261014
```

All actual test/vet/build/measurement commands above exited 0. Source, root
filesystem and module cache were read-only; only /tmp and build-cache volume
were writable. Networking remained disabled. The dedicated container was
removed after verification. Linux coverage is arm64, not Linux/amd64 deployment
binary acceptance. Full-tree, external-consumer and official dual-build gates
remain owned by main; this document does not mark those complete.
