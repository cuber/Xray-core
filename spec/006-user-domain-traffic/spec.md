# 用户域名流量

- Feature ID: 006-user-domain-traffic
- Status: Implemented（有界尽力缓存、快照补测及 Core/Sidecar 本地验收完成；未部署）
- Authorized: 2026-09-27，用户同意分类整理、重建历史并备份原分支；随后明确授权
  Core/Sidecar LRU 直接删除明细、不搬 other，以及 V-019 至 V-027 补测和窄范围修复。
- Source: 原 Core e7a21974，相对 b4f08981；完整清单见 [001](../001-core-fork-contracts/inventory.md)。
- Scope: Core 有界尽力采集、用户归因、桶和增量 cursor/RPC。不吸收上游、不改生产、
  不修改普通完整 Stats 计数、不改 RPC/schema/version pin。主流程统一提交，不部署。
- 原 001 FR-008 由本规格负责；FR-009 的消费者聚合与恢复移交
  [Sidecar spec007](../../../xray-sidecar-rebase-20261014/spec/007-domain-traffic-aggregation/spec.md)。
  旧编号与测试证据不变，本次仅拆分文档所有权。

## US-001 — 分类维护与独立验证（P1）

Given 原始分叉和受控测试输入，When 重组本类别提交并执行测试，
Then 按用户确认的以下契约验证，保留历史重建证据与后续修复的区别，不以文件存在代替验收通过。

## FR-001 — 当前行为契约

- 与 StatsService 独立开关：关闭域名统计不关闭用户/入站/出站普通计数。
- Core 默认 5s 桶、300s 保留、4096 组合；身份键是 user + 分隔符 + domain，
  用户取认证 email，不反推出中继前的最终用户。显式旧 maxDomains 不自动扩大。
- domain 去首尾空白、末尾点并转小写；归因依次取最终 outbound 的
  Target、RouteTarget、OriginalTarget 中第一个域名，否则 unknown。
- 无用户的 unknown 使用特殊桶；有用户的 unknown 是普通组合，会占容量并被淘汰。
  other 是兼容旧 Core/旧快照的匿名汇总，不保留可恢复用户身份，不因新淘汰而增加。
- 上行按 dispatcher 消费的 MultiBuffer 长度；下行按交给 writer 的长度，
  在下游写入结果返回前计数。不是 TCP ACK 成功送达字节，也不包含外层协议开销。
  Reader 的带数据+错误返回仍统计已有数据；嗅探缓存保留该载荷供后续重放，
  同时返回原错误；缓存重放不再次通过计数 Reader。避免 Reader/Writer 双计数。
- Core 是按组合跨桶全局 LRU 的有界尽力缓存，容量淘汰直接删除该组合
  仍保留桶内明细，不搬 other；自然过期直接丢弃。相同 lastSeen 平局不指定牺牲者，
  但必须满足组合预算。未采集即遭 Core 淘汰的数据允许丢失；RPC 不发送撤销通知，
  不维护消费者确认状态。消费者的独立保留策略不属于 Core 的内存预算。
- Record/Snapshot 时惰性裁剪，不存在“每五分钟清空全部”的语义。
  边界桶 start == cutoff 保留，可能有当前桶加 60 个历史桶。
- 仅返回已结束桶，sequence 在有记录的新桶建立时分配，空闲时间不生成空桶。
  同 boot 的 afterSequence 过滤；异 boot 返回当前可用桶；过旧 cursor 标记 gap。
  LatestSequence 可能包含尚未结束的桶，调用方必须以实际接收桶推进 cursor。
  LRU 可令已发布桶变为空桶，保留其 sequence 直到自然过期；不是撤销通知。
  Core 对 Record/Snapshot 使用非递减逻辑墙钟：回拨时钳制到上次观察时间，
  不重开已发布桶，不恢复过期桶；墙钟追上前当前逻辑桶可能延迟闭合。
  未来 cursor（包括 MaxUint64）不因加法溢出伪报 gap。
- uint64 加法饱和，不回绕。RPC 走现有 StatsService.GetDomainTrafficBuckets。
- 消费者必须区分 Core boot，不能把 LatestSequence 当成已收到桶的确认。
  Sidecar 的幂等应用、跨 boot 合并、十分钟/24h 聚合和原子快照以 Sidecar spec007 为准。
- V-025 明确验收：Core 容量 1，先 alice/A=100/200，再 bob/B=30/40；
  早采集 Sidecar 保留 130/240，晚采集只有 30/40，两者 other 均为 0。
  普通 Stats 不受影响。闭合桶不承诺身份明细不可变。

## SC-001 — 验收标准

历史重组后的累计文件树等于原始 HEAD 是已完成历史阶段的要求；本轮授权修复不再
要求与原树相等。[测试矩阵](tests.md) 记录正/负向实测，证据区分原 Core 与新 worktree。
定向 race、临时跨仓库联测不代替父进程的最终全量测试或正式构建；不宣称生产已变更。

## 导航

[技术计划](plan.md) · [任务](tasks.md) · [检查清单](checklists/requirements.md)

## 所有权与历史编号

| 原追溯项 | 当前权威 |
|---|---|
| 001 FR-008、V-019 至 V-023 | 本规格：Core 采样、字节、归因、LRU、时间 |
| V-024 / V-025 提供方 | 本规格：分页、boot/gap、淘汰前后 RPC 可见性 |
| 001 FR-009、V-024 / V-025 消费方、V-026 / V-027 | Sidecar spec007：聚合、预算、snapshot 与恢复 |

原跨仓矩阵作为 [2026-09-27 历史验证](history/validation-20260927.md) 保留，
不再作为 Sidecar 当前契约的第二份可编辑副本。未更改实现、pin 或部署状态。
