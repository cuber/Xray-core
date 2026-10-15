# Protocol Chain Darwin Final Ledger

## Source And Freeze

All execution here is native Darwin/arm64 with cached Go 1.27.0, offline module
resolution and a coordinator-assigned serial socket window. No canonical
develop change, commit, push, deployment or service restart was performed by
this agent.

The dedicated 292-event matrix and all three whole-default runs used clean
`76b17aa254ee85e8c2a32b42331cc50d6dae173e`. This checkpoint already contains
the test-only original-listener oracle. Existing runtime, shared scenario
harness, dependencies and the four original `protocol_chain_*_test.go` files
were unchanged throughout this execution.

Only after every default run finished was
[`protocol_chain_domain_test.go`](../../testing/scenarios/protocol_chain_domain_test.go)
added, SHA256
`e0fdf544c02abddfa7c4ab2d57f467080a4c3bcf80c8cb25a9b7e0b0261c3431`.
Its independent runs used Core76 plus that additive file, not a new committed
HEAD. Compile-before/after and execution-after hashes match. Existing test and
runtime hashes still match Core76. There is no claim of a post-addition whole
default rerun. The coordinator owns the later local checkpoint and official
dual-platform build.

## Completed Execution

Counts are Go test/subtest events, including parents, not protocol combinations.
Every command below exited zero; all stderr files are empty.

| Group | Ordinary PASS / SKIP | Race PASS / SKIP | Checkptr2 PASS / SKIP | FAIL |
|---|---:|---:|---:|---:|
| Dedicated original matrix | 292 / 0 | 292 / 0 | 292 / 0 | 0 |
| Whole module defaults | 3481 / 71 | 3482 / 70 | 3481 / 71 | 0 |
| Independent domain group | 86 / 0 | 86 / 0 | 86 / 0 | 0 |

Each whole-default run completes all 179 listed packages: 102 package PASS and
77 package SKIP (`no test files`). Ordinary/checkptr test PASS name sets are
identical. Race additionally executes
`TestSudokuChildRaceZeroExitRejected`, the deliberately race-instrumented child
negative control, which ordinary/checkptr skip. Matrix and domain PASS name
sets respectively match across all three modes.

The original matrix is 15 top-level tests plus 277 leaf/subtest events. Its
healthy graph matrix remains all 16 ordered pairs and 64 ordered triples.
Dedicated package elapsed seconds are 281.566 / 299.645 / 289.036. All identity,
UDP boundary, cancellation, active-close, 32 outage, resource and listener-owner
cases executed, without a strict resource skip. The diagnostic 50-round SS2
loop was deliberately excluded.

The scenario child binaries in whole defaults were captured before cleanup.
Each reports Core76 and `vcs.modified=false`; race reports `-race=true`, and
checkptr reports `-gcflags=all=-d=checkptr=2`. Checkptr was supplied through both
the parent command and inherited `GOFLAGS`, so child coverage is verified rather
than inferred. Scenario package times: 281.417 / 1055.154 / 275.987 seconds.
Race retains the previously documented 45-minute aggregate scenario budget,
selected before execution, not increased after a failure. Native business and
ownership budgets were not changed.

Observed long race MASQUE cases were compared with the retained prior baseline,
not merely left waiting for that aggregate timeout:

| Test Suffix | Current Seconds | Prior Seconds |
|---|---:|---:|
| `Masque` | 175.73 | 185.69 |
| `MasqueHTTP2` | 150.18 | 157.44 |
| `MasqueServer` | 183.51 | 191.07 |
| `MasqueServerHTTP2` | 173.40 | 177.60 |

After the additive file, strict whole-tree gofmt/gofumpt and
`go vet -mod=readonly -p=2 ./...` both exited zero. No Go source changed after
those checks. No test child remained after the final network run. Domain-window
TCP TIME_WAIT snapshots were 85 before and 104 after; no host networking settings
or production process was changed.

## Listener And Resource Oracles

