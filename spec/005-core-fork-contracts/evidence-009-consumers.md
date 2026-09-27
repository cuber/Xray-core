# V-018 Real Consumer RPC Evidence

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

Date: 2026-09-27. Toolchain: `go version go1.26.1 darwin/arm64`.

## Scope and source identity

- Core server source: `/Volumes/Linux/opensource/cuber/xray-core-spec-final`,
  observed starting HEAD `c1ed7ad41d7458f5a55fb8fe81eb3a06460a5799`;
  this worker did not change any Core file.
- Sidecar HEAD: `4fad2a94320af175b55b6b2c242848478d2cd8b7`.
- Config/TUI HEAD: `5c593bb45ec37338b1140537477bcde8d6edbeb8`.
- New Sidecar test: `../xray-sidecar/route_control_contract_test.go`.
- New TUI test: `xray-tui/internal/xrayapi/routes_rpc_contract_test.go`.
- Only test code was added. No runtime implementation, production configuration,
  tracked go.mod/go.sum, Makefile pin, Core code, commit or deployment changed.
  Parent-owned AnyTLS investigation and final Core build/race results are not
  covered by this report.

This evidence closes the actual-consumer portion of 009 T010/V-018. It supersedes
the consumer-decoding gap in the earlier
[008/009 report](evidence-008-009.md), not that report's other boundaries.

At handoff the parent had amended Core to
`c1e701d3ef9e30fa7c25d95326202c66b2d8931e`. The read-only command
`git diff --name-only c1ed7ad41d7458f5a55fb8fe81eb3a06460a5799 HEAD` in Core final
listed only `proxy/anytls/clients_test.go` and `proxy/anytls/integration_test.go`.
Thus the Core runtime imported by these consumer tests is unchanged across
that parent amendment; no AnyTLS result is inferred from these tests.

## Why these are real consumer tests

Both tests initialize a real Core `router.Router` using `Router.Init` with two
ordered rules and register `routerCmd.NewRoutingServer(r, nil)` on a real TCP
gRPC server bound to `127.0.0.1:0`. Neither test implements a fake RoutingService,
constructs a ListRule response, nor calls a UI converter on a handwritten response.
Input RoutingRule protos are actual Core configuration, compiled into its runtime
rule list. Static and dynamic ListRule responses are emitted by final Core code.

Only unrelated HandlerService ListInbounds/ListOutbounds responses are stubbed
as empty so the consumers can finish their normal inventory reads. The TUI also
uses a real Core stats manager and `NewStatsServer`, not fake routing/stats data.
Optional Observatory is absent. A server interceptor counts ListRule requests
and forwards every call unchanged to the real Core service.

### Sidecar actual read path

`TestRouteControlContractRealCoreRPC` calls the unchanged `xrayDebugDump` with the
ephemeral server address. It creates its own gRPC connection, performs ListRule,
and runs the real `addRoutingRuleDetails` decoding path. Assertions inspect the
resulting debug rule maps, including exact key sets, not a test-only converter.
Missing details, lost fields and sorting the first-match list all fail the test.

The test removes PATH-based host process/version discovery using `t.Setenv`.
The unchanged debug function can still perform its normal read-only absolute
config/runtime lookups; these are not used as the source of the asserted routes.
Optional stats/observatory errors do not bypass the required successful ListRule.
No production API address or network request is used.

### TUI actual client read path

`TestRoutesRPCContractRealCore` constructs the actual `NewClient` with empty
local config metadata and calls `Client.Fetch`, which reaches `refreshInventory`,
the generated gRPC client, `routesFromResponse`, and the returned snapshot's
`cloneRoutes`. It is stronger than directly calling the converter after a manual RPC.
Inventory refresh is forced by expiring the client's timestamp, not by replacing
its router client or injecting response data. The only allowed warning is the
missing optional Observatory; inventory or other read errors fail the test.

## Assertions and lifecycle

Each fixture contains exact users and `domain:` users, two inbound tags, an
outbound tag, domain-suffix and full-domain conditions, TCP/UDP, and a rule tag.
Consumers must retain all values and array order. Sidecar indexes are 0-based;
TUI Order values are 1-based.

Each test executes this sequence against the same live Core service:

1. Static `[z-first, a-second]`, deliberately not lexical order.
2. Real AddRule RPC appends `m-third`; decode `[z-first, a-second, m-third]`.
3. Real RemoveRule RPC deletes `a-second`; decode `[z-first, m-third]`.
4. Mutate decoded user/domain/inbound/outbound values, then refetch; Core-derived
   values must remain intact. TUI additionally reads once without refresh to
   verify its cached snapshot was not aliased or corrupted.
5. Real AddRule with shouldAppend=false replaces the whole table with
   `[a-second, z-first]`; both consumers must show exactly this new order.
6. Remove both rules through RPC; decode an empty list without stale entries.

Each execution asserts exactly six Core ListRule RPCs. TUI has seven Fetch calls
because the extra cache read must issue zero ListRule calls. All clients, servers,
routers and the TUI stats manager are closed; the server goroutine is joined.
Only loopback ephemeral listeners are opened. There are no fixed service ports,
remote servers, DNS lookups for fixture domains, real proxy outbounds or deployments.

## Temporary dependency setup

Scratch directory returned by `mktemp -d /tmp/core-consumers-009.XXXXXX`:
`/tmp/core-consumers-009.FEU9QB`. The following commands all exited 0.

Sidecar cwd: `/Volumes/Linux/opensource/cuber/xray-sidecar`:

```sh
cp go.mod /tmp/core-consumers-009.FEU9QB/sidecar.mod
cp go.sum /tmp/core-consumers-009.FEU9QB/sidecar.sum
env GOWORK=off GOTOOLCHAIN=local go mod edit -modfile=/tmp/core-consumers-009.FEU9QB/sidecar.mod -replace=github.com/xtls/xray-core=/Volumes/Linux/opensource/cuber/xray-core-spec-final
```

TUI cwd: `/Volumes/Linux/opensource/cuber/xray-config/xray-tui`:

```sh
cp go.mod /tmp/core-consumers-009.FEU9QB/tui.mod
cp go.sum /tmp/core-consumers-009.FEU9QB/tui.sum
env GOWORK=off GOTOOLCHAIN=local go mod edit -modfile=/tmp/core-consumers-009.FEU9QB/tui.mod -replace=github.com/xtls/xray-core=/Volumes/Linux/opensource/cuber/xray-core-spec-final
```

In the corresponding cwd, both commands below returned exit 0 and reported
`Replace.Dir` and `Dir` as `/Volumes/Linux/opensource/cuber/xray-core-spec-final`:

```sh
env GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go list -mod=mod -modfile=/tmp/core-consumers-009.FEU9QB/sidecar.mod -m -json github.com/xtls/xray-core
env GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go list -mod=mod -modfile=/tmp/core-consumers-009.FEU9QB/tui.mod -m -json github.com/xtls/xray-core
```

Comparing each temporary modfile with its original shows only the replacement
path changed. The first focused run allowed dependency resolution in temporary
files; subsequent runs used `-mod=readonly`. GOPROXY/GOSUMDB were off and toolchain
local, so these validation commands did not fetch remote dependencies.

## Exact test commands and results

Sidecar cwd:

```sh
gofmt -w route_control_contract_test.go
make check-fmt
env GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -mod=mod -modfile=/tmp/core-consumers-009.FEU9QB/sidecar.mod -json -race -count=1 -timeout=5m . -run '^TestRouteControlContractRealCoreRPC$' > /tmp/core-consumers-009.FEU9QB/sidecar-race.json
env GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -mod=readonly -modfile=/tmp/core-consumers-009.FEU9QB/sidecar.mod -json -race -count=10 -timeout=5m . -run '^TestRouteControlContractRealCoreRPC$' > /tmp/core-consumers-009.FEU9QB/sidecar-repeat.json
```

All exit 0. Initial test PASS 0.040s, package PASS 1.764s. Repeat: ten test PASS
events, package PASS 1.849s. No failures or skips; 60 actual ListRule RPCs in the
ten-run test. Full Sidecar repository tests were not run in this scoped task.

Formatting cwd `/Volumes/Linux/opensource/cuber/xray-config`:

```sh
gofmt -w xray-tui/internal/xrayapi/routes_rpc_contract_test.go
```

TUI test cwd `/Volumes/Linux/opensource/cuber/xray-config/xray-tui`:

```sh
env GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -mod=mod -modfile=/tmp/core-consumers-009.FEU9QB/tui.mod -json -race -count=1 -timeout=5m ./internal/xrayapi -run '^TestRoutesRPCContractRealCore$' > /tmp/core-consumers-009.FEU9QB/tui-race.json
env GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -mod=readonly -modfile=/tmp/core-consumers-009.FEU9QB/tui.mod -json -race -count=10 -timeout=5m ./internal/xrayapi -run '^TestRoutesRPCContractRealCore$' > /tmp/core-consumers-009.FEU9QB/tui-repeat.json
env GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -mod=readonly -modfile=/tmp/core-consumers-009.FEU9QB/tui.mod -json -race -count=1 -timeout=5m ./internal/xrayapi > /tmp/core-consumers-009.FEU9QB/tui-package.json
```

All exit 0. Initial test PASS 0.020s, package PASS 1.550s. Repeat: ten test PASS
events, package PASS 1.489s. Full xrayapi package: nine test PASS events, package
PASS 1.485s. No failures or skips. The repeat run performs 60 actual ListRule
RPCs and ten cache-only Fetch calls.

Both new files pass `gofmt -l` with empty output. Sidecar `make check-fmt` passes.
`git diff --exit-code -- go.mod go.sum Makefile` in Sidecar and the equivalent
config command scoped to `xray-tui/go.mod xray-tui/go.sum xray-tui/Makefile` both
return 0 with no output. Core final is clean; its parent-owned fixture amendment
is recorded above and no Core file was modified by this worker.

## JSON artifact SHA-256

All files are under `/tmp/core-consumers-009.FEU9QB/`:

| File | SHA-256 |
|---|---|
| sidecar-race.json | af7ffb584faf059b5417ea490b930c2a43a8a71849372a3bc1dc516c55dfb095 |
| sidecar-repeat.json | 735e871946e98080e7ae7c9efba2b75dbd3e7969667ecbce7b9c9a9c26a3dfb0 |
| tui-race.json | 1dbc3c3757befeefe5c605829723c8848e390ed51bf8d3af373f4a54ef974ee0 |
| tui-repeat.json | 06d94956c7de7934b0a3d25771d349dee0c9197982f75f13d288315c7238404c |
| tui-package.json | c4273e6cd5169fc9ad4efbba1648eaa4b3988c08803cf1f2e310bcb80b47474c |

## Remaining boundaries

V-018's actual Sidecar/TUI route-decoding gap is now closed for the fields and
lifecycle above against final Core c1ed7ad4. This is not a production-network,
rendered terminal/browser UI, real proxy transport, full Sidecar suite, or
AnyTLS acceptance result. No pin promotion, release build, commit or deployment
was attempted. Final integration and commits remain with the parent process.
