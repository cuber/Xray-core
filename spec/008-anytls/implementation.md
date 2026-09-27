# 本地实现与验收记录

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

更新时间：2026-09-26。状态：Implemented（仅内部试点）；SHI 最终版本已验收，未修改订阅或其他节点。
Core 基线 `1e0c6a30`，实现提交 `52a74fe3`，补充验收与修复提交 `187bf0ab`；
Sidecar 基线 `95d1e30`，实现提交 `18ade47`，恢复测试及 pin 提交 `663362e`。
以上仅本地提交，未 push。当前构建为 Core `187bf0ab` / Sidecar `663362e`。
用户要求后续改动使用 amend，以上验收提交不再新增平行提交。
逐项验收映射见 [acceptance.md](acceptance.md)，资源上限见 [resource-audit.md](resource-audit.md)。

## 已实现

- 原生 JSON -> ServerConfig/User/Account；仅 RAW TCP+TLS inbound，空用户表可运行。
- 用户唯一性、不可变账户 generation、撤销断流、会话/流预算、独立路由上下文。
- TCP 与 UoT v2 经现有 Dispatcher；复用 inbound/user/domain 三维统计。
- 真实 HandlerService 入站和用户生命周期，失败 AddInbound 不发布幽灵 handler。
- Sidecar 每轮同步读取入站类型、构造正确 Account、预检重复密码、保护静态用户。
- AnyTLS 公开用户展示只读运行时；不复活启动文件中的已删除用户、不泄露密码。
- Core Go 格式门禁：CI 和本地 build wrapper 均检查新改动；不是整库格式清理。

## 已执行通过

| 范围 | 证据 |
|---|---|
| 配置、认证、原生 gRPC 生命周期 | `proxy/anytls/*_test.go`、`infra/conf/anytls_test.go`；相关包 `go test -race` 通过 |
| 多用户/多域名 marker 和统计 | `TestAnyTLSControlRoutingStatistics`，动态添加/删除/换密钥及统计精确值 |
| 单 TLS 会话 100 条流 | `TestAnyTLSSingleSession100StreamsAndRevocation`，A/B 出口、关闭半数及用户撤销 |
| 固定大流量 | `TestAnyTLSExactLargePayload`，1 MiB 上行、2 MiB 下行精确计数，race 连续 5 次通过 |
| inbound 计数边界 | `TestAnyTLSInboundCounterBoundary`，客户端 TLS 解密后的计数与 Core 受管连接一致，race 连续 5 次通过 |
| UoT | 1/512/8192 字节回显与精确用户计数；0/8193/65507 字节明确拒绝测试通过 |
| 回收 | `TestServerReleasesStreamReservations`，100 轮各 32 条截断握手流，结束后准入计数归零 |
| 实际连接/FD 回收 | `TestAnyTLSRepeatedConnectionsAndListenerClose`，100 轮真实 TLS 连接/关闭，连续三轮均 FD 11 -> 10 |
| UDP 多目标 | `TestAnyTLSUoTIndependentIPv4IPv6Targets`，域名/IPv6 目标交替进入不同 IPv4/IPv6 回显出口，marker A/B 正确 |
| 网络准入 | 用户会话配额、其他用户不受影响、认证 deadline 的真实 TLS 测试连续三轮通过 |
| 帧 fuzz | 定时器修复后 60 秒、3,899,994 个输入，通过；单次缩减上限 1 秒 |
| padding fuzz | 60 秒、1,332,254 个输入，通过 |
| Sidecar | 全量 `go test -race ./...`；真实嵌入 Core 的同步及 debug 脱敏集成测试通过 |
| 独立客户端 | sing-box 1.14.2 与 Mihomo 1.19.31 的 TCP/UoT 回显；均验证临时 CA/SNI |
| 长压测 | sing-box/Mihomo 各 50 条流、共享一个 Core、持续 10 分钟，race 通过；最终定时器修复版本日志 `stress-deadline-fix.log` |
| 同端口/同用户回归 | 独立 sing-box 同时连接 Hy2 UDP 和 AnyTLS TCP；SS-2022/AnyTLS 同 email 计数精确汇总 |
| Core 重启恢复 | Sidecar 真实 gRPC 测试：API 用户不持久化，重启后从期望状态重新同步 |
| 正式构建入口 | `xrayctl build core` / `sidecar build`，Darwin 实际执行、Linux amd64 产物成功，VCS metadata 对齐提交 |
| 配置二进制验证 | `XRAY_ANYTLS_BINARY=build/xray-darwin python3 test/tools/test_anytls_binary.py`，合法证书/空用户通过，四种非法配置拒绝 |
| CLI 回归 | `python3 test/run.py xrayctl`，200 项通过 |
| 配置仓库最终回归 | 全量 252 项通过（1 项 opt-in skipped，随后带 `XRAY_ANYTLS_BINARY` 单独通过）；pilot 夹具 5 项离线单测通过 |
| 最终 Core 相关回归 | `core-pilot-final-tests.log`，AnyTLS/engine/CLI race 通过，同时提供两个独立客户端，不以缺工具 skip |
| SHI 单节点历史基线 | Core cb7410ca / Sidecar 688d9b6，双用户、TLS、UoT DNS、三维统计、撤销、完整十分钟低负载及清理通过；最终版本复验见 pilot.md |