The exact oracle is the originally captured `syscall.RawConn` identity, not an
FD integer and not a port-rebind probe. Listener controllers are registered once
at test initialization. Every expected physical listener must have exactly one
nonnil, live capture before Start returns. After its owner's bounded five-second
Close, `RawConn.Control` must return an error matching `net.ErrClosed`, with its
callback uncalled; an arbitrary error does not pass.

Physical expectations are SS2 TCP+UDP, VLESS/AnyTLS raw TCP, Hy2 UDP, and actual
doko TCP/UDP listeners. Never-owned protocol/port pairs are not retirement
evidence. Zero/missing capture fails. Deliberately leaving the original TCP or
UDP listener live must fail; closing it and binding a foreign socket at the same
port must pass the original-identity check while leaving the foreign socket live.
These controls and real captures ran in every dedicated mode.

Accepted client-Core doko TCP must produce actual `io.EOF` within five seconds,
before caller safety cancellation. Caller-owned UDP requires explicit caller
cancel/Close, not invented remote EOF. Every service Core is then closed before
the backend's natural worker join is judged; success never force-closes accepted
backend sockets. Client-only instantaneous remote QUIC/UDP teardown is not
claimed. Core.Close timing alone is not an all-Core-worker gauge.

Resource rounds use Darwin RLIMIT_NOFILE/F_GETFD enumeration, not `/dev/fd`
directory enumeration. Warmed FD/goroutine baseline -> final was 4/8 -> 4/13
(ordinary), 4/7 -> 4/13 (race), and 4/7 -> 4/13 (checkptr). Existing allowances
remain +4 FDs/+8 goroutines within ten seconds. This is bounded process growth,
not exact physical-session counts or an RSS/heap acceptance gate.

## Domain Preservation

`TestProtocolChainDomainPreservation` repeats all 80 graphs with literal protocol
endpoints, two authenticated users, TCP/UDP, two business domains and sizes
31/1200: 16 exact requests per graph, 1280 per mode. Distinct destination payload
markers reject swapped domain-to-backend mappings. The business target port is
443, different from every owned backend port. Final-only full-domain/network
`AsIs` rules select four freedom redirects to the corresponding owned loopback
TCP/UDP endpoint; blackhole is the first/default outbound. Client and intermediate
protocol configurations retain their original boundaries and AnyTLS native pool.

Every hop's authenticated user bytes increase in both directions, and final-hop
uplink/downlink is exactly 4940 bytes per user. Other-user attribution follows
the original matrix's explicit outer-pool-control allowance. Exact backend
receipts, original RawConn closure and owned worker join remain mandatory.

`TestProtocolChainDomainRouteControls` uses four separate SS2/VLESS/final-protocol
triples. Both users first pass TCP/UDP domain controls with exact 66-byte
per-direction exit deltas. Eight wrong-domain/literal-IP attempts per triple
must fail, with denied backend0 total receipts zero and positive backend1
receipts exactly once. Denied UDP may finish through its two-second caller
safety deadline; zero business receipts, not an asserted UDP close notification,
is the negative oracle. This is deliberately different from the established
outage test, which rejects caller-timeout-as-success.

There are 80+4 leaves and two parents, hence 86 events. Elapsed seconds are
41.129 / 49.085 / 41.146. These are domain preservation and final-hop route
selection tests, **not DNS resolution, DNS query attribution, or instrumented
zero-DNS-query tests**. No resolver replacement, static DNS hosts, global DNS
mutation or runtime patch was added. They do not add three-hop Mux/fault coverage.

## Explicit Default Skips

| Package/Reason | Ordinary + Checkptr | Race |
|---|---:|---:|
| DNS public-network opt-ins | 12 | 12 |
| AnyTLS external clients/diagnostics/soaks and address capability | 33 | 33 |
| AnyTLS engine real-time idle opt-in | 1 | 1 |
| SS2 external fragmentation/soak opt-ins | 2 | 2 |
| Scenarios network opt-ins, including diagnostic | 13 | 13 |
| Sudoku race-only child negative control | 1 | 0 |
| TLS public ECH / Linux system-CA gate | 2 | 2 |
| Xdrive credentialed live gates | 7 | 7 |

