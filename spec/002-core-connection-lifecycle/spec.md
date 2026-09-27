# 连接生命周期

- Feature ID: 002-core-connection-lifecycle
- Status: Implemented（隔离分支补测、提交归并及本地构建完成；未部署）
- Authorized: 2026-09-27，用户授权分类重建、缺口补测与文档同步；主流程统一提交，不部署。
- Source: 原 Core e7a21974，相对 b4f08981；清单见 [001](../001-core-fork-contracts/inventory.md)。
- Scope: Dispatch pipe 所有权、显式关闭、响应 packet 与 Hy2 manager 锁边界。
- 原 001 FR-002、FR-004 保留为追溯编号。

## US-001 — 分类维护与独立验证（P1）

Given 原始分叉和受控输入，When 在隔离 worktree 重组并执行测试，
Then 保留现有运行时行为，以实际正负向结果验收，不以测试文件存在代替通过。

## FR-001 — 当前行为契约

- 返回的 net.Conn 持有双向 pipe；runner 提前返回不能触发抢先 EOF。
  调用者 Close 释放 pipe、解除阻塞；重复 Close 及与 Interrupt 并发已验证。
- borrowedReader 保留 TimeoutReader，返回真实 buf.ErrReadTimeout，并转发 Interrupt。
  阻塞读是否终止是断言，不以接口存在代替实际唤醒。
- DispatchConnOutputPacket **仅改变响应方向**：一个响应 buffer 对应一次
  足够大目标切片的 Read。net.Conn.Write 仍是流输入，不承诺双向数据报、
  任意大 packet 或短 Read 保留包余下字节。
- **Cancel 不是 Close**。通用 dialer redirect 使用 WithoutCancel；
  cnc 连接的 deadline 方法不提供实际超时。调用者取消场景必须显式 Close，
  测试以独立时限和 goroutine join 验证退出，不要求取消自动回收全部 pipe。
- proxySettings、DialSystem/dialerProxy、singbridge 共用上述所有权规则；
  三入口使用真实 loopback 链路和可观测的受控 outbound processor 验证。
- Hy2 manager 锁保护 map 查找/创建，setCtx 与客户端 clean 在其外执行；
  被持锁的客户端不阻止另一配置取得客户端。失败创建、认证拒绝、TLS 拒绝
  释放其原始连接/UDP socket，重复 manager clean 可用。
- 私有 client.close 依赖活动连接，不视作可在空状态反复调用的公共幂等 API。

实现定位：transport/dispatch_conn.go、common/singbridge/dialer.go、
app/proxyman/outbound/handler.go、transport/internet/hysteria/dialer.go。

## SC-001 — 验收标准与状态

历史重建基线的 tree 等同性与新增补测分别审计；新增测试不再要求最终 tree
逐字节等于原始 HEAD。当前变更不包含运行时实现修改。
[测试矩阵](tests.md) V-001 至 V-004 在上述边界内已实现并通过 Darwin race 验证。
正负向命令、退出码、失败试跑见
[证据报告](../001-core-fork-contracts/@@SPECMAP0@@)。

这不是任意协议、全部操作系统、生产 Hy2/NAT 或全局 FD 无泄漏认证。
/dev/fd 枚举在本机不可用；使用自有任务 join、连接关闭断言证明受测资源退出。

## 导航

[技术计划](plan.md) · [任务](tasks.md) · [检查清单](checklists/requirements.md)
