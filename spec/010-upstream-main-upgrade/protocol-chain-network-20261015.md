# V015 Serial Network Ledger

## Status

The main agent granted this agent the exclusive local socket-heavy window.
After the observed collision the coordinator authorized a narrow shared dialer repair;
its independent baseline-red/fix-green report is in
[spec002](../002-core-connection-lifecycle/system-udp-loopback-source-20261015.md).
No commit, push, checkpoint build or deployment was made.
All logs below are under `/tmp/` on the Darwin verification host. Canonical
develop remains untouched. Source fingerprints for r3/r5 are
`core-protocol-chain-v015-source-r{3,5}-20261015.sha256`.

**Not an all-green acceptance:** the SS2/SS2 UDP return-path failure is retained,
independently reproduced and narrowly repaired. Remaining Darwin lifecycle and
final-source platform gates are still pending. Isolated Linux results and
corrected boundary/ownership fixtures are recorded in
[the Linux ledger](protocol-chain-linux-20261015.md). A pre-repair short PASS did not waive
the failure; runtime authorization followed the tuple/receipt/counter evidence.

## Frozen Repair Results

Manifest: `/tmp/core-protocol-chain-v015-freeze-r6-20261015.sha256`. All following
groups ran serially on that frozen source, with zero skips/failures:

| Group | Leaf Cases | Normal Time | Race Time | JSON Suffix |
|---|---:|---:|---:|---|
| two-hop | 16 | 1.220s | 3.775s | `<mode>-r6-20261015.jsonl` |
| three-hop | 64 | 2.235s | 7.404s | `<mode>-r6-20261015.jsonl` |
| tls | 9 | 4.519s | 5.819s | `<mode>-r6-20261015.jsonl` |
| mux | 4 | 0.534s | 1.984s | `<mode>-r6-20261015.jsonl` |
| ss2-udp-diagnostic50 | 50 | Not run | 5.205s | `race-r6-20261015.jsonl` |

Filename prefix is `/tmp/core-protocol-chain-v015-<group>-`. Each two-/three-hop
group includes one additional Go parent PASS event; these are not extra protocol
combinations. The healthy gate requires both users' nonzero authenticated
traffic at every hop, exact exit user bytes, two real destinations per network,
TCP/UDP exact payloads, once-only final receipts and owned shutdown/port rebind.

The coordinator then requested handing off the Mac window before long lifecycle gates.
The window has been handed to the main agent/Sidecar. No further Mac socket
tests run until it is returned; the subsequently authorized Linux namespace
execution is separate and recorded in the Linux ledger.

Offline work after handoff (exit zero, **not runtime execution**):

```sh
env GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./testing/scenarios -o /tmp/core-protocol-chain-linux-arm64-r6.test
env GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOMAXPROCS=2 go test -p=2 -c ./transport/internet -o /tmp/core-system-udp-loopback-linux-arm64-r6.test
env GOMAXPROCS=2 go test -p=2 -gcflags=all=-d=checkptr=2 -c ./testing/scenarios -o /tmp/core-protocol-chain-checkptr2-r6.test
go vet ./transport/internet ./testing/scenarios
```

`file` confirms the first two binaries are static AArch64 Linux ELF executables;
the third is Darwin/arm64 Mach-O. These commands were compile-only. Subsequent
Linux execution used `--network=none`, not the Mac socket window.

## Pre-Repair Gates

Log names use `core-protocol-chain-v015-<group>-<mode>-rN-20261015.jsonl`.
Their matching `.stderr` files are preserved separately.

