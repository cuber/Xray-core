# V015 Isolated Linux Execution

## Environment And Isolation

This is **Linux/arm64 execution in the local OrbStack VM**, not native amd64,
not bare-metal ARM evidence and not a production/Akile test. Engine architecture
is aarch64; `uname -srmo` reports
`Linux 7.0.11-orbstack-00360-gc9bc4d96ac70 aarch64 GNU/Linux`.

The cached ARM64 image is
`sha256:2abfa7348a8006cb2d2374824e6e7de7ae12862edd87560ebb55ca2cbae777a8`
(`golang:1.26.1`), but **its compiler is not used**. All runs execute static
CGO_ENABLED=0 Linux/arm64 test ELF files built locally with Go 1.27.0 from this
candidate. ELF/source revisions are stated per gate. The initial Alpine attempt
failed before test execution because the cached Alpine image was amd64; its
JSON/stderr are retained, not counted as a test run or platform PASS.

The r10 normal/checkptr ELF files were compiled **before** the main agent's
subsequently announced Mux factory owner-context repair in
`common/mux/client.go`, `app/proxyman/outbound/handler.go` and its new owner tests.
These Linux results do not validate that later repair or a combined post-repair
candidate. The r10 freeze manifest scopes only this agent's three scenario files,
`system_dialer.go` and its two loopback regression files. Main-agent owner-fix
files are outside that manifest and outside this agent's write scope. A combined
source checkpoint/build and subsequent native acceptance belong to the main agent.

Every test container uses `--network=none`, no published/mapped ports, no bridge,
no host network, no privilege, `--cap-drop=ALL`, no-new-privileges, read-only root,
one read-only test-ELF bind mount and a 256MiB `/tmp` tmpfs. The container has a
256 PID limit, 2GiB memory and 2 CPUs. Only loopback traffic in that namespace is
generated. No source/dependency download, public DNS, Internet request, remote
deployment or installed service edit is performed. `--pull=never` forbids an
implicit image fetch. Containers are removed on exit. Mac socket tests remain
paused while Sidecar owns its window.

## Executed Ordinary Gates

All successful groups below exit zero with zero skip/fail events. One Go parent
PASS is included in each event count; it is not an extra protocol combination.
Logs have prefix `/tmp/core-protocol-chain-v015-linux-arm64-` and JSON/stderr
suffixes. The runtime repair is unchanged from the r6 freeze; r7/r8/r9 adjust only
test boundary/ownership assumptions and diagnostics.

| Group | Source/ELF | Leaf Cases | Pass Events | Seconds | Log Suffix |
|---|---|---:|---:|---:|---|
| TwoHop | r6 | 16 | 17 | 1.048 | `two-hop-normal-r6b-20261015.jsonl` |
| ThreeHop | r6 | 64 | 65 | 3.531 | `three-hop-normal-r6b-20261015.jsonl` |
| UDPBoundary | r7 | 4 | 5 | 10.439 | `udp-boundary-normal-r7-20261015.jsonl` |
| InvalidIdentity | r7 | 12 | 13 | 31.908 | `invalid-identity-normal-r7-20261015.jsonl` |
| Cancellation | r7 | 16 | 17 | 0.678 | `cancellation-normal-r7-20261015.jsonl` |
| ResourceRounds | r7 | 12 + warmup | 13 | 2.164 | `resource-normal-r7-20261015.jsonl` |
| Outage | r7 | 32 | 33 | 241.134 | `outage-normal-r7-20261015.jsonl` |
| TLS + VLESSMux | r8 | 9 + 4 | 15 | 4.635 | `tls-mux-normal-r8-20261015.jsonl` |
| ActiveClose, correct ownership | r9 | 16 | 17 | 0.495 | `active-close-normal-r9-20261015.jsonl` |

Resource baseline FD/goroutines was 6/6, final 6/12, within the predefined +4/+8
supporting bound. This is not an assertion that all Core/global goroutines joined.
The r7 log's phrase "all Core/backend workers" was too broad; r8+ corrects that
message without changing its assertions. Actual independent assertions are
bounded Core.Close, joined owned backend workers and retired listening ports.

