# Validation: sing-mux Inbound Multiplexing

All checks ran on 2026-10-22 on macOS arm64 against the implementation in this spec's
commit, using Go 1.27 with the module pins in `go.mod` (sing v0.8.14, sing-mux v0.3.7).
V-11 to V-14 (the 2026-10-23 amendment) ran on 2026-10-23 with sing
`v0.9.7-0.20260929150544-0ad23b637bd4` and sing-mux `v0.3.10-0.20260929204512-caf09fe32475`.

## Independent clients

| Client | Version | Path |
|---|---|---|
| mihomo | Mihomo Meta v1.19.31 (go1.26.8, 2026-09-14) | `xray-config/.cache/anytls/tools/mihomo` |
| mihomo | Mihomo Meta 1.19.24 (go1.26.2, 2026-04-19) | `/opt/homebrew/bin/mihomo` |
| mihomo | Mihomo Meta v1.19.33 (go1.26.10, 2026-10-21), from V-11 on | `xray-config/.cache/anytls/tools/mihomo-1.19.33` |
| sing-box | 1.14.2 | `xray-config/.cache/anytls/tools/sing-box-1.14.2-darwin-arm64/sing-box` |
| sing-box | 1.14.3 (go1.26.8; same sing and sing-mux pins as this amendment), from V-11 on | `xray-config/.cache/anytls/tools/sing-box-1.14.3-darwin-arm64/sing-box` |

The external tests read `SINGMUX_MIHOMO` and `SINGMUX_SINGBOX` as path lists, and
`SINGMUX_STRICT=1` turns a missing client into a failure:

```bash
T=../xray-config/.cache/anytls/tools
SINGMUX_STRICT=1 SINGMUX_MIHOMO="$T/mihomo-1.19.33:$T/mihomo:/opt/homebrew/bin/mihomo" \
  SINGMUX_SINGBOX="$T/sing-box-1.14.3-darwin-arm64/sing-box:$T/sing-box-1.14.2-darwin-arm64/sing-box" \
  go test ./common/mux/singmux -count=1 -v
```

Clients run with a temporary config directory only. mihomo started without `-d`
creates `~/.config/mihomo/config.yaml`, so the tests never do that.

## Results