客户端保存在 ignored `.cache/anytls/tools/`，不是运行时依赖。
本地日志保存在 ignored `.cache/anytls/`，测试账户不是生产账户。

客户端 SHA-256：

- sing-box：`d652879eed7e38b866fa980bc2e119dcdb13968fe5e6583670293a258686d0e7`
- Mihomo：`fae1f37e28ee53fcf5be7a8bb121099db1fe442e44205734ed49c62579364090`

当前 Core Darwin：`fb35303843364b12ad7f3feea31fa11f4b615342baa278ba28cf974919b0486b`。
当前 Core Linux：`fcd0ff5abee45fbe727df9419a110e572e04154b36d2f27855a40c1781242377`。
当前 Sidecar Darwin：`d44d968bf9374319a5ccdf8f7c2cf80004cec99247fb851d0d8d6468d9701786`。
当前 Sidecar Linux：`1dcdce947543e31a975a406395690906a20438d212b5d6fbeae6cc56ca8d88ee`。

定时器修复后的长压测再次通过，日志 `stress-deadline-fix.log`；100 条流运行
10 分钟，goroutine 稳定约 934。最终帧 fuzz 60 秒 3,899,994 次输入通过。
这是包含测试夹具的进程，不是生产 Core RSS。

新增验收：

- `TestRevocationAdmissionBarrier`：100 个并发准入与删除交错，删除返回后全部拒绝，
  重加账户不复活旧 generation。认证查表和会话登记同锁，不存在解锁后的中间窗口。
- `TestSlowReaderBackpressureAndClose`、`TestClosedStreamRejectsDeadlineRearming`：
  单 pending 缓冲背压，关闭释放等待者及定时器；三轮 race 通过。
- `TestAnyTLSResourceRounds`：预热加三轮 100 条流，整套连续三次 race 通过；
  关闭后 FD=4、goroutine=3，稳定堆约 7.6 MB，无线性增长。
- `TestVLESSAfterAnyTLSRemoval`：真实 sing-box VLESS 回归连续三次通过。
- `TestAnyTLSCertificateAndAccessLog`：实际 TLS 与 API 证书一致；domain:user +
  inbound/network 路由正确；日志含身份/目标/tag，不含密码、摘要或私钥。
- `TestAnyTLSPaddingAndIPAccounting`：默认/4 KiB padding 不改变用户 payload；
  纯 IP 归入现有 unknown 桶，不伪造域名，三轮 race 通过。
- `TestExtractAnyTLSInboundUsers`：补齐 Core 原生 `api adu` 的 AnyTLS 识别。