The r9 full strict-checkptr=2 run completed: **281 test/subtest PASS events,
zero fail/skip, 285.568s**. Its 12 top-level tests and 269 leaves include every
ordinary gate above plus all 80 config and four pure encoding cases. The log is
`full-checkptr2-r9-20261015.jsonl` under the same prefix.

Final r10 tightens only ActiveClose's accepted TCP read oracle from an arbitrary
non-nil error to `errors.Is(err, io.EOF)`, still rejecting safety cancellation.
The service-owner shutdown sequence, runtime and all time budgets are unchanged.
The final-source ordinary run completed with **281 test/subtest PASS events,
zero fail/skip, 289.562s**. ActiveClose passed all 16 pairs with the precise EOF
oracle (0.250s parent). ResourceRounds warmed FD/goroutines 6/8 and finished
6/14. Its log is `full-normal-r10-20261015.jsonl`. The same-source strict-checkptr
run also completed: **281 unique test/subtest PASS events, zero fail/skip,
287.645s**, including all 16 precise-EOF ActiveClose cases (0.250s parent).
ResourceRounds warmed FD/goroutines 6/7 and finished 6/15, within the unchanged
+4/+8 bounds. Its log is `full-checkptr2-r10-20261015.jsonl`. Both final commands
exited zero and both stderr files are empty. Linux race
instrumentation and native amd64 execution are not established by any ordinary
or checkptr result here. Darwin lifecycle normal/race and final-source
whole-matrix verification remain pending.

## Retained Failures And Corrected Assumptions

- `two-hop-normal-r6-20261015`: infrastructure launch failure, cached Alpine
  amd64 image versus requested ARM64. No test case executed. The cached ARM64
  image was then selected without pulling anything.
- `udp-boundary-normal-r6b-20261015`: SS2 maximum passed; VLESS8190 positive failed
  at five seconds. Pure native length framing supports 8190, but the configured
  default cone path selects XUDP for the fixture's non-53/non-443 destination.
  This is not a universal VLESS wire-protocol maximum or a Core.Dial net.Conn
  fragmentation limit. The fixture submits one MultiBuffer packet directly;
  neither net.Conn.Write splitting nor a changed pipe buffer explains the drop.
- `active-close-normal-r7-20261015`: the test incorrectly expected instance Close
  to close a caller-owned `core.Dial` connection. It failed at SS2/SS2 and was
  replaced with real client-Core doko inbounds. No runtime ownership was changed.
- `active-close-normal-r8-20261015`: real accepted TCP EOF passed, but the test
  additionally required every still-running remote backend TCP socket to end
  within five seconds of client-only shutdown. It failed at VLESS/Hy2 after six
  successful pairs. Across UDP/QUIC that is not the local worker ownership
  contract. The original failure is retained, not converted into a later PASS.

The coordinator reviewed the corrected r9 fixture ownership boundary:

1. Two identities each have real doko TCP and UDP entry listeners and delivered,
   response-withheld requests. Client-Core Close must complete within five
   seconds and cause actual TCP peer `io.EOF` within the five-second join gate,
   before a safety context expires. r10 checks the precise error, not merely a
   failed read.
2. Both client TCP/UDP listening ports must be bindable after Close. The parent
   UDP socket has no remote EOF; its read worker joins only after the helper's
   explicit caller cancellation/Close. The guard rejects a safety timeout firing
   before this explicit cleanup.
3. Each real relay/exit Core is then explicitly closed. Only after all those
   owners close must backend TCP sockets naturally reach EOF and all owned
   backend workers join within five seconds. No forced backend close is used to
   establish success. Final complete receipt counts must remain exactly one.

This proves local accepted-stream retirement plus complete-fixture shutdown,
**not client-only instantaneous remote teardown**. No QUIC idle, protocol limit,
test timeout or runtime has been widened to make this pass.

## UDP Mode Boundaries

