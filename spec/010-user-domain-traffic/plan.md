# 用户域名流量实施计划

1. 按 [spec.md](spec.md) 固定现有契约，与原提交逐文件对应。
2. 在独立 worktree 重组功能和测试；proto 源与生成文件同笔。
3. 编译各提交测试包，执行 [tests.md](tests.md) 中已有定向测试和最终完整回归。
4. 新旧最终 tree 必须完全一致；缺口测试或缺陷修复另立后续提交。

依赖与提交映射统一记录在 [005 重建记录](../005-core-fork-contracts/reconstruction.md)。
回滚使用原备份分支/tag；不修改远端、Sidecar pin 或已部署版本记录。
本类别遵守 constitution 的事实、兼容、可追溯和隔离验证约束；无生产副作用。

## 2026-09-27 追加授权阶段

历史重建步骤保持原记录；以下是用户另行授权的修复，不冒充 tree-equal 重建。

1. Core 与 Sidecar 容量淘汰直接删除明细，兼容读旧 other，普通 Stats 不变。
2. 补齐 V-019 至 V-027 的测试；修复回拨/cursor 溢出及 data+error 嗅探缓存边界。
3. Sidecar snapshot 校验保持原子失败；真实子进程 kill/restart 仅操作临时路径与测试 PID。
4. 分别做定向 race，并用临时 modfile 指向 xray-core-spec 跨消费者验证，不改版本 pin。
5. 更新本规格与 005 evidence；由父流程统一全量回归及每仓库规格提交，不部署。
