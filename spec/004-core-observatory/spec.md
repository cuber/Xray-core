# Observatory

- Feature ID: 004-core-observatory
- Status: Implemented（核心补测、窄修复、提交归并及本地构建完成；未部署）
- Authorized: 2026-09-27，用户授权历史重建、隔离 worktree 补齐 V-010 至 V-014、窄修复及本目录文档同步；未授权部署。
- Source: 历史基线为原 Core e7a21974，相对 b4f08981；见 [001 清单](../001-core-fork-contracts/inventory.md)。
- Scope: 独立探测组、URL 重载、健康恢复、快照和调度停止；不吸收上游、不改原 core 或生产。
- 原 001 FR-005 与 V-010 至 V-014 保持稳定。当前结果见 [执行证据](../001-core-fork-contracts/@@SPECMAP0@@)。

## US-001 — 分类维护与独立验证（P1）

Given 历史分叉和隔离 worktree 中的受控输入，When 重建后补齐测试并修复暴露的缺陷，
Then 分别记录历史一致性、当前实际运行时变化和验证边界，不以测试文件存在替代验收。

## FR-001 — 当前实现契约

- pingGroups 独立配置 selector、destination、interval、timeout、sampling、
  httpMethod、keepAlive；旧 subjectSelector/pingConfig 保持可用。
- 跨组相同或前缀覆盖 selector 拒绝，同组完全重复拒绝；同组嵌套允许。
  旧空 selector 是 no-op，不解析标签、不启动探测。
- destinationsByPrefix 使用最长前缀，未命中回默认 URL；query 原样保留。
  ob 参数仅属于 URL，Core 不解释其 egress 含义。
- keepAlive 缺省 false；true 允许同一 transport 复用，不保证服务端保活或跨轮复用。
- 主探测 HTTP 成功必须是 204，不追随重定向；200、302、503、超时有真实 HTTP 负向测试。
- 空样本不健康，初始成功即健康，失败立即不健康；恢复需 min(3,容量) 次连续成功。
  恢复中失败重置计数；过期缓存不沿用健康，长空档后重新恢复。
- WalkResults 锁内取快照、锁外回调；阻塞或重入回调不阻塞结果写入，快照修改不污染内部。
- Start/Stop 串行化；Stop 取消并等待本次调度的在途请求、延迟采样及受管理 worker。
  取消和调度结果发布共用结果锁，取消屏障之后不发布该调度的样本。
  公共 PutResult 和无关的一次性 Check 不因此被全局禁用。
- selector 必须并发安全、及时返回并释放自身资源。旧签名不支持传入取消 context，
  Stop 取消等待 selector，不强制终止其函数；违规阻塞回调可能在 Stop 后继续存活。
- 3s interval / 2s timeout / 20 sampling 在 JSON 构建与运行时保持原值。
  interval < 10 的 duration 数值比较本轮未改；不承诺精确每三秒发包。

## SC-001 — 验收与状态

V-010 至 V-014 的核心 harness 已实现并在四包完整 race 及新增测试十次重复 race 中通过。
本类别新增 11 个顶层测试，具体命令和断言见 [测试矩阵](tests.md)。

历史重建阶段的累计 tree 一致性保留在 [重建记录](../001-core-fork-contracts/reconstruction.md)；
本轮增加实际取消修复及测试，当前累计 tree 不再要求等于原历史 HEAD，差异须由父进程审阅归并。
不能将本状态解释为已提交、已发布或全仓验收完成。

## 剩余边界

- HTTP harness 使用 loopback tagged dialer，不覆盖生产 dispatch/远端链路。
- 过期测试控制样本时间戳，不改系统时间；未验证有效期纳秒级相等边界。
- selector 资源由回调方负责，不能把取消等待写成强制回收任意回调。
- 全仓测试及 Darwin/Linux 发布构建不在本轮通过声明内；父进程统一隔离 clone 构建，
  本 worker 不再尝试 wrapper。已有 worktree wrapper 拒绝记录保留在证据中。

## 导航

[技术计划](plan.md) · [任务](tasks.md) · [检查清单](checklists/requirements.md)
