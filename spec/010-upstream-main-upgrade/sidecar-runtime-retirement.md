# Sidecar Runtime Ownership and Retirement

Candidate-only work, 2026-10-15. No canonical pin, commit or deployment.

## Ownership Boundary

`DialSystem` resolves dialerProxy handlers and strategy-driven A/AAAA DNS
through the Core instance in its request context. It never falls back to the
last-created instance. Core installs resolver callbacks at package init to
avoid a transport-to-Core import cycle. Missing owner DNS produces an error;
the existing UseIP versus ForceIP fallback policy is unchanged.

The contextless exported `LookupForIP` remains a compatibility API using the
last `InitSystemDialer` DNS, protected against data races. It does NOT provide
instance isolation. System-default DNS (including SRV/TXT address/port
strategies), alternate system dialers and other legacy callers are outside
this instance-local A/AAAA contract. No whole-process isolation claim.

HY2 registry keys include owner identity even if callers share stream settings.
Unix destinations remain streams. Dispatch returns the concrete `*cnc.Connection`
needed by packet adaptation. Detached dispatch retains request context values;
closing the connection cancels its relay context and interrupts its pipes.

## Retirement

The retained goroutines were attributed to `client.close` waiting inside
`quic.Transport.Close`: dispatch-backed FakePacketConn read deadlines are
no-ops, and the underlying connection was only closed AFTER that wait. This
also blocked the periodic manager sweeper, retaining later clients.

Close the QUIC connection, then its owned packet connection, then join the
transport. Apply the same ordering to authentication failure. No global sweep
or unrelated cleanup is added.

`Instance.OnClose` registers synchronous owned cleanup, rejects registration
once shutdown starts, and runs hooks exactly once before feature teardown.
Hooks run under the existing statusLock and MUST NOT call lifecycle methods
such as IsRunning, Start or Close. The HY2 hook removes only its own registry
entry, releases the manager lock, cancels pending dial admission, and joins
client cleanup. It does not call instance lifecycle methods. The retirement
registration lock is released before invoking hooks, avoiding registry lock
inversion. The periodic sweeper checks IsRunning without holding its map lock.

Tests cover synchronous Close joining, late-registration rejection, exactly-once
cleanup, cancellation of a blocked dial, and actual QUIC shutdown over a
packet connection whose deadlines are deliberately no-ops.

## SOCKS UDS

CONNECT replies use representable loopback/port-zero BND fields, not a Unix
path encoded as a domain. Authentication and requested destination are
unchanged. Core's strict client now parses that reply and transfers echo data.
UDP ASSOCIATE over UDS remains explicitly rejected by the independently added
protocol regression. HY2 over UDS needs an explicit stream-capable UDP relay
(tested VLESS); neither direct UDS ASSOCIATE nor SS2's native UDP provides it.

## Validation Boundary

Owned package race count=3 passed (core, transport, internet, hysteria, socks).
Sidecar extended churn subsequently found a DIFFERENT pending shutdown bug:
udpWorker.Close does not close activeConn pipes, leaving SS2022 processUDP
readers behind. Three real SS2->HY2 exchanges reproduce three retained readers;
450 exchanges expose 18 readers. See Sidecar spec006
`ss2-udp-reader-retirement.log` and `churn-fixed-long.log`. Main was notified;
worker.go is outside the explicitly assigned file ownership. This prevents
overall stability acceptance despite the passing HY2/dialer/UDS fixes.

Main subsequently repaired worker admission and active-pipe retirement. The
focused real Sidecar reproduction plus 100-chain churn passed three race rounds
(20.067s); worker ownership unit tests passed 20 rounds (1.854s). See
[spec002 follow-up](../002-core-connection-lifecycle/upgrade-20261014.md). These close
the observed reader retention reproducer, not the remaining full acceptance gates.

Sidecar spec006 contains full-suite and actual 25-pair/UDS/resource evidence.
Candidate build wrapper was invoked with XRAY_CORE_SRC pointing here and an
isolated /tmp output path. It correctly refused this dirty tree. Formal
Darwin/Linux release artifacts remain blocked until an authorized clean state;
do not bypass this guard or interpret Go test binaries as release artifacts.
