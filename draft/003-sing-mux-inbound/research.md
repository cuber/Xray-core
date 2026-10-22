# Research: sing-mux Inbound Multiplexing

Collected 2026-10-22 by reading local checkouts and the Go module cache. Nothing was
compiled or run. Core paths are relative to this repository; library aliases:

| Alias | Path under `/Users/cube/Dev/go/pkg/mod/github.com/` |
|---|---|
| `SM` | `sagernet/sing-mux@v0.3.9-0.20260919141002-baf887b90a62` (sing-box pin) |
| `MM` | `metacubex/sing-mux@v0.3.10` (mihomo client pin) |
| `SING` | `sagernet/sing@v0.8.14` (Core pin) |
| `SMUX` | `sagernet/smux@v1.5.50-sing-box-mod.1` |
| `YAMUX` | `hashicorp/yamux@v0.1.2` |

## 1. Mux.Cool Today

- **Wrapping.** `AlwaysOnInboundHandler` owns `mux.NewServer(ctx)` and passes it as the
  dispatcher to the TCP, UDP and Unix workers (`app/proxyman/inbound/always.go:112,139,162,177`).
  `Start`/`Close` are no-ops and there is no worker registry (`common/mux/server.go:77-85`).
- **Detection.** `Dispatch`/`DispatchLink` compare only `dest.Address` with `v1.mux.cool`
  (`server.go:42,63`; `client.go:190`). Anything else passes unchanged.
- **Sub-streams.**
  - `SubContextFromMuxInbound` copies `Content` by value, including `SniffingRequest`, and
    shares the `*Inbound`, so tag, Source and User are inherited
    (`common/session/context.go:48-60`).
  - Each stream gets an access log with the email (`server.go:176-187`) and is dispatched to
    the raw `DefaultDispatcher` (`server.go:266`).
  - Sniffing then runs per stream (`app/dispatcher/default.go:305-339`).
- **Ending.** On a carrier read error or EOF, `run` exits and every session closes
  (`server.go:365-386`, `session.go:140-156`). An idle worker retires after 60–120 s
  (`server.go:101,134-137`).
- **Accounting.**
  - Per-stream links get user traffic, domain traffic and online-IP tracking
    (`default.go:143-250`).
  - The carrier bypasses `getLink` and is counted only by `inbound>>>tag`
    (`worker.go:248-254`).
  - Online-IP removals fire only at carrier end, because streams have no own cancel
    (`default.go:244-250`, `worker.go:122-124`).

## 2. SS-2022 and Other Inbounds

- **Multi-user SS-2022.** Sets `inbound.User` before `dispatcher.Dispatch` on `h.mux`
  (`proxy/shadowsocks_2022/inbound_multi.go:263-275`). Early data is written into the link
  (`:282-287`). The handshake deadline is cleared after authentication (`stream.go:625`).
  `TransportTCP` applies the user level's timeouts to the carrier (`stream.go:631-659`).
- **Single-user SS-2022.** `inbound.go:86,135`.
- **VLESS.** `DispatchLink` (`proxy/vless/inbound/inbound.go:626-633`).
- **AnyTLS.** Drops `v1.mux.cool` explicitly (`proxy/anytls/server.go:447`, spec008 FR-010).
- **Current sing-mux behaviour.** A sing-mux carrier is routed as the domain
  `sp.mux.sing-box.arpa:444` and dialed.
- **Revocation.** SS-2022 `RemoveUser` keeps authenticated TCP alive
  (`inbound_multi.go:159-160`; spec011). AnyTLS checks revocation on each stream open
  (`anytls/server.go:295,432`).

## 3. sing-mux Library

- **Service.**
  - `ServiceOptions{NewStreamContext, Logger, HandlerEx, Padding, Brutal}` (`SM/server.go:39-46`).
    `NewStreamContext` and `Logger` are mandatory (`:71,73,98,103,162`).
  - `NewConnectionEx` blocks until the session ends (`:67-111`).
  - Handlers get `onClose = nil` and own the stream conn (`:167,182`).