AnyTLS source-alias capability cannot bind `127.0.0.2` on this Darwin host; no
host alias was added. The native 30-second idle acceptance requires its separate
`ANYTLS_STRICT` opt-in. None of these skips is relabeled PASS. Dedicated network
tests use both `XRAY_PROTOCOL_CHAIN_NETWORK=1` and
`XRAY_PROTOCOL_CHAIN_STRICT=1`; missing opt-in fails strict acceptance.

## Retained Original Reds

The original Core44 full ordinary run failed at
`TestProtocolChainInvalidIdentity/ss2/position2`: UDP rebind at `127.0.0.1:52242`,
leaf 5.01s, package 254.840s, failfast. That port belonged to the first SS2 hop,
not a never-owned VLESS UDP check. Its historical socket owner is **unknown**.
General discovery of never-owned checks is an independent oracle defect, not
proof of this individual failure's owner.

A later bounded count50 diagnostic stopped at Fatalf on its fourth iteration;
only three iterations passed. lsof captured test PID46332 IPv4
`127.0.0.1:55590` coexisting with existing process PID99227 IPv6 `*:55590`.
The failing callsite was the UDP-rebind assertion, but its exact port argument
was not decoded. This is **55590 coexistence corroboration only**, not a proven
52242 root cause or a runtime-fix claim. The paused diagnostic child was ended;
that is not graceful-teardown acceptance. PID99227 was untouched.

Full original-log SHA256 anchors, diagnostic-log SHA256 and lsof SHA256 are in
the manifest's `retained_historical_raw_evidence`. In particular the original
JSONL SHA256 remains
`ba306e59bf0b52a51ab059dd5f6083b33778c52874ebd543b279d4161ce57723`.
No timeout relaxation, business retry or runtime change was used to turn these
old rebind assertions green. Core76 instead tests original listener ownership.

## Commands And Persistent Evidence

The [manifest](evidence/protocol-20261016/manifest.json) contains exact executed
shell argv, exit codes, timestamps, source hashes, compile commands, test ELF
hashes, original full JSONL/stderr SHA256 and filtered gzip SHA256. Read a precise
executed command without reconstructing environment flags:

```sh
jq -r '.runs[] | select(.id == "matrix-race") | .execution.argv[-1]' spec/010-upstream-main-upgrade/evidence/protocol-20261016/manifest.json
```

Dedicated original test selection, count=1, parallel=1, timeout=20m:

```text
^TestProtocolChain(ConfigMatrix|UDPEncodingBoundary|TwoHop|ThreeHop|VLESSMux|TLSVerification|UDPBoundary|InvalidIdentity|Cancellation|ActiveClose|Outage|ResourceRounds|Listener(MissingCapture|OwnershipOracle|Capture))$
```

Independent domain selection, count=1, parallel=1, timeout=10m:

```text
^TestProtocolChainDomain(Preservation|RouteControls)$
```

Both use precompiled in-process fixtures through `go tool test2json`, strict
network opt-in and failfast, with no external-client dependency. Whole defaults
use `go test -mod=readonly -count=1 -p=1 -parallel=1 -failfast -json ./...`,
15m per-package ordinary/checkptr or the predeclared 45m race budget. All module
operations disable proxy/sumdb fetching and workspace overrides. Specialized
network/AnyTLS environment opt-ins are explicitly unset for defaults.

The nine persistent gzip files contain only structured `start/run/pass/skip/fail`
events and fields `Time/Action/Package/Test/Elapsed`, **not complete original
logs or stdout**. All Output/OutputType and output actions are removed. See
[archive format and limitations](evidence/protocol-20261016/README.md).
Temporary raw-log hashes are durable anchors, not durable raw-log copies.

This ledger does not claim current-source native Linux/amd64, Linux race,
Windows/checkptr, WAN, external-client interoperability, or public-route/RSS
acceptance. Those are separate coordinator/owning-agent gates. Historical Linux
execution and earlier failures remain revision-bound in their existing ledgers.
