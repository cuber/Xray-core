# Specs 006 and 007 scoped verification evidence

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

Date: 2026-09-27. Environment: Go 1.26.1, darwin/arm64.
Worktree: `/Volumes/Linux/opensource/cuber/xray-core-spec`, branch
`refactor/spec-series-20260927`, inspected HEAD
`a603b44ecc9db77370869e1900e54b4d149d767f`.
This is a shared, dirty worktree. Unrelated changes belong to other workers.
The original `../xray-core` was not modified. No commit, checkout, rebase,
deployment, full-suite run, or AnyTLS suite was performed by this worker.
All Core changes below are tests; no production implementation was changed.

## File ownership

Paths below are relative to the isolated Core worktree.

| Spec | Path | Change |
|---|---|---|
| 006 | `transport/dispatch_contract_test.go` | New pipe ownership, timeout, interrupt, concurrent close and response-packet tests |
| 006 | `app/proxyman/outbound/dispatch_contract_test.go` | New three-entry loopback lifecycle fixture, including singbridge |
| 006 | `transport/internet/hysteria/lifecycle_contract_test.go` | New manager progress and setup/authentication/handshake failure cleanup tests |
| 007 | `app/proxyman/inbound/listener_contract_test.go` | New UDS, SOCKS, real gRPC atomicity, dynamic users and shared-counter tests |
| 007 | `app/proxyman/inbound/listen_test.go` | Existing TCP/UDP multi-listen fixture now asserts exact shared tag counter deltas |
| 007 | `infra/conf/listen_test.go` | Add valid singleton listen array and assert the corresponding proto cardinality |
| 007 | `proxy/freedom/uds_contract_test.go` | New missing/closed/canceled Unix redirect tests using real system dialing |

This evidence file is the only xray-config document changed by this worker.
Parent may group the seven Core paths into the respective spec commits.

## Verification command

Executed from `/Volumes/Linux/opensource/cuber/xray-core-spec`:

```sh
go test -json -race -count=3 -timeout=3m ./transport ./app/proxyman/outbound ./transport/internet/hysteria ./app/proxyman/inbound ./infra/conf ./proxy/freedom ./proxy/socks ./transport/internet -run 'Test(DispatchContract|DispatchEntrancesLifecycle|ClientContract|ClientManager(GetOrCreateDoesNotHoldManagerLockWhileSettingContext|CleanDoesNotHoldManagerLockWhileCleaningClient)|ListenerContract|MultiListen|InboundListen|FreedomUnixContract|ProcessRedirectsToUnixDestination|IsStreamNetwork|SocksLocalAddress|DialUnixDestination|NewDispatchConn|ProxySettingsDialPreservesTimeoutReader)' > /tmp/evidence-006-007-release.json
```

Exit **0**. Eight packages passed, **240 test pass events** including subtests
and three repetitions; zero fail/build-fail events. This is not 240 distinct
top-level tests. No race report. Both IPv4 and IPv6 loopback bindings succeeded;
the multi-listen tests were not skipped.

After that command, the existing TCP/UDP test gained exact counter assertions,
and the three-entry fixture gained an explicit unavailable-FD diagnostic.
Their final versions were checked separately:

```sh
go test -json -race -count=3 -timeout=1m ./app/proxyman/inbound -run '^TestMultiListen(TCPUDP|BindFailureReleasesEarlierAddress)$' > /tmp/evidence-006-007-udp-counters.json
go test -json -race -count=3 -timeout=1m ./app/proxyman/outbound -run '^TestDispatchEntrancesLifecycle$' > /tmp/evidence-006-007-entrances.json
```

The first supplementary command exited **0**, with 18 test pass events.
The second supplementary command exited **0**, with 48 test pass events.
Both had zero fail events and no race report. The latter explicitly logged
`fstatat /dev/fd: bad file descriptor` for both before/after enumeration on all
three repetitions, so no numeric process-wide FD delta is claimed.

All seven files were gofmt-formatted. Scoped `git diff --check` exited 0.
No release build was run: this worker made only test changes, and the isolated
worktree has no `build.sh`; invoking the normal original-checkout build would
not validate these isolated test files and is outside this delegated scope.

## Coverage by verification ID

