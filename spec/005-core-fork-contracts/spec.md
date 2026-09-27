# Feature Specification: Core 分叉契约与回归验收

- Feature ID: 005-core-fork-contracts
- Created: 2026-09-27
- Status: Implemented（每 spec 一笔 Core 提交、补测及隔离验收完成；未切换 develop 或部署）
- Baseline: xray-core `b4f08981becb71eaa995fa98ed2098ade92566bb` (v26.4.25)
  → `e7a2197424614e47d3e728da1d9cdded3454f559` (develop)，21 个提交。
- Implementation status: 分类重建与本轮授权补测完成；提交映射见
  [reconstruction.md](reconstruction.md)，实际验收及限制见 [acceptance.md](acceptance.md)。
- Authorization: 2026-09-27 用户同意分类整理、在隔离分支重建提交并备份原分支。
  不吸收上游、不改生产；暂不覆盖 develop、不强推远端。

阅读顺序：[提交与文件清单](inventory.md) → [契约](contracts.md) →
[验证矩阵](tests.md) → [计划](plan.md) → [任务](tasks.md) →
[检查清单](checklists/requirements.md)。

本次重建的备份、提交映射、依赖和实际验证见 [重建记录](reconstruction.md)。
独立复验的环境、证据与清理要求见 [复验规程](test-procedure.md)，
V-001 至 V-033 的详细步骤在各类别 tests.md；追加场景不等于已实现。

## 问题与边界

提交标题不能充当功能清单：AnyTLS 的合并提交还包含 DNS、VLESS、XHTTP、
管理器并发和测试基础设施改动。升级上游前必须有逐项可观察的契约。

AnyTLS 协议本身继续由 [012](../012-anytls-inbound/spec.md) 唯一负责，
不复制其身份、UoT、资源限额和许可证契约。005 负责清单、依赖和跨功能回归索引，
详细契约已拆至 006–011，由 contracts.md 导航。DomainTraffic 以 010 为准，旧 docs 保留
历史验收证据，不再把旧分钟粒度当成当前要求。

## 用户场景与验收

### US-001 — 可定位每一项分叉改动（P1）
Given 固定 base/head，When 枚举所有提交及累计文件差异，
Then 每笔提交、每个文件都能定位到 012 或本规格的契约/验证模块。
独立验收：集合比较，无需启动服务。

### US-002 — 保持代理与调度行为（P1）
Given 本地回显端点及受控健康样本，When 经过 dispatch pipe、UDS、
Hy2 manager、OB 和 weightedLeastPing，Then 首包不丢失、取消可退出、
失败恢复及选路比例符合 contracts.md，而不是只验证配置可解析。
独立验收：本机隔离测试，不依赖真实节点或公网。

### US-003 — 保持控制面和统计一致性（P1）
Given 两个认证身份与相同域名，When 传输固定字节、读取规则和统计、
推进时钟并重启采集端，Then 身份隔离、未淘汰明细精确、cursor 幂等且无错误归因。
域名统计是有界尽力缓存；淘汰允许丢失尚未采集的明细，不影响普通流量计数器。
独立验收：受控 Core/Sidecar 实例及原始 RPC，不依赖 Portal 截图。

### US-004 — 回归结果可信（P1）
Given 并发和故障注入场景，When 执行默认离线回归及可选公网测试，
Then 记录实际通过、失败和跳过；不能把未启用公网测试算成公网通过。
独立验收：测试日志、退出码、版本、资源清理证据。

## 功能需求

- FR-001: 完整覆盖 21 个提交及累计 diff，新增文件和生成文件也不得漏记。
- FR-002: Dispatch-backed pipe 的所有权、TimeoutReader、Interrupt 和返回时序保持。
- FR-003: Freedom Unix redirect、通用 Unix dial、认证 SOCKS UDS 与多 IP 监听保持；
  监听部分失败必须释放已绑定资源，旧单地址配置不回归。
- FR-004: Hy2 manager 不在持全局锁时执行可能阻塞的单客户端操作。
- FR-005: OB 分组、前缀 URL、keepAlive、204 判定、失败即死和恢复迟滞保持。
- FR-006: weightedLeastPing 按健康池执行平滑加权选择；全不健康回到所有正权重候选；
  权重约束的是选路次数，不是传输字节。leastLoad tolerance 另行保持。
- FR-007: user-domain 匹配及 ListRule 详细规则支持静态/动态配置，返回对象不得改写内部状态。
- FR-008: DomainTraffic 可选开启，用户×域名、字节口径、LRU、窗口、cursor 和饱和计数保持。
- FR-009: 与 Sidecar 的十分钟聚合、快照恢复、跨 boot 合并及匿名历史保持兼容。
- FR-010: 通用 DNS/XHTTP/VLESS/管理器修复独立验收，不能由 AnyTLS 单测替代。
- FR-011: 默认回归用本地可控 fixture；公网测试显式开启、代理失败不得静默直连；
  TLS 验证不得为了测试全绿而关闭；格式检查不修改源文件。
- FR-012: proto 字段/编号、账户注册、依赖升级及既有协议回归必须有跨组件兼容检查。

## 非目标

- 不吸收 origin/main、不替换 ipsBlocked、不改变 3s 探测或任何生产策略。
- 不重新实现 AnyTLS，不新增协议、API 或 xrayctl 命令。
- 不将历史部署说明当成这次验证过的线上状态。
- 不承诺 LRU 淘汰后仍可还原每个人的全部历史域名。

## 完成标准

- SC-001: 提交、文件清单与固定 base/head 完全一致，遗漏集合为空。
- SC-002: FR-002 至 FR-012 都有正向、负向、边界、并发/重启场景及独立 oracle。
- SC-003: 已有测试与待补测试明确分开；没有仅凭测试函数存在就标记 PASS。
- SC-004: 实现与契约的差距记录为阻塞项；后续验证附版本、命令、退出码、证据路径。
- SC-005: docs/ 和模块入口可逐级发现本规格，无秘密和断链。

## 当前审计发现

1. Core 默认容量现为 4096 个用户×域名组合，Sidecar 默认 8192；
   旧设计中的 512/4096 不是当前默认值，显式配置值仍需单独核对。
2. 用户已确认 DomainTraffic 淘汰直接删除明细，不转移到 other；Sidecar
   已采集数据独立保留，不按同一 sequence 回溯修正。不同采集时机的缓存
   明细可以不同，V-025 验证这种明确接受的行为，不再强求淘汰前后守恒。
3. Core 同域名不同用户的排序只按 domain；不能承诺同值稳定次序。
4. AnyTLS 的当前提交带有其他运行时修复，必须独立回归。

补测和必要修复已获用户授权，具体结果须记录到对应 spec 的证据，不以审计发现
代替验证通过。生产配置、远端分支及部署仍不在本阶段范围。

## 修订

- 2026-09-27: 回溯规格草案建立；现存实现不等于新增验收完成。
- 2026-09-27: 用户批准分类与历史重建；原 HEAD 保存在备份分支和 annotated tag。
- 2026-09-27: 用户要求每个 spec 一笔提交、补齐测试并允许并行；确认 LRU
  尽力缓存契约，取消淘汰到 other 的补账行为。