| Group | Mode / Revision Suffix | Leaf Cases | Pass Events Including Parents | Elapsed | Result |
|---|---|---:|---:|---:|---|
| config | normal r3 | 80 configs + 3 encoder boundaries | 85 | 0.773s | PASS, no skips |
| config | race r3 | 80 configs + 3 encoder boundaries | 85 | 2.482s | PASS, no skips |
| two-hop | normal r3 | 16 | 17 | 0.999s | PASS, no skips |
| two-hop | race r4 | 16 | 17 | 3.817s | PASS, no skips; prior failure remains unresolved |
| tls | normal r4 | 9 | 10 | 4.696s | PASS, no skips |
| tls | race r4 | 9 | 10 | 5.752s | PASS, no skips |
| mux | normal r4 | 4 | 5 | 0.561s | PASS, no skips |
| mux | race r4 | 4 | 5 | 1.966s | PASS, no skips |
| ss2-udp-diagnostic50 | race r5 | 33 passed, round33 failed | 33 PASS / 2 FAIL | 8.072s | FAIL, stopped at first failed round, zero skips |

UDPBoundary wire, InvalidIdentity, Cancellation, ActiveClose, Outage,
ResourceRounds and strict-checkptr execution are not established by this Darwin
ledger; their subsequent isolated Linux execution is recorded separately.
Native amd64 remains pending. Implementations/compile checks alone are not PASS
evidence. No external client executable is required: these fixtures use real
same-process Core instances and native outbound handlers. They do not call the
existing child-building scenario helper. The diagnostic-only opt-in delegates
to the unchanged DefaultSystemDialer and records its UDP connections.

## Retained Failures

1. `core-protocol-chain-v015-two-hop-normal-20261015.jsonl`: original universal
   8192-byte UDP assumption failed for SS2/VLESS/Hy2 exits. The run was interrupted
   with SIGINT and is not a completed 16-combination result. Protocol limits were
   then separated from the common 1/31/512/1200-byte positive matrix, without
   increasing any runtime buffer or test deadline.
2. `core-protocol-chain-v015-two-hop-race-r2-20261015.jsonl`: fixture TCP-assigned
   port was already occupied on UDP before the next Core started. Joint UDP/TCP
   reservation now discards only allocation EADDRINUSE collisions. It never
   replays application data or retries a failed business request.
3. `core-protocol-chain-v015-two-hop-race-r3-20261015.jsonl`: SS2/SS2 user1 UDP31
   to destination1 failed after both hops authenticated. A focused r4 single-case
   diagnostic and full r4 race matrix passed; they did not explain the failure.
4. `core-protocol-chain-v015-ss2-udp-diagnostic50-r5-20261015.jsonl`: the unchanged
   address-selection workload reproduced the same class of failure in round33.

## Observed UDP Misdelivery

Round33 topology: SS2 hop0 `127.0.0.1:54346`, SS2 hop1 `127.0.0.1:50726`,
backend UDP0 `127.0.0.1:52488`, backend UDP1 `127.0.0.1:61019`.

- An earlier relay freedom socket remained open at local `127.0.0.1:56399`,
  configured remote `127.0.0.1:50726`. It had carried a 578-byte encrypted upload
  and read its 570-byte response successfully.
- A later outer SS2 socket was allocated local `[::]:56399`, configured remote
  `127.0.0.1:54346`, requested source nil. Its 163-byte upload succeeded.
- The intended business user1 UDP31 request reached backend0 exactly once.
  Exit user bytes reached `[151024,151024]`; outer user bytes reached
  `[156622,152652]`. Authentication, dispatch to the requested destination and
  response generation therefore occurred. This is not a wrong-user counter
  attribution or missing destination receipt.
- The 147-byte response was read by the **earlier IPv4 socket**, whose configured
  remote remained hop1, not the new wildcard dual-stack socket. Its SHA-256 was
  `d747414e9e99311007a66e99ddd2b3fbdfb9806fdf4b0cf89a3faf99ba5d51b0`.
- The wildcard dual-stack socket read no response bytes, and returned only after
  the unchanged five-second safety context closed it. The request failed.

The concurrent IPv4/wildcard source-port collision and wrong reader are directly
observed. The exact cause of the earlier r3 instance is still an inference; its
log did not contain source tuples. This does not establish a universal SS2
protocol flaw or cross-platform behavior. No sendThrough workaround or runtime
patch was applied to conceal the observation. The subsequently authorized narrow
system-dialer repair, not a sendThrough workaround, is recorded in spec002 above.