| ID | Tests and independent assertions |
|---|---|
| V-001 | `TestDispatchContractRunnerExit64KiB`: runner hands off its link without closing it; later 65536-byte payload is echoed byte-for-byte, then Close joins the pipe copier and repeated Close is safe. Existing `TestNewDispatchConnDoesNotCloseWhenRunnerExits` and `CloseUnblocksRunnerRead` were rerun. |
| V-002 | `TestDispatchContractConcurrentCloseInterrupt`: stream and packet modes, each 100 fresh connections, actual `buf.ErrReadTimeout`, 32 concurrent Close/Interrupt calls, both request and response reads terminate within the one-second join deadline. `TestDispatchContractInterruptUnblocksWaitingRead` tests Interrupt without concurrent Close. `TestDispatchContractPacketResponseBoundaries` writes three buffers in one MultiBuffer and reads lengths 1,17,1200 with exact bytes. Existing interface-preservation tests rerun. |
| V-003 | `TestDispatchEntrancesLifecycle`: actual proxySettings Handler.Dial, public DialSystem with dialerProxy, and singbridge.NewOutboundDialer run as three parallel subtests. Each executes 100 rounds of 64KiB echo, refused TCP dial, server early-close, and caller cancel plus explicit Close. Direct loopback echo is the independent byte reference. Each runner and accepted TCP connection is joined; closed reads and operations have bounded waits. |
| V-004 | `TestClientContractOtherConfigurationProgress`: a held client mutex does not prevent another configuration from being obtained within one second, for setCtx and manager clean; held locks are released by deferred cleanup. `TestClientContractFailedCreationCleanup`: 100 rounds of injected dial failure, rejected connection type, peer-observed raw-connection closure, 32 concurrent cleans, and reusable manager entry. `TestClientContractAuthenticationFailureClosesPacketConn`: 100 actual HTTP/3 auth rejections and 100 untrusted TLS handshakes; every UDP socket rejects further writes with net.ErrClosed and repeated clean is safe. HTTP handler invocation count distinguishes these failure stages. `TestClientContractUDPFailureDoesNotPanic` checks inactive-client hop rejection. Existing manager-lock tests rerun. |
| V-005 | `TestListenerContractUnixDialAndFreedom`: real generic Unix Dial and real Core Freedom Unix redirect each echo 64KiB. Generic Dial rejects missing path, closed listener and canceled context. Freedom closes its inbound after the Unix listener disappears. `TestFreedomUnixContractMissingClosedCanceled`: actual Freedom.Process uses real system dialing in all three failure cases; every attempted destination remains UNIX with the exact path, and Process returns an error within its deadline. Existing redirect/network classification tests rerun. |
| V-006 | `TestListenerContractSocksAuthentication`: real authenticated Unix SOCKS plus TCP SOCKS control, wrong password and unknown user rejected with zero increment to the business accept count, correct credentials give one business connection and exact 64KiB echo. Actual UDS service handling covers non-TCP socket addresses; existing `TestSocksLocalAddress` separately verifies Unix fallback address and TCP address preservation. |
| V-007 | Existing three `TestInboundListen*` tests rerun with added singleton array. Omitted/single/array forms build, old/new proto listen fields remain exclusive, array cardinality and roundtrip survive, malformed arrays/TUN/no-port cases reject. Top-level legacy null remains intentionally valid; `[null]` is invalid. Pure Build tests do not start Core. |
| V-008 | `TestMultiListenTCPUDP`: both IPv4/IPv6 addresses and TCP/UDP transports echo five bytes and increment the same tag counters by exactly five bytes per direction, not ten. `TestMultiListenBindFailureReleasesEarlierAddress` verifies TCP/UDP rebind after the second address fails. `TestListenerContractMultiUserAndCounters`: one real gRPC AddUser to a two-address Trojan inbound enables both addresses; each transfers 64KiB with exact uplink 65604 (payload + 68-byte Trojan header) and downlink 65536. One RemoveUser rejects both addresses without additional business accepts. |
| V-009 | `TestListenerContractRPCFailureRetry`: real loopback gRPC HandlerService, non-AnyTLS dokodemo-door, single-address and second-address bind-conflict variants. Failed AddInbound returns RPC error and ListInbounds is empty; partial TCP and UDP listeners can be rebound; releasing the occupier permits same-tag/same-port retry; every configured address echoes 64KiB. RemoveInbound empties ListInbounds and all TCP/UDP addresses rebind. |

V-001 payload SHA256:
`ef4636928161808e87035fa51983821677527ccd9661991c5d0126a778b2268a`.
The final HTTP/3 log explicitly reports 100 rejected auth requests and 100
rejected TLS handshakes per repetition.

