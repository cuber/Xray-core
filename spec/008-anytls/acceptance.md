# AnyTLS 验收证据映射

2026-09-26。本页对应内部试点范围，不是公开分发许可或全节点推广许可。
构建、远端结果和已知基线失败见 [implementation.md](implementation.md)；
压力预算见 [resource-audit.md](resource-audit.md)。测试名称可在本 Core 仓库的
`proxy/anytls/`、`infra/conf/anytls_test.go` 和 Sidecar 测试文件直接定位。

## 功能覆盖

| 验收项 | 已通过证据 |
|---|---|
| AT-001/002/003 | `TestConfigurationAndAccount`、`infra/conf` AnyTLS 测试、`test/tools/test_anytls_binary.py`；原生配置、空表、唯一性、限制、REALITY/非 TCP/明文/outbound 拒绝 |
| AT-004 | `TestAnyTLSAuthenticationFailures`；空用户、错误密码、错误 CA/SNI 拒绝；独立客户端验证正常 TLS |
| AT-005/002 | `TestAnyTLSControlRoutingStatistics`、`TestAnyTLSUserDomainRouting`；同目标按用户走不同 marker，user-domain/inbound/network 组合 |
| AT-007 | `TestAnyTLSSingleSession100StreamsAndRevocation`；100 流，多目的地，关闭半数后存活流仍可交换 payload |
| AT-008 | `TestAnyTLSExactLargePayload`；1 MiB 上行/2 MiB 下行精确计数 |
| AT-009/006/007 | `TestAnyTLSPaddingAndIPAccounting`、`TestAnyTLSStatisticsSwitches`、`TestAnyTLSUoTConcurrentAccounting`；padding 不改变 payload，双用户独立，IP 为 unknown，无 UoT 伪域名 |
| AT-012/015 | `TestAnyTLSControlRoutingStatistics`、`TestUserConcurrentMutations`、`TestUserRevocationAndBudgets`；真实 gRPC 增删查及重加，旧凭据/generation 不复活 |
| AT-013/014 | `TestRevocationAdmissionBarrier`；100 并发准入与撤销，撤销返回后新准入全拒绝。查表与登记在同一锁内，不存在计划中“查表后解锁暂停”的中间窗口；不为测试引入生产解锁点 |
| AT-016/017/018/019 | `TestAnyTLSUoT`、`TestAnyTLSUoTIndependentIPv4IPv6Targets`、`TestAnyTLSUoTRejectsUnsupportedSize`、`TestAnyTLSUoTConcurrentAccounting`；双目标 marker/回包源、IPv4/IPv6、范围拒绝、双用户上下行精确值；独立客户端 UoT |
| AT-020 | engine 帧/认证 fuzz、`FuzzUoTAdapter`、握手 deadline；分片、截断、畸形输入及准入回收 |
| AT-021/022 | `TestAnyTLSNetworkAdmissionAndHandshakeTimeout`、`TestAnyTLSResetReleasesSessionQuota`、真实 TCP/UoT 三轮资源测试、慢读撤销、engine deadline/close 回归；超限、RST、阻塞 I/O 与退出恢复 |
| AT-023/024/025 | Sidecar `TestAnyTLSUserSyncWithRealCore` 及 usersync factory/预检/错误隔离测试；混合 SS2/AnyTLS、admin 保护、未知协议拒绝、重复同步和部分失败不伪报成功 |
| AT-026 | Sidecar `TestAnyTLSSidecarTrafficSnapshotAndCoreRestart`；真实流量、0600 快照、同 epoch 重放去重、Core 重启清零计数、新 boot ID 合并历史、不重复记账 |
| AT-027 | `TestAnyTLSCertificateAndAccessLog`；实际 TLS/API 证书一致，日志身份/目标/tag；Sidecar debug 只读运行时并脱敏 |
| AT-028 | `TestExternalClients` 的 SS2/Hy2 回归、`TestVLESSAfterAnyTLSRemoval`、相关共享包和 Sidecar 全量 race；整库既有失败单列，不声称全 Core 绿 |
| AT-029/030 | `TestAnyTLSFailedAddIsAtomic`、`TestAnyTLSRPCInvalidConfigAtomicity` 及真实控制面测试；添加/删除/查询、重名、端口冲突、坏证书/非法 proto 设置无幽灵 handler |
| AT-031/032 | Sidecar 真实 gRPC 集成：动态用户运行时可见，API-only 入站带用户重启后消失且端口释放，受管入站由 usersync 恢复 |
| AT-033 | `TestAnyTLSInboundCounterBoundary` 对受管连接字节精确断言；100 流、双用户统计与 padding 测试补充覆盖，不把 payload 等同 inbound 协议总量 |
| AT-034 | Sidecar `TestAnyTLSSidecarTrafficStatisticsSwitches` 穷举 8 种 inbound/user/domain 开关组合，分别验证上下行及 HTTP/debug 结果 |
| AT-035 | 独立 SS2/AnyTLS 同 email 精确汇总；padding/IP 和 sniff 测试保留现有 unknown 边界，不伪造域名补齐总量 |
| AT-036 | sing-box 同端口 Hy2 UDP/AnyTLS TCP；独占 TCP 冲突及删除后 VLESS 回归，不修改生产旧监听 |