| ID | Check | Result |
|---|---|---|
| V-01 | Unit tests (`common/mux/singmux/server_test.go`). sagernet sing-mux client over in-memory carriers and a recording dispatcher. Covers 20 concurrent streams × 3 protocols × padding, carrier identity (tag, email, sniffing, distinct session IDs), fixed-destination and PacketAddr UDP, the stream cap, revocation with the open stream kept, nested and invalid targets, preamble validation and timeout, disabled server, `Close` joining a held stream, Brutal refusal and `IsDestination` | PASS; `-race -count=3` PASS |
| V-02 | In-process end to end (`TestInProcessClientThroughSS2022`). sing-mux client → SOCKS inbound → Xray SS-2022 outbound → SS-2022 multi-user inbound with `singMux`. Per protocol × padding: 20 streams over domain, `user:` and sniffed-HTTP routes, UDP, 1–4 carriers, per-user traffic counted, closed clients release server state | PASS |
| V-03 | Control plane and revocation. `GetInboundMuxStats` over real gRPC: every inbound sorted, enabled only where configured, all protocol gauges present, default `maxStreams` 256, unknown tag is an error. `AlterInbound` `RemoveUser` refuses the next stream (`revoked`) while the open stream keeps echoing; `AddUser` restores. An inbound without `singMux` serves no carrier. Xray's Mux.Cool client on the same port is reported in `mux_cool` | PASS |
| V-04 | mihomo matrix (both versions): h2mux/smux/yamux × padding with `only-tcp: false` (UDP through the mux), plus the proposed setting h2mux + padding + `only-tcp: true`. Each run: 20 requests (domain, user and sniff routes) and UDP via a mihomo tunnel through a counting relay; 1–4 TCP connections; per-user traffic counted | PASS (14 runs) |
| V-05 | sing-box 1.14.2 matrix: h2mux/smux/yamux × padding, `max_connections: 4`. TCP via SOCKS, UDP via a direct inbound carried in the multiplex; same checks as V-04 | PASS (6 runs) |
| V-06 | JSON config (`infra/conf/singmux_test.go`): omitted, disabled, default, bounded, over-limit (4097) and the AnyTLS exclusion | PASS |
| V-07 | Stress (`TestMihomoH2MuxStress`, mihomo 1.19.31, proposed setting) against Core's Go 1.27 HTTP/2 server. 300 concurrent streams × 256 KiB echoed in 449 ms over 4 TCP connections (4 carriers). 50 streams abandoned mid-transfer were all released. After every carrier was cut, requests opened new carriers. Idle carriers closed after 30 s (GOAWAY), and requests afterwards succeeded | PASS |
| V-08 | Full suite `go test ./... -count=1 -timeout 60m` with both external clients set: 104 packages ok, 77 without tests, no failures, including `proxy/anytls` (336 s), `proxy/shadowsocks_2022` and `testing/scenarios`. Touched packages (`common/mux/...`, `app/proxyman/...`, `infra/conf`) also pass under `-race` with the external clients | PASS |
| V-10 | Standard SS-2022 on the inbound with `singMux` enabled. Xray's own SS-2022 outbound (`TestPlainSS2022OnSingMuxInbound`) and released clients with multiplexing off (mihomo 1.19.31, mihomo 1.19.24 and sing-box 1.14.2, `plain-ss2022`). Each: 20 TCP requests over domain, `user:` and sniffed routes, plus UDP. Each request used its own connection (20 TCP); per-user traffic was counted; the sing-mux and Mux.Cool servers saw no carriers. UDP on plain SS-2022 is also covered by the `only-tcp: true` runs of V-04 | PASS |
| V-09 | Real entry config: `pgz.luck` with `singMux` on `ss-auto-in`/`ss-us-in` passes `run -test` with a build of this tree. The approved `8d15534` build also reports "Configuration OK" because it ignores the unknown field: rollout must check runtime `enabled` | Recorded (spec Edge Cases) |
| V-11 | `TestEndingCarriersDoNotRaceTheH2MuxSession` (`common/mux/singmux/carrier_end_test.go`): 25 rounds × 16 h2mux carriers, each served on a cancellable context and ended the way the SS-2022 inbound ends it (link interrupted while the context is cancelled). sing-mux v0.3.7: "close of closed channel" panic in 3 of 6 runs. Amendment pins: 6 of 6 runs pass, and 3 more under `-race` | PASS |
| V-12 | `TestEndingCarriersLeaveNoH2MuxHandlers`: streams keep opening while 16 carriers end, 10 rounds; afterwards no goroutine may remain in `(*h2MuxServerSession).ServeHTTP`. v0.3.7: about 9,500 stuck handlers in 3 of 6 runs. Amendment pins: none, in every run | PASS |
| V-13 | Full suite `go test ./... -count=1 -timeout 1h`, alone on the machine, with `resources/geoip.dat` and `geosite.dat`: 104 packages ok, 77 without tests, no failures, including `proxy/anytls` (339 s), `proxy/anytls/internal/engine`, `proxy/shadowsocks_2022`, `common/mux/singmux` and `testing/scenarios`. An earlier run concurrent with a baseline run failed only on `connect: can't assign requested address` (ephemeral port exhaustion, seen in the baseline too); those packages passed when rerun alone | PASS |
| V-14 | Independent clients, amendment pins. `SINGMUX_STRICT=1` with mihomo 1.19.33, 1.19.31 and 1.19.24 and sing-box 1.14.3 and 1.14.2: the V-04/V-05 matrices (24 mihomo and 14 sing-box runs, including `only-tcp: true` and `plain-ss2022`) and `TestMihomoH2MuxStress`. AnyTLS `TestExternalClients` with mihomo 1.19.33 + sing-box 1.14.3, mihomo 1.19.31 + sing-box 1.14.2, and mihomo 1.19.24 | PASS |

## Not covered

- No device test of Clash Verge, FlClash or CMFA apps; their mihomo cores are covered by V-04.
- No Linux runtime run. The Linux build comes from `xrayctl build core`.
- No deployment, so no acceptance check exists yet.
- Amendment (V-11–V-14): macOS only and not yet deployed; boards are checked through xray-config spec009 acceptance after `deploy --core`.
- Consumers (`/xray-debug`, Portal, acceptance, subscriptions) are validated in
  [Sidecar spec010](../../../xray-sidecar/spec/010-entry-multiplex/validation.md).