## Contract limits and remaining gaps

- **Packet direction matters.** DispatchConnOutputPacket changes only the
  response reader splitter. net.Conn.Write remains stream input split into
  ordinary buffers; this API is not a bidirectional net.PacketConn. The precise
  verified alternative is three separate response buffers, read with a large
  enough destination slice. No claim is made about request packet boundaries,
  arbitrary large datagrams, short-read remainder preservation, or UDS SOCKS
  UDP ASSOCIATE. Those would require a separately specified API change.
- **Cancel is not Close.** Dialer redirect intentionally uses WithoutCancel;
  cnc connection deadlines are no-ops. The three-entry caller-cancel test
  therefore explicitly closes the connection and uses test-owned deadlines
  and joins. It does not claim context cancellation alone interrupts every
  dispatch connection. Changing that would change the existing ownership
  contract, not merely verify it.
- The three-entry fixture uses a controlled loopback outbound processor to
  expose and join the runner. It verifies the real entrance adapters, not
  every protocol implementation's own failure cleanup.
- Process-wide FD counts are diagnostic only. `/dev/fd` enumeration may be
  unavailable on this host; the fixture logs that explicitly. Owned runner,
  listener and accepted-connection joins, raw UDP net.ErrClosed assertions,
  and post-removal rebinds are the precise resource evidence. No global FD
  leak-free or goroutine-count claim is made.
- Hy2 failed creation, real rejected auth, real rejected TLS and repeated
  manager clean are covered. Successful established-session repeated private
  `client.close()` calls are not asserted: that internal method requires a
  live conn and is not an idempotent public Close API. No successful production
  Hy2 endpoint or NAT/UDP-hop environment was used.
- Shared dynamic users are tested using Trojan stream sessions. Independent
  TCP/UDP shared accounting and rollback use dokodemo-door. This does not
  establish dynamic-user behavior for every UDP-capable protocol.
- Live UDS authentication proves handling without TCPAddr assumptions;
  detailed session source metadata is not captured by this fixture. The
  existing address helper test remains the exact fallback-address assertion.
- IPv4/IPv6 worked on this macOS host. Linux-specific FD accounting and
  successful Hy2/NAT operation were not independently exercised here.

## Development attempts (superseded)

JSON artifacts remain in `/tmp`, not committed evidence assets. They are useful
for this shared-machine review but should be archived externally if permanent
raw event retention is required.

| Log basename under `/tmp` | Exit | Result |
|---|---|---|
| `evidence-006-007-first.json` | 1 | UDS echo fixture deadlocked on sequential 64KiB write/read with small macOS socket buffers; changed to concurrent write/read. Other selected tests passed. |
| `evidence-006-007-second.json` | 1 | Freedom test JSON omitted required `unix:` redirect prefix; corrected. Three-entry tests already passed. |
| `evidence-006-007-third.json` | 0 | Initial Hy2, listener and parsing selection passed; initial Hy2 test lacked an auth-handler arrival assertion and is not sufficient auth-rejection evidence. |
| `evidence-006-007-fourth.json` | 1 | New exact timeout assertion used nonexistent buf.ErrTimeout; corrected to buf.ErrReadTimeout. Freedom cases passed. |
| `evidence-006-007-final.json` | 1 | Same timeout assertion compile error in the wider selection; no transport pass claimed. |
| `evidence-006-007-verified.json` | 0 | Wider selection, count=3, passed before strengthening the auth-arrival assertion. Superseded for auth-stage evidence. |
| `evidence-006-007-complete.json` | 1 | Stronger auth assertion correctly found zero HTTP handler calls: connected UDP fixture cannot support QUIC WriteTo. Fixed by using production-shaped unconnected UDP plus PacketConnWrapper. |
| `evidence-006-007-hy2.json` | 0 | Corrected Hy2 test, count=1: actual auth and untrusted TLS stages, all raw packet sockets closed. |
| `evidence-006-007-release.json` | 0 | Final broad scoped race selection, count=3: 8 packages, 240 pass events, no failures. |

One diagnostic `go test -v -race -count=1 -timeout=30s
./transport/internet/hysteria -run
'^TestClientContractAuthenticationFailureClosesPacketConn$'` exited 1 and
printed `use of WriteTo with pre-connected connection`; it was the same fixture
issue subsequently corrected. No failed attempt is counted as passing evidence.
