# Implementation Plan: AnyTLS inbound

- Spec: [spec.md](spec.md)
- Status: Implemented / 本地资源与功能验收完成；SHI 最终版本十分钟复验及清理通过，仅内部试点。

## 技术范围与文件归属

### 2026-09-26 会话回收修正

- AnyTLS 独立接入 `common/mux` 的双观测算法，不耦合其帧/Session 类型。
  每个认证完成的会话每 30 秒检查：前后 handler 数均为零，且累计创建流数
  未变化，才标记 retiring 并关闭；正常最后一个 handler 退出后 30-60 秒回收。
- 准入、退出、累计计数和 retiring 标记共用已有互斥锁。关闭 socket 在锁外；
  会话/流预算仅在真实 teardown 后归还，不能提前释放以绕过资源限制。
- 心跳继续应答，但不改变业务计数。保留活跃业务的用户 policy 超时。
  协议 FIN 不变：收到 FIN 不回发，符合参考实现。
- `maxSessionsPerUser` 省略或零表示不额外限制单用户，显式正数仍有效；
  入站总会话默认 256、每会话流上限 128、共享 handler 预算均保留。
  四台 RFC CO 配置移除显式 16，先在 SHI 验证，再扩展部署。

| 仓库 | 拟改位置 | 内容 |
|---|---|---|
| `.` (Core) | `proxy/anytls/` | 配置 proto、Account、入站、用户注册表、会话管理及测试 |
| `.` (Core) | `infra/conf/anytls.go`、`infra/conf/xray.go` | JSON 构建、校验、入站注册；不注册出站 |
| `.` (Core) | `main/distro/all/all.go` | 打包入站协议实现 |
| `.` (Core) | `common/singbridge/` 或协议内适配器 | 优先局部适配；共享改动必须证明现有协议不受影响 |
| `.` (Core) | `go.mod`、`go.sum` | 经选型确认后锁版本，保留许可通知 |
| `../xray-sidecar` | `usersync.go` 与相关 tests、协议映射、Core 依赖引用 | 账户 factory、类型发现、错误隔离；不新增 outbound |
| `../xray-config` | `test/`、适用文档、已有 xrayctl 检查路径 | 跨组件夹具、必要的协议识别测试和最终使用说明 |

默认不修改 router/dispatcher 的算法。如果适配时发现必须修改其公共行为，先更新
本计划、列出风险和回归范围，不把协议功能变成全局重构。

## 原则检查

| 原则 | 满足方式 |
|---|---|
| I 声明意图，保留事实 | research 锁定源码证据；Approved 不表示线上已支持 |
| II 本地权威，部署可追溯 | 仅本地实现与夹具；发布仍由 xrayctl 构建/验证/部署 |
| III 兼容先于清理 | 保持 SS-2022 与现有用户/统计契约；不开启即无行为变化 |
| IV 边界明确 | 密码认证、email 身份、逻辑流、TLS 与用户统计分层 |
| V 验证真实行为 | 外部客户端与独立出口，计数、撤销、UDP、压力分别验收 |
| VI 副作用可见 | 不自动开端口、签证书、改 DNS、切订阅或升级生产 |
| VII 渐进实现 | 先 TCP 静态多用户，再动态管理、UDP、Sidecar 和压力 |

## 架构

```text
Xray listener (RAW TCP + TLS，现有证书)
  -> AnyTLS 认证/帧/session 引擎
  -> 受控用户注册表 + 会话索引 + 资源预算
  -> 每条 stream 的独立 context
       -> TCP: Dispatcher -> 既有 outbounds
       -> UoT v2 解包: 按实际 UDP 目标 -> Dispatcher -> 既有 outbounds
  -> 既有 StatsService / DomainTraffic -> Sidecar -> 既有展示

Sidecar usersync -> HandlerService -> AnyTLS UserManager
```

协议库只承担线协议、padding 和流复用；用户权限、Xray context、资源预算、路由
和计数边界由适配层负责。外部客户端用于验收，本仓库不新增 AnyTLS 客户端实现。

