# Plan: Connection Control

- Spec: [spec.md](spec.md)

## Core

No library change; the quic-go pin stays. No configuration field: `HandlerService` is already enabled on the nodes.

- **Registry** (`app/proxyman/inbound/worker.go`):
  - `acceptedConnection` gains `kind` (`connTCP`, `connHy2Stream`, `connHy2UDP`, from the accepted connection's type:
    `*hysteria.InterConn` is a UDP session, `hysteria.IsStream` a TCP stream), `email` (an `atomic.Pointer[string]`,
    set by compare-and-swap from nil, so once), `carrier` (an `atomic.Uint32` holding a `mux.Carrier`) and `closing`
    (an `atomic.Bool`).
  - `streamOwnership.accept` binds a connection that has `User() *protocol.MemoryUser` (Hy2's streams and UDP sessions)
    at once.
  - `tcpWorker.callback` hands `process` a `boundDispatcher{Dispatcher: w.dispatcher, entry}` instead of
    `w.dispatcher`; the protocol's `Process` receives it as its dispatcher. Its `Dispatch` and `DispatchLink` call
    `observe` and forward. `observe` returns at once when the entry is bound; otherwise it reads
    `session.InboundFromContext(ctx).User` on the calling goroutine (the one that wrote it, or one holding a copy
    made before), asks the inbound's `mux.Server` (`Carrier(dest)`) whether the destination is one of its carriers, and
    binds. The protocols already read `inbound.User` on that goroutine when routing, so no new cross-goroutine read.
  - `entries()` copies the live entry pointers under the lock; `claim(match)` marks bound, matching entries closing
    by compare-and-swap under the lock and returns them.
  - Considered and rejected: binding in `common/mux`'s `Server.Dispatch` through a context value, which would need a
    neutral package for the entry type and still miss inbounds whose protocol dispatches outside the mux server;
    binding inside each protocol, which touches six protocols and every future one.
- **Mux server** (`common/mux/server.go`): `Carrier` (`NoCarrier`, `MuxCoolCarrier`, `SingMuxCarrier`) and
  `(*Server).Carrier(dest)`, the same decision `Dispatch` takes.
- **QUIC** (`transport/internet/quic_stats.go`, `quicrecovery`):
  - `quicConnEntry.closing`; `Census()` copies the set under its lock and counts live connections by user (`""` for
    none) and those closing; `Claim(match)` marks and returns the live, user-bound, `QuicCloser` connections whose user
    matches.
  - `QuicCloser.CloseByControl(reason)`; `quicrecovery.conn` implements it as `CloseWithError(0x100, reason)`, the
    code the Hy2 listener closes its connections with.
- **SS-2022** (`proxy/shadowsocks_2022`): `ServerUDPSession.shut() bool` is `Close` that says whether it closed;
  `MultiUserInbound.UserSessions()` and `CloseUserSessions(match)` walk the session map.
- **Handler** (`app/proxyman/inbound/connections.go`): `AlwaysOnInboundHandler.Connections()` and
  `CloseConnections(match, bound)`. The close cancels each claimed entry's context, then runs every connection close
  (`Abort` for a Hy2 stream, `hysteria.interConn.Abort`: CancelRead and CancelWrite) and QUIC close in its own goroutine; `closeBounded` waits for them up to `bound` and counts the rest as pending.
  UDP sessions close synchronously (`Close` takes only the session's own lock and interrupts pipes).
- **RPCs** (`app/proxyman/command/connections.go`, `command.proto`): `GetConnections`, `CloseConnections`; messages
  `ConnectionCounts`, `GetConnectionsRequest`, `ConnectionUser`, `InboundConnections`, `GetConnectionsResponse`,
  `CloseConnectionsRequest`, `InboundClosed`, `CloseConnectionsResponse`. Inbounds close in parallel, each bounded by
  two seconds. Regenerated with the files' existing protoc header (protoc 34.1, protoc-gen-go 1.36.11,
  protoc-gen-go-grpc 1.6.1); additive.
- **CLI** (`main/commands/all/api/connections.go`): `conns` and `connclose`.

## Tests

- `app/proxyman/inbound/connections_test.go`: binding at the first dispatch and case-insensitive rows, carrier marks,
  kinds and accept-time binding, selection (unbound and excluded users stay, idempotent), the pending bound with a
  blocking close, and `TestCloseRaces`: four goroutines accepting and ending connections, four closing (by user and
  all) and reporting, then the worker's close, under `-race -count=3`.
- `app/proxyman/command/connections_contract_test.go` (CP-7): one instance whose client outbounds dial its own
  SS-2022 (TCP and UDP), VLESS with Mux.Cool, Trojan over TLS, Hy2 and a Hy2 relay inbounds; every closure checked on
  both the report and the client connections, the arguments, and a default configuration.
- `main/commands/all/api/connections_test.go`: both commands' output, the request they send, and an older Core.
- sing-mux carriers with real clients: the Akile gate (xray-config spec034), where mihomo and sing-box drive them.