## Protocol Size Boundaries

The separate encoding test establishes these limits without socket timing:

- SS2 AES-128 multi-user client: `8192 - 16 - 16 - 11 - AddrPortLength(dst) - 16`.
  The terms are packet header, one EIH, fixed body, address/port and AEAD tag.
  For the IPv4 fixture this is 8126. Maximum encodes exactly 8192 bytes; max+1
  must return ErrPacketTooLarge and no packet.
- Bare VLESS length codec: `buf.Size - 2 = 8190`; maximum emits the exact
  length-prefixed packet, max+1 emits zero bytes. The configured default cone
  path instead selects XUDP here: its existing `length+666 <= buf.Size` check
  permits 7526, not 8190. Both encoders have independent pure tests; the Linux
  actual-wire test preserves default cone behavior and checks 7526/7527.
- Hy2 UDP: `buf.Size - UDPMessage.HeaderSize()` for the actual destination.
  For a five-digit IPv4 loopback port the limit is 8168. Serialization at max
  returns 8192; max+1 returns -1 **before QUIC fragmentation or backend reads**.
  This independently explains the original 8192 failure even without an SS2 or
  VLESS prefix. Linux wire max/max+1 results are in the Linux ledger; Darwin
  wire boundaries remain pending.
- AnyTLS UoT: 8192 application bytes; the independent Linux wire test established
  exact success at 8192 and zero destination receipts at 8193. Its oversized datagram
  is submitted as one MultiBuffer packet: net.Conn.Write would split it into
  smaller datagrams, which would be an invalid negative fixture.

No limit has been expanded. The baseline-runtime negative comparison is not
claimed merely from a source read. If a new candidate regression is identified,
it still requires an independently owned baseline-red/fix-green runtime gate.

## Commands And Runner Interface

All ordinary runs use this exact command shape; race adds `-race` after `test`:

```sh
env XRAY_PROTOCOL_CHAIN_NETWORK=1 XRAY_PROTOCOL_CHAIN_STRICT=1 go test ./testing/scenarios -run '^TestProtocolChainTwoHop$' -count=1 -failfast -p=1 -parallel=1 -timeout=10m -json > /tmp/core-protocol-chain-v015-two-hop-normal-r3-20261015.jsonl 2> /tmp/core-protocol-chain-v015-two-hop-normal-r3-20261015.stderr
env XRAY_PROTOCOL_CHAIN_NETWORK=1 XRAY_PROTOCOL_CHAIN_STRICT=1 XRAY_PROTOCOL_CHAIN_DIAGNOSTIC=1 go test -race ./testing/scenarios -run '^TestProtocolChainSS2UDPDiagnostic$' -count=1 -p=1 -parallel=1 -timeout=10m -json > /tmp/core-protocol-chain-v015-ss2-udp-diagnostic50-r5-20261015.jsonl 2> /tmp/core-protocol-chain-v015-ss2-udp-diagnostic50-r5-20261015.stderr
```

Executed config regex: `^TestProtocolChain(ConfigMatrix|UDPEncodingBoundary)$`,
timeout 2m. Executed TLS regex: `^TestProtocolChainTLSVerification$`, timeout 3m.
Executed Mux regex: `^TestProtocolChainVLESSMux$`, timeout 5m. All use count=1,
failfast, p=1, parallel=1, JSON and both strict/network variables exactly as above.

Additional runner names/leaf counts: `TestProtocolChainThreeHop` 64;
`TestProtocolChainUDPBoundary` 4; `TestProtocolChainInvalidIdentity` 12;
`TestProtocolChainCancellation` 16; `TestProtocolChainActiveClose` 16;
`TestProtocolChainOutage` 32; `TestProtocolChainResourceRounds` 12 named round
leaves plus explicit warmup. Lifecycle/resource tests target Darwin/Linux.
Strict acceptance must require each expected name/count and reject skips.