- **Magic destination.** `sp.mux.sing-box.arpa:444` (`SM/protocol.go:33-36`). sing-box
  compares domain and port (`sing-box common/mux/router.go:71`).
- **Preamble.**
  - Version 0/1, then protocol `smux=0`/`yamux=1`/`h2mux=2`.
  - Version 1 adds a padding flag and 256–767 filler bytes (`SM/protocol.go:18-27,44-89`).
  - The server auto-detects the protocol per session (`SM/session.go:50-70`).
  - The server's `Padding` option only rejects unpadded sessions (`SM/server.go:82-85`).
  - Padding frames the first 16 reads and writes in each direction (`SM/padding.go:16,64-197`).
- **Stream request.**
  - u16 flags (UDP, PacketAddr) plus a SOCKS address (`SM/protocol.go:109-141`).
  - Sent lazily with the first payload (`SM/client_conn.go:49`).
  - The status byte is prepended lazily to the first server write (`SM/server_conn.go:36-61`).
- **UDP.** Fixed destination uses `[u16 len][payload]`; PacketAddr uses
  `[socksaddr][u16 len][payload]` (`SM/server_conn.go:100-124,235-266`). mihomo uses
  PacketAddr (`mihomo adapter/outbound/singmux.go:56`).
- **Brutal.** A brutal-enabled client fails the whole session if the server refuses
  (`SM/client.go:265-271`; `MM/client.go:203-209`).
- **Wire compatibility.**
  - `MM` and `SM` differ in `protocol.go`, `padding.go`, `server_conn.go` and `brutal.go`
    only by import paths and an equivalent uvarint string helper.
  - smux forks have identical `frame.go`/`mux.go`.
  - metacubex yamux (libp2p fork) and hashicorp yamux share the 12-byte v0 header.
  - h2mux is `x/net/http2` on both sides.
- **Server resources.**
  - smux v1: no per-stream window (session-wide 4 MiB bucket), accept backlog 1024,
    keepalive off (`SMUX/mux.go:68`, `session.go:39,394-404`; `SM/session.go:140`).
  - yamux: 256 KiB per-stream window with no session cap, 30 s pings, and a panic on nil
    addresses (`YAMUX/const.go:125`, `mux.go:64-68`; `SM/session.go:72-76`).
  - h2mux: 250 streams, 1 MiB windows, 30 s idle GOAWAY (`SM/h2mux.go:23,40-43`; x/net
    `http2/config.go:101-110`).
- **Dependency.**
  - `SM/go.mod` requires `sagernet/sing v0.9.5`, `hashicorp/yamux v0.1.2` and
    `sagernet/smux v1.5.50-sing-box-mod.1`.
  - Core `go.mod:20` pins sing v0.8.14, and Core imports none of these modules.
  - Every sing symbol sing-mux uses exists in v0.8.14.
  - v0.9 breaks `N.CloseRead/CloseWrite` signatures and vectorised packet-waiter types;
    AnyTLS uses neither.
- **Release requirements** (proxy.golang.org `.mod`/`.info`):

  | sing-mux | Date | Requires sing |
  |---|---|---|
  | v0.3.3 | 2025-08-13 | v0.6.7 |
  | v0.3.4–v0.3.7 | 2026-01-16 to 2026-09-19 | v0.7.14 |
  | v0.3.8–v0.3.9 | 2026-09-19 to 2026-09-27 | v0.9.5 |

- **v0.3.7 on sing v0.8.14.** Verified 2026-10-22 with a probe module in the session
  scratchpad: `go mod tidy` keeps sing v0.8.14 and adds yamux v0.1.2 and smux
  v1.5.50-sing-box-mod.1; `go build`, `go vet` and a `NewService` run succeed.
- **v0.3.7 vs v0.3.9.** Only `client.go`, `error.go`, `server.go` and `session.go`
  differ (158 lines). The server change is struct-literal field names; the wrappers
  gain `onClose` close callbacks that only the client's idle-session tracking sets.
