# 分叉完整清单

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

本页固定原始历史；新提交映射见 [reconstruction.md](reconstruction.md)。
重建不会覆盖这里的历史 hash 或把旧验收记录伪装成新提交的线上证据。

- 审计日期：2026-09-27
- Core base：`b4f08981becb71eaa995fa98ed2098ade92566bb`
- Core head：`e7a2197424614e47d3e728da1d9cdded3454f559`
- Sidecar 对照：`d96dba326614ffd8df11d410137c7576ecf98a36`，仅核对消费契约，
  不声称审计 Sidecar 整个提交历史。
- 范围：21 个提交，累计 147 个变更文件，包括测试、生成 proto、文档、CI 和依赖。
- K1-K6 定义见 [contracts.md](contracts.md)，验证见 [tests.md](tests.md)；
  008 指 [AnyTLS spec](../008-anytls/spec.md)。

## 提交逐笔归属

| 提交 | 契约归属 | 内容 |
|---|---|---|
| `fa30790f` | K1 | dispatch pipe 所有权 |
| `6f5f7928` | K3 | 前缀 URL override |
| `a6bd65e6` | K1 | runner 退出/Interrupt |
| `dfae77f6` | K3 | 多 pingGroups 与冲突验证 |
| `eb669065` | K3 | 锁外 WalkResults 回调 |
| `526589bb` | K4/K6 | 详细路由、user-domain；附带 DoH 测试调整 |
| `4c8ba18f` | K3/K4 | 动态规则与组配置测试 |
| `d4ce07cd` | K1 | Hy2 manager 锁粒度 |
| `751e4ef1` | K3 | keepAlive 开关 |
| `6c3e8fb7` | K2 | Freedom Unix redirect |
| `5867a881` | K2 | Unix stream Dial |
| `8fea74b9` | K4 | weightedLeastPing 及 leastLoad tolerance |
| `70355b3f` | K3 | 204 严格判定、最近失败即死 |
| `c22729d2` | K3 | 恢复迟滞与启动窗口 |
| `460516c5` | K2 | SOCKS UDS |
| `48a99bad` | K5 | 可选 DomainTraffic、RPC、LRU |
| `eb7a9959` | K5 | dispatcher writer 验证 |
| `9cb1f966` | K5 | dispatcher reader 补记上行 |
| `9f82fd94` | K2 | 显式多 IP 监听及失败回滚 |
| `1e0c6a30` | K5 | 认证用户×域名维度，容量调整 |
| `e7a21974` | 008/K2/K6 | AnyTLS、通用 handler 生命周期、DNS/XHTTP/VLESS 修复、依赖及测试设施 |

## AnyTLS 提交不能掩盖的附带变更

- DNS：本地 fixture 信任注入、握手错误资源清理、共享 IP slice 复制。
- XHTTP：WaitReadCloser 同步、uploadWriter 返回字节数、proto 配置指针化。
- VLESS：unsafe 指针计算改为 unsafe.Add；需要 Vision 分支回归。
- Manager：失败入站不发布、清理失败实例、selector cache 锁范围调整。
- 依赖：sing v0.8.14，SS2022/singbridge 也需验证。
- 基础设施：本地 DNS/TLS/QUIC/ECH fixture、公网 proxy env、fmt CI；
  periodic/blackhole/geo matcher 等测试调整归 K6，非生产新功能。
- AnyTLS API、账户、协议、统计、stream 验证及许可仍归 008。

## 累计文件清单

以下为 `git diff --name-only BASE HEAD` 的完整集合。
生成的 *.pb.go 与其 proto 同验，不表示可手工修改生成文件。
公共 README/CI/go.mod/go.sum 归 K6，同时保留其 AnyTLS 依赖和许可关联。