`TestAnyTLSSniffedDomain` 另验证 IP 请求经 HTTP Host 嗅探后选中正确出口。
首段上行在嗅探前可记入 unknown；嗅探后的下行和后续上行归域名，保持 Dispatcher
既有契约，不回搬已记字节。Sidecar 重启用丢弃 collector/aggregator 后重建及加载
真实快照模拟，不把它描述成操作系统整机重启测试。

## 非功能门槛

- 认证、帧、padding、UoT 适配器分别执行至少 60 秒 fuzz；认证 18,236,379、
  帧 3,899,994、padding 1,332,254、UoT 632,939 个输入，无失败。
- sing-box 1.14.2 与 Mihomo 1.19.31 各 50 TCP 流，持续 10 分钟；两用户不同
  marker 出口，每用户实际发送/接收字节与 StatsService 增量精确一致。
- TCP 100 轮连接与三轮堆/FD/协程回收；UoT 128 真实目标占满预算，撤销后
  额度恢复，预热加三轮连续运行 20 次（80 轮）通过；慢消费者测试通过。
- Core 相关包 race、Sidecar 全量 race/vet、Python 全量和显式二进制验证通过。
  外部客户端在最终相关测试中显式提供，不以缺少工具的 skip 作为互通证据。
- 默认资源值已定稿；共享上限不保证新请求的逐用户公平性，不限制内核 socket
  和所有异步 outbound 的 RSS，详见资源审计。
- Core/Sidecar 各自通过批准入口构建 Darwin/Linux；试点只在 SHI，详细远端
  版本、十分钟观测和清理记录在 [pilot.md](pilot.md)。

## 复现日志

Core 工作目录为本仓库根目录（两个客户端路径须显式给出，缺失工具不是通过）：

```sh
ANYTLS_SINGBOX=/absolute/path/to/sing-box ANYTLS_MIHOMO=/absolute/path/to/mihomo \
  go test -race ./proxy/anytls/... ./main/commands/all/api \
  ./app/proxyman/inbound ./app/dispatcher ./app/stats ./proxy/freedom
go test -vet=off -race ./infra/conf -run AnyTLS -count=1
ANYTLS_SINGBOX=/absolute/path/to/sing-box ANYTLS_MIHOMO=/absolute/path/to/mihomo \
  ANYTLS_STRESS_DURATION=10m go test -race ./proxy/anytls -run '^TestExternalClients$' -count=1
go test -race ./proxy/anytls -run '^TestAnyTLSUoTResourceRounds$' -count=20
go test ./proxy/anytls/internal/engine -run '^$' -fuzz '^FuzzAuthenticationPrologue$' -fuzztime=60s -fuzzminimizetime=1s
go test ./proxy/anytls/internal/engine -run '^$' -fuzz '^FuzzServerFrames$' -fuzztime=60s -fuzzminimizetime=1s
go test ./proxy/anytls -run '^$' -fuzz '^FuzzPaddingValidation$' -fuzztime=60s -fuzzminimizetime=1s
go test ./proxy/anytls -run '^$' -fuzz '^FuzzUoTAdapter$' -fuzztime=60s -fuzzminimizetime=1s
```

`infra/conf` 的 `-vet=off` 仅用于绕开已在原始基线复现的 unreachable-code
vet 错误，不代表已修复它。Sidecar 工作目录 `../xray-sidecar`：

```sh
go vet ./...
go test -race ./...
```

本配置仓库：

```sh
python3 test/run.py
XRAY_ANYTLS_BINARY=build/xray-darwin python3 test/run.py tools
```

SHI 远端测试会修改临时运行时状态，不能把它当成只读检查；只在授权窗口按照
[pilot.md](pilot.md) 执行。不要对其他节点直接套用。

本机忽略目录 `.cache/anytls/`：`convergence-core-tests.log`、
`convergence-sidecar-tests.log`、`conf-convergence.log`、`auth-fuzz.log`、
`adapter-fuzz.log`、`distinct-client-stress.log`、`uot-loopback-repeat.log`、
`convergence-python-tests.log`、`final-tools-tests.log`、`final-core-build.log`、
`final-sidecar-build.log`。历史失败日志保留，不用成功重跑覆盖失败事实。

无未解决的 AnyTLS 身份绕过或重复计数失败；公开许可审查仍独立待办。
这不保证覆盖所有网络故障组合，也不把整库已知外网/基线失败归零。