## 发现与处理

1. 默认 fuzz 缩减耗时可能看起来像停滞；保存输入重放无阻塞，限制每次缩减
   1 秒后完整 60 秒通过。人工 SIGQUIT 的一次诊断运行不算通过证据。
2. `app/proxyman/outbound.TestTagsCache` 的 race 在原始 `1e0c6a30` detached
   worktree 也复现；未借本任务修改 outbound manager。
3. 首轮压力夹具并行创建两个 Core，争用全局 SystemDialer；已改为一个 Core、
   两个用户各 50 条流。不把多实例 race 当作 AnyTLS 协议通过证据。
4. 实际 Core listener 启用 SO_REUSEPORT；冲突测试使用独占 `net.Listen` 验证
   错误清理，生产仍必须检查 TCP 端口空闲，不能靠 bind 失败防止误共用。
5. 实际发现并修复：缺少 SYNACK 使 sing-box 复用会话的开流 watchdog 在约 3 秒
   后断开一条流；原压力夹具只在 10 分钟结束才汇报错误，已增加带时点的诊断。
   修复后双客户端 5 秒压力连续三轮通过；100 流测试强制检查每条 SYNACK。
6. 实际发现并修复：会话结束没有立即取消 dispatcher 流，快速重连时累计到
   用户会话上限；SessionClosed 取消父 context，100 轮真实连接回收验证通过。
7. AnyTLS 对齐其他代理入站的 freedom 默认私网阻断；仅隔离夹具用明确的
   `ipsBlocked: []` 放行回环目标，生产不会隐式放开私网。
8. 全 Core 测试不能报告全绿：既有 DNS/ECH 外网测试失败，ECH 错误已在原始
   基线上复现；`infra/conf` 的 unreachable-code vet 错误也在基线复现。
   相关改动包 race 通过、Sidecar 全量 race/vet 通过；不隐去全量失败。
9. 三轮资源测试发现 stream deadline 定时器在关闭后仍持有会话，修复为所有关闭
   路径取消计时器且拒绝重新设置。修复前每轮约增 3 MB，修复后预热趋稳。
10. SHI 先升级 Sidecar 后升级 Core，暴露旧 Core 的 Unimplemented 被永久缓存。
    Sidecar 改为一分钟后重新探测；真实 gRPC 测试验证抑制重复调用、失败退避和
    能力恢复，race 连续十次通过。全量 Sidecar race 首次在既有 Egress 100ms
    HTTP 超时测试偶发失败，未改该用例，完整重跑通过；日志 `sidecar-retry-tests.log`。
11. 远端夹具的原生 `api adu` 必须带 RAW/TLS streamSettings，不能因只添加账户
    而省略完整入站校验。首次失败清理通过，补齐后动态添加与撤销均成功。

12. 原生 gRPC AddInbound 可绕过 JSON 证书检查，TLS 工厂跳过坏私钥后仍可能
    发布监听。AnyTLS 构造时校验服务端证书/私钥并拒绝无有效证书；测试先复现失败，
    修复后验证无 handler/端口泄漏，其他协议 TLS 语义未改。

## 最终本地收敛

- 真实多用户 UoT 128 目标饱和、129 拒绝、撤销后额度完整恢复、另一用户 TCP/
  已有 UDP 不受影响；显式 512 KiB policy。20 次重复共 80 轮回收通过。
- 双独立客户端各 50 流、不同出口 marker、十分钟、逐用户精确增量通过，
  日志 `distinct-client-stress.log`。慢读/无限 buffer policy 被局部上限约束。
- 认证、帧、padding、UoT 四类 fuzz 分别至少 60 秒通过；认证 fuzz
  `auth-fuzz.log` 为 18,236,379 个输入，另做 seed corpus race 三轮。
- 原生 gRPC 坏证书/非法设置、空用户认证、REALITY 拒绝、RST 配额归还、
  user-domain 路由和 HTTP Host 嗅探均补齐。统计不改变既有 unknown 边界。