- **Commits v0.3.5 → v0.3.9** (`gh api repos/sagernet/sing-mux/compare/v0.3.5...v0.3.9`).
  All h2mux fixes are in `httpConn`'s late-setup path, which only the client uses (the
  server builds `httpConn` with its reader set).

  | Tag | Commit | Change | Side |
  |---|---|---|---|
  | v0.3.6 | `e57b3528` | CONNECT request gets `Header: make(http.Header)` ("Fix h2mux client with Go 1.27") | client |
  | v0.3.7 | `cdfbf1fa` | Wrap h2mux setup errors with `baderror.WrapH2` | client (late setup) |
  | v0.3.7 | `30dbac20` | Fix the data race between late setup and `Read`/`Close` | client (late setup) |
  | v0.3.8 | `7e85abc8` | go.mod only: require sing v0.9.5 "for HTTP/2 error wrapping" | dependency |
  | v0.3.9 | `0257b84c` | `SetKeepIdleConnections`, `CloseIdleConnections`, `ContextWithKeepSession` | client; server only renames struct-literal fields |

  sing-box pins `baf887b9`, the same client feature committed directly on `7e85abc8`.
- **What sing v0.9.5 changes.**
  - `baderror.WrapH2` wraps errors containing `stream error:`, `connection error:` or
    `http2: server sent GOAWAY` in a `protocolError`.
  - Without it, an outer x/net or grpc-go HTTP/2 reader that reads directly from an
    inner tunnel conn type-asserts `http2.StreamError`, keeps reading, and spins
    (`sing@v0.9.5/common/baderror/baderror.go`).
  - This requires HTTP/2 framing that reads from a conn returning another HTTP/2
    layer's errors.
  - On the server, only the h2mux framer reads, and it reads the carrier. Over SS-2022
    raw TCP the carrier never returns HTTP/2 errors. Over an HTTP/2-based transport
    (gRPC, h2, XHTTP) it could.
- **mihomo's fork.** `metacubex/sing-mux` v0.3.10 fixes the late-setup race its own way
  (`setupOnce`, `h2mux_conn.go:40-56`). Its `metacubex/sing` v0.5.8 has no
  `protocolError`. mihomo uses x/net v0.35.0 (legacy HTTP/2) and builds releases with
  Go 1.26 (`.github/workflows/build.yml:167`).
- **Core's HTTP/2.** Core is `go 1.27` with x/net v0.61.0, whose `http2.Server` wraps
  net/http's internal HTTP/2 (`x/net/http2/server_wrap.go`, `go1.27 && !http2legacy`).
  An h2mux server in Core therefore runs on a different HTTP/2 implementation from the
  mihomo client, whatever the sing-mux version.
- **Licences.** sing-mux and sing: GPL-3.0-or-later. smux: MIT. yamux: MPL-2.0.

## 4. mihomo Client Behaviour

- **Configuration.** `smux:` wraps any proxy (`mihomo adapter/parser.go:234-245`).
- **Dialing.** The carrier is dialed through the SS proxy to the magic destination
  (`component/proxydialer/sing.go:23-25`).
- **No fallback.** A dial error is returned with no fallback to plain
  (`adapter/outbound/singmux.go:41-46`). The tunnel retries up to 10 times
  (`tunnel/tunnel.go:739-760`).
- **Pooling** (`MM/client.go:135-175`).
  - It picks the least-loaded session.
  - With `max-connections > 0`, it reuses that session when the session count is at the
    limit or the session has fewer than `min-streams` streams; otherwise it dials a new one.
  - With every limit zero, it dials a new carrier whenever all sessions are busy, so
    `max-connections` must be set.
  - There is no client idle close (`MM/client.go:241-253`).
- **UDP.** `only-tcp: true` sends UDP through the plain proxy (`singmux.go:49-52`).

## 5. Upstream

- No `sing-box.arpa`, `sing-mux`, `smux`, `yamux` or `h2mux` in this tree or in
  `origin/main` `0888beb3` (2026-10-14).
- Upstream removed `common/singbridge` (#6831) after limiting sing to SS-2022 (#6286).