## 实施阶段

1. **依赖决策**：闭环 D-001/D-002，先做隔离兼容实验，锁定版本与许可方案。
2. **TCP 可验收增量**：注册配置、TLS 入站、静态用户、独立流、路由、
   inbound/user/user+domain 三维上下行计数与控制面读取；
   用 sing-box/Mihomo 真实客户端跑双用户测试，不部署。
3. **动态身份**：UserManager、generation、严格撤销、并发控制及错误语义。
4. **UDP 完整性**：UoT v2 转接、逐目的地路由、报文与用户统计；不能静默降为 TCP。
5. **完整控制面与 Sidecar 联动**：真实 gRPC 入站添加/查询/删除、用户动态管理、
   proto 类型注册与解码、usersync 类型分派及混合协议同步；验证失败时无残留资源。
   Sidecar 读取运行时状态并正确识别 anytls，无需改数据库。
6. **收敛**：压力、模糊测试、泄漏、回归和本地完整验收；确定资源默认值。

## 构建和验证纪律

实际测试清单见 [tests.md](tests.md)。新增 Core 代码必须按项目
约束提交成可追溯构建输入，再从 `../xray-config` 执行
`scripts/xrayctl.py build core` 构建 Darwin/Linux；不得隐式切换批准的 Core 源分支。
不可因构建包装器拒绝 dirty 树而偷偷绕过。Sidecar 同样先 Darwin 执行，再 Linux。
实现和本地验收按已授权范围推进；每轮相关本地验收完成后才更新试点。

本地集成测试使用临时高位端口、临时 CA/证书、隔离配置和临时目录，不碰正式
本地 Xray、8080、LaunchAgent 或用户 SOCKS 端口。客户端二进制仅作测试，不部署
sing-box 到生产，不以外部 Xray release 替代 patched Core。

## 后续部署与回滚边界

用户已授权的试点为 `tk.rfc.co.micro.shi`，AnyTLS 监听 TCP 2083，Hy2 UDP 2083
和 SS-2022 TCP/UDP 2053 保持不变。本地客户端使用 sing-box；详细步骤和验收记录
见 [pilot.md](pilot.md)。部署前重查端口、SSH、证书、防火墙和现有服务，不自动抢占端口。
本地门槛通过后，按已授权范围先新增独立入站试点，
不替换旧入站；配置和二进制经 xrayctl 正常验证再发布，观察用户路由、计数和资源。

回滚时先停止该入站的动态同步写入，再部署移除 AnyTLS 入站的兼容配置，最后才能
回退不认识 AnyTLS protobuf 的 Core；必须保留旧接入链路。用户计数与域名历史
仍沿用现有重启恢复语义，不以回滚为由清空历史数据。

## 实际结果

AnyTLS 内部新增每入站 128 个 UoT 目标的共享准入限制，保留每 association
64 目标限制。worker 退出并同步 interrupt 双向 pipe 后释放额度；Dispatch
失败立即归还。TCP/UoT 的 pipe policy 均限制为最多 32 KiB，用户更小的
非负值保留，unlimited 同样被限制；不修改全局 policy 或其他协议。
这是 pipe 阈值和协议 handler 准入，不声称控制整个进程 RSS 或 outbound
异步清理完成时刻。容量证据与剩余闸门见 [resource-audit.md](resource-audit.md)。

Core/Sidecar 已有本地实现、真实 gRPC 和双客户端 TCP/UoT 测试。
详见 [implementation.md](implementation.md)，未完成项不视为通过。
公共 inbound manager 另有小范围变更：只有 Start 成功后才发布 handler，
创建失败由调用者 Close；这是 AT-030 无幽灵 handler 的必要修复，已覆盖
重复 tag 和独占 TCP 端口冲突测试。不修改 router/dispatcher 算法。
freedom 的默认私网阻断列表增加 AnyTLS，与现有代理入站一致；只有隔离夹具
显式允许 loopback，不改变其他协议或生产配置的地址策略。
