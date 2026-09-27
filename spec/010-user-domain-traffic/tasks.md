# 用户域名流量任务

- [x] T001 [US-001] 从 005 提取 FR-001 契约和 V-019 至 V-027 测试矩阵。
- [x] T002 [US-001] 对照重建提交检查内容与依赖，编译并执行现有定向测试。
- [x] T003 [US-001] 记录差距和最终 tree 对比；待补验收不伪报通过。

状态及执行证据见 [005 重建记录](../005-core-fork-contracts/reconstruction.md)。

- [x] T004 [US-001] tests.md 各编号补齐独立复验流程，现有命令对照源码核名。
- [x] T005 [US-001] V-019 至 V-027 定向场景及负向对照已实现/执行；证据见 005 evidence-010-core/sidecar.md。
- [x] T006 [US-001] 按追加授权将 Core/Sidecar LRU 统一为直接删明细，不搬 other；完整 Stats 不变。
- [x] T007 [US-001] 修复回拨/cursor 溢出、sniff data+error 缓存、快照校验；完成三次定向 race 与临时 modfile 联测。
- [x] T008 [US-001] 记录子进程/故障注入/heap证据的覆盖边界，以及 build wrapper 拒绝 linked worktree 的真实结果。
- [x] T009 [US-001] 父进程完成最终全量测试、双平台构建条件核验及每仓库统一规格提交；结果见 005/acceptance.md，未部署。