The standalone native encoder test and independent single-hop wire test now
separate mode-specific constraints:

| Mode | Positive Size | Negative Size | Oracle |
|---|---:|---:|---|
| SS2 AES128 + one user EIH, IPv4 | 8126 | 8127 | Native max packet exactly 8192 bytes; ErrPacketTooLarge above; exact wire receipt vs zero |
| Bare VLESS length codec | 8190 | 8191 | Pure encoder emits 8192 bytes vs zero; not claimed as the default cone-mode wire path |
| Default VLESS XUDP writer | 7526 | 7527 | Its existing `length+666 <= buf.Size` check; independent pure writer and actual Core path, exact receipt vs zero |
| Hy2 default, five-digit IPv4 port | 8168 | 8169 | Header+data serializes to 8192 vs -1; actual fragmented QUIC request exact vs zero |
| AnyTLS UoT | 8192 | 8193 | One actual input datagram, exact receipt vs zero; not two net.Conn.Write fragments |

The pure encoding test has four leaves: SS2, bare VLESS, VLESS XUDP and Hy2.
The wire test has four leaves: SS2, **default** VLESS XUDP, Hy2 and AnyTLS. No
protocol buffer or accepted range was expanded, and no failed packet is retried.

## Exact Runner

The following is the executed ActiveClose command; other ordinary rows replace
the run regex, ELF revision and new output filenames only. Identity uses a 5m
safety budget, outage 90m; resource/active/cancellation 5m, UDP boundary 3m,
TwoHop 10m and ThreeHop 15m.

```sh
go tool test2json -t -p github.com/xtls/xray-core/testing/scenarios docker run --rm --pull=never --platform=linux/arm64 --network=none --read-only --cap-drop=ALL --security-opt=no-new-privileges --pids-limit=256 --memory=2g --cpus=2 --tmpfs /tmp:rw,nosuid,size=256m --mount type=bind,src=/tmp/core-protocol-chain-linux-arm64-r9.test,dst=/core-scenarios.test,readonly -e XRAY_PROTOCOL_CHAIN_NETWORK=1 -e XRAY_PROTOCOL_CHAIN_STRICT=1 2abfa7348a80 /core-scenarios.test -test.run '^TestProtocolChainActiveClose$' -test.count=1 -test.parallel=1 -test.timeout=5m -test.failfast -test.v=test2json > /tmp/core-protocol-chain-v015-linux-arm64-active-close-normal-r9-20261015.jsonl 2> /tmp/core-protocol-chain-v015-linux-arm64-active-close-normal-r9-20261015.stderr
```

Strict ELF compile command, with no network/dependency update:

```sh
env GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOMAXPROCS=2 go test -p=2 -gcflags=all=-d=checkptr=2 -c ./testing/scenarios -o /tmp/core-protocol-chain-linux-arm64-checkptr2-r9.test
```

Strict run uses that ELF with the same Docker restrictions and regex
`^TestProtocolChain(ConfigMatrix|UDPEncodingBoundary|TwoHop|ThreeHop|VLESSMux|TLSVerification|UDPBoundary|InvalidIdentity|Cancellation|ActiveClose|Outage|ResourceRounds)$`,
count=1, parallel=1, timeout=20m, failfast and test2json verbose framing.
Its logs are `full-checkptr2-r9-20261015.{jsonl,stderr}` under the same prefix.
Expected total: 12 top-level tests plus 269 leaf passes = 281 test/subtest events,
zero skips/failures. Counts are an acceptance requirement, not a prediction of PASS.

Final r10 ordinary ELF compile:

```sh
env GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOMAXPROCS=2 go test -p=2 -c ./testing/scenarios -o /tmp/core-protocol-chain-linux-arm64-r10.test
```

Final r10 strict compile uses the same command with
`-gcflags=all=-d=checkptr=2` and output
`/tmp/core-protocol-chain-linux-arm64-checkptr2-r10.test`. Exact final runners:

```sh
go tool test2json -t -p github.com/xtls/xray-core/testing/scenarios docker run --rm --pull=never --platform=linux/arm64 --network=none --read-only --cap-drop=ALL --security-opt=no-new-privileges --pids-limit=256 --memory=2g --cpus=2 --tmpfs /tmp:rw,nosuid,size=256m --mount type=bind,src=/tmp/core-protocol-chain-linux-arm64-r10.test,dst=/core-scenarios.test,readonly -e XRAY_PROTOCOL_CHAIN_NETWORK=1 -e XRAY_PROTOCOL_CHAIN_STRICT=1 2abfa7348a80 /core-scenarios.test -test.run '^TestProtocolChain(ConfigMatrix|UDPEncodingBoundary|TwoHop|ThreeHop|VLESSMux|TLSVerification|UDPBoundary|InvalidIdentity|Cancellation|ActiveClose|Outage|ResourceRounds)$' -test.count=1 -test.parallel=1 -test.timeout=20m -test.failfast -test.v=test2json > /tmp/core-protocol-chain-v015-linux-arm64-full-normal-r10-20261015.jsonl 2> /tmp/core-protocol-chain-v015-linux-arm64-full-normal-r10-20261015.stderr
go tool test2json -t -p github.com/xtls/xray-core/testing/scenarios docker run --rm --pull=never --platform=linux/arm64 --network=none --read-only --cap-drop=ALL --security-opt=no-new-privileges --pids-limit=256 --memory=2g --cpus=2 --tmpfs /tmp:rw,nosuid,size=256m --mount type=bind,src=/tmp/core-protocol-chain-linux-arm64-checkptr2-r10.test,dst=/core-scenarios.test,readonly -e XRAY_PROTOCOL_CHAIN_NETWORK=1 -e XRAY_PROTOCOL_CHAIN_STRICT=1 2abfa7348a80 /core-scenarios.test -test.run '^TestProtocolChain(ConfigMatrix|UDPEncodingBoundary|TwoHop|ThreeHop|VLESSMux|TLSVerification|UDPBoundary|InvalidIdentity|Cancellation|ActiveClose|Outage|ResourceRounds)$' -test.count=1 -test.parallel=1 -test.timeout=20m -test.failfast -test.v=test2json > /tmp/core-protocol-chain-v015-linux-arm64-full-checkptr2-r10-20261015.jsonl 2> /tmp/core-protocol-chain-v015-linux-arm64-full-checkptr2-r10-20261015.stderr
```

Final-source static/offline checks passed:

```sh
env GOMAXPROCS=2 go vet -p=2 ./transport/internet ./testing/scenarios
env GOMAXPROCS=2 go run ./infra/vformat/main.go -mode check -pwd ./
git diff --check
```

Darwin/arm64 r10 normal, race and checkptr=2 test binaries are compile-only
artifacts under `/tmp/core-protocol-chain-darwin-arm64-{r10,race-r10,checkptr2-r10}.test`.
They were not run while Sidecar owns the Mac window. No Linux race, native amd64,
final-source Darwin lifecycle or final-source Darwin whole-matrix PASS is claimed.

Freeze manifest: `/tmp/core-protocol-chain-v015-freeze-r9-20261015.sha256`.
Final EOF-oracle manifest: `/tmp/core-protocol-chain-v015-freeze-r10-20261015.sha256`.
ELF manifest: `/tmp/core-protocol-chain-v015-linux-arm64-r10-elf-20261015.sha256`.
After recording these completed runs, this agent freezes all its Core writes,
including reports. The freeze is limited to its owned files, not the main agent's
subsequent owner-context repair. Unrun final-source Darwin gates remain pending;
the existing r6 Darwin normal/race evidence is not silently promoted to the
combined post-owner-fix source. No commit, push, official build or deployment was
performed by this agent. Further evidence can be committed/amended later by the
coordinator without implying that these old ELF files contain the later repair.
The main agent owns subsequent unified checkpoint/build and prepared-candidate
native amd64 execution on Akile without modifying its installed service.