| Core 相对路径 | 契约 |
|---|---|
| `.github/check-gofmt.sh` | K6 |
| `.github/workflows/test.yml` | K6 |
| `README.md` | K6 |
| `app/dispatcher/default.go` | K5 |
| `app/dispatcher/stats.go` | K5 |
| `app/dispatcher/stats_test.go` | K5 |
| `app/dns/dns_test.go` | K6 |
| `app/dns/nameserver_doh.go` | K6 |
| `app/dns/nameserver_doh_test.go` | K6 |
| `app/dns/nameserver_fixture_test.go` | K6 |
| `app/dns/nameserver_local_test.go` | K6 |
| `app/dns/nameserver_quic.go` | K6 |
| `app/dns/nameserver_quic_test.go` | K6 |
| `app/dns/nameserver_tcp_test.go` | K6 |
| `app/dns/network_test.go` | K6 |
| `app/observatory/burst/burstobserver.go` | K3 |
| `app/observatory/burst/config.pb.go` | K3 |
| `app/observatory/burst/config.proto` | K3 |
| `app/observatory/burst/healthping.go` | K3 |
| `app/observatory/burst/healthping_destfor_test.go` | K3 |
| `app/observatory/burst/healthping_result.go` | K3 |
| `app/observatory/burst/healthping_result_test.go` | K3 |
| `app/observatory/burst/manager.go` | K3 |
| `app/observatory/burst/manager_test.go` | K3 |
| `app/observatory/burst/ping.go` | K3 |
| `app/observatory/burst/ping_test.go` | K3 |
| `app/proxyman/config.pb.go` | K2 |
| `app/proxyman/config.proto` | K2 |
| `app/proxyman/inbound/always.go` | K2/008 |
| `app/proxyman/inbound/inbound.go` | K2/008 |
| `app/proxyman/inbound/listen_test.go` | K2 |
| `app/proxyman/outbound/handler.go` | K1 |
| `app/proxyman/outbound/handler_test.go` | K1/K6 |
| `app/proxyman/outbound/outbound.go` | K6 |
| `app/router/command/command.go` | K4 |
| `app/router/command/command.pb.go` | K4 |
| `app/router/command/command.proto` | K4 |
| `app/router/command/command_test.go` | K4 |
| `app/router/condition.go` | K4 |
| `app/router/condition_test.go` | K4 |
| `app/router/config.go` | K4 |
| `app/router/config.pb.go` | K4 |
| `app/router/config.proto` | K4 |
| `app/router/router.go` | K4 |
| `app/router/strategy_leastload.go` | K4 |
| `app/router/strategy_leastload_test.go` | K4 |
| `app/router/strategy_weightedleastping.go` | K4 |
| `app/router/strategy_weightedleastping_test.go` | K4 |
| `app/stats/command/command.go` | K5 |
| `app/stats/command/command.pb.go` | K5 |
| `app/stats/command/command.proto` | K5 |
| `app/stats/command/command_grpc.pb.go` | K5 |
| `app/stats/command/command_test.go` | K5 |
| `app/stats/config.pb.go` | K5 |
| `app/stats/config.proto` | K5 |
| `app/stats/domain_traffic.go` | K5 |
| `app/stats/domain_traffic_test.go` | K5 |
| `app/stats/domain_traffic_user_test.go` | K5 |
| `app/stats/stats.go` | K5 |
| `common/geodata/ip_matcher_test.go` | K6 |
| `common/singbridge/dialer.go` | K1 |
| `common/task/periodic_test.go` | K6 |
| `core/xray.go` | K2/008 |
| `features/stats/stats.go` | K5 |
| `go.mod` | K6 |
| `go.sum` | K6 |
| `infra/conf/anytls.go` | 008 |
| `infra/conf/anytls_test.go` | 008 |
| `infra/conf/freedom.go` | K2 |
| `infra/conf/freedom_test.go` | K2 |
| `infra/conf/listen.md` | K2 |
| `infra/conf/listen_test.go` | K2 |
| `infra/conf/observatory.go` | K3 |
| `infra/conf/observatory_test.go` | K3 |
| `infra/conf/router.go` | K4 |
| `infra/conf/router_strategy.go` | K4 |
| `infra/conf/router_strategy_weightedleastping_test.go` | K4 |
| `infra/conf/xray.go` | K2/K5/008 |
| `main/commands/all/api/inbound_user_add.go` | 008 |
| `main/commands/all/api/inbound_user_anytls_test.go` | 008 |
| `main/distro/all/all.go` | 008 |
| `proxy/anytls/README.md` | 008 |
| `proxy/anytls/acceptance_test.go` | 008 |
| `proxy/anytls/clients_test.go` | 008 |
| `proxy/anytls/config.go` | 008 |
| `proxy/anytls/config.pb.go` | 008 |
| `proxy/anytls/config.proto` | 008 |
| `proxy/anytls/fd_other_test.go` | 008 |
| `proxy/anytls/fd_unix_test.go` | 008 |
| `proxy/anytls/idle.go` | 008 |
| `proxy/anytls/idle_test.go` | 008 |
| `proxy/anytls/integration_test.go` | 008 |
| `proxy/anytls/internal/engine/LICENSE` | 008 |
| `proxy/anytls/internal/engine/ORIGIN.md` | 008 |
| `proxy/anytls/internal/engine/anytls.go` | 008 |
| `proxy/anytls/internal/engine/client.go` | 008 |
| `proxy/anytls/internal/engine/fuzz_test.go` | 008 |
| `proxy/anytls/internal/engine/lifecycle_test.go` | 008 |
| `proxy/anytls/internal/engine/padding.go` | 008 |
| `proxy/anytls/internal/engine/service.go` | 008 |
| `proxy/anytls/internal/engine/session.go` | 008 |
| `proxy/anytls/internal/engine/settings.go` | 008 |
| `proxy/anytls/internal/engine/stream.go` | 008 |
| `proxy/anytls/observability_test.go` | 008 |
| `proxy/anytls/regression_test.go` | 008 |
| `proxy/anytls/resources_test.go` | 008 |
| `proxy/anytls/routing_acceptance_test.go` | 008 |
| `proxy/anytls/server.go` | 008 |
| `proxy/anytls/server_test.go` | 008 |
| `proxy/anytls/udp.go` | 008 |
| `proxy/anytls/udp_budget_test.go` | 008 |
| `proxy/anytls/udp_fuzz_test.go` | 008 |
| `proxy/anytls/udp_resources_test.go` | 008 |
| `proxy/anytls/wire_test.go` | 008 |
| `proxy/blackhole/blackhole_test.go` | K6 |
| `proxy/dns/dns.go` | K6 |
| `proxy/freedom/anytls_test.go` | 008 |
| `proxy/freedom/config.pb.go` | K2 |
| `proxy/freedom/config.proto` | K2 |
| `proxy/freedom/freedom.go` | K2 |
| `proxy/freedom/freedom_test.go` | K2 |
| `proxy/socks/server.go` | K2 |
| `proxy/socks/server_test.go` | K2 |
| `proxy/vless/outbound/outbound.go` | K6 |
| `testing/README.md` | K6 |
| `testing/networktest/proxy.go` | K6 |
| `testing/networktest/proxy_test.go` | K6 |
| `testing/scenarios/command_test.go` | K6 |
| `testing/scenarios/feature_test.go` | K6 |
| `testing/scenarios/socks_test.go` | K2 |
| `testing/scenarios/vless_test.go` | K6 |
| `testing/servers/dnsfixture/fixture.go` | K6 |
| `testing/servers/udp/udp.go` | K6 |
| `transport/dispatch_conn.go` | K1 |
| `transport/dispatch_conn_test.go` | K1 |
| `transport/internet/dialer.go` | K1/K2 |
| `transport/internet/dialer_test.go` | K1/K2 |
| `transport/internet/hysteria/dialer.go` | K1 |
| `transport/internet/hysteria/dialer_test.go` | K1 |
| `transport/internet/splithttp/client.go` | K6 |
| `transport/internet/splithttp/config.go` | K6 |
| `transport/internet/splithttp/dialer.go` | K6 |
| `transport/internet/splithttp/mux.go` | K6 |
| `transport/internet/splithttp/mux_test.go` | K6 |
| `transport/internet/splithttp/wait_reader_test.go` | K6 |
| `transport/internet/splithttp/xpadding.go` | K6 |
| `transport/internet/tls/ech_test.go` | K6 |

## 现有文档覆盖

- 008 已覆盖 AnyTLS，不再创建第二份同名协议 spec。
- docs/domain-traffic*.md 已有设计/历史测试，但混有旧容量与粒度，001/K5 明确当前契约。
- Core infra/conf/listen.md 只有监听说明，缺少规格/任务/统一验证，纳入 K2。
- Core testing/README.md 为现行测试操作入口，K6 补可观察契约与失败门槛。
- 其余分叉能力原先主要依赖代码和测试描述，现补入 K1-K4。

本清单不把后来的上游提交算入本地实现，也不因与上游修复名称相同就宣称可直接替换。