- Sidecar 八种统计开关、真实 AnyTLS 快照和 Core 重启/epoch 合并、API-only
  入站带存活用户重启后清除并重同步，完整 race/vet 通过。
- Core 相关包 race 及配置测试通过；全量 Python 252 项通过，显式 Darwin
  AnyTLS binary 测试通过。已有 Core 基线失败仍如上记录，不宣称整库全绿。
- `xrayctl build core` / `sidecar build` 完成四个产物；SHI 实际证书本地镜像
  run -test、Darwin Sidecar -test、远端两组件 -test 均通过，已更新到上列版本。
  最终十分钟远端复验及清理通过，结果记录在 pilot.md。

## 整库回归收敛（2026-09-26）

上面的全量失败是试点时的历史结果，不作为永久豁免。后续修复包括：

- DNS TCP/DoH/DoQ、系统 resolver、ECH、REALITY 和域名嗅探使用本地真实协议
  夹具，证书使用独立信任根且仍校验；公网集成保留为显式 `TestPublic*`。
- 更正此前归因：`TestLocalDomain` 是本地 UDP 测试，不是外网测试；修复端口
  预留/监听就绪、关闭回收及反向耗时断言，缓存验证等待实际缓存写入。
- GeoIP 测试使用明确的本地 CIDR 数据，不再假设随版本变化的公共归属库内容。
- 修复 outbound 标签缓存替换、DNS 共享 IP 切片、XHTTP 响应体发布/关闭和
  上传缓冲所有权交接的竞态，以及测试夹具自身的并发访问问题。
- 清理 unreachable code、丢弃 cancel、不安全 uintptr 中转和 protobuf 锁复制
  的 vet 报错；AnyTLS 资源测试在原有时限/内存上限内等待异步回收，不提高上限。
- 增加 `XRAY_TEST_NETWORK`、`XRAY_TEST_PROXY`、`XRAY_TEST_UDP_PROXY` 和
  `XRAY_TEST_ECH_DNS`；代理失败不隐式直连。SOCKS 域名转发和 UDP 拒绝有本地测试。

测试命令及网络边界见 [Core testing README](../../testing/README.md)。
当前本地 Core 为 `8a400cf3`，Sidecar pin 同步为 `73b9b15`；这些不是远端部署版本。
Darwin/Linux Core 构建已通过，Sidecar 全量测试已通过。
Core `go test ./... -count=1 -timeout=15m` 全量通过，场景包耗时 270.856 秒；
最终提交整库 `go test -race ./... -count=1 -timeout=15m` 通过，场景包耗时
280.453 秒；静态检查 `go vet ./...` 和 gofmt 门禁均通过。
最终提交补测 DNS/TLS/代理夹具 race、XHTTP/DNS/VLESS 修改包 race 均通过。
本机原始日志保存在 `.cache/anytls/regression-20260926/`，不进入 Git。
Surge `127.0.0.1:1080` 下公网 TCP DNS、DoH（含 A/AAAA）及实际 ECH 接受验证
普通/race 两轮通过，Sidecar vet 同样通过。
公网双栈目标改为已确认有 AAAA 的 `cloudflare.com`，未删除 IPv6 断言。
Surge 拒绝 UDP ASSOCIATE（code 7），直连及本地 Xray UDP 出口 DoQ 亦超时；
公网 DoQ 仍明确未通过，不能把默认本地协议回归通过描述成所有公网测试通过。

## 发布边界

公共分发许可方案仍未闭环；保留 GPL 来源不等于宣布许可问题已全部解决。
2026-09-26 用户已接受该引擎用于内部试点，公开分发另行处理。
当前不 push 实现或公开发布二进制，不修改订阅。
后续用户明确授权 SHI/ZDS 两节点内部部署，用户和路由对齐标准 Hy2；
空账户/默认拒绝属于此前隔离试点状态，当前范围见 [pilot.md](pilot.md)。
