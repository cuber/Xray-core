# Observatory 任务

- [x] T001 [US-001] 从 005 提取 FR-001 契约和 V-010 至 V-014 编号。
- [x] T002 [US-001] 历史阶段检查重建内容与依赖，编译并执行原有定向测试。
- [x] T003 [US-001] 保留历史 tree 对比和差距记录；与本轮新增修复明确分开。
- [x] T004 [US-001] 更新 tests.md 的可执行复验命令、逐项断言与证据链接。
- [x] T005 [US-001] 实现并执行 V-010 至 V-014 核心 harness；真实 HTTP、缓存恢复、
  快照、Stop、3s/2s/20 与 JSON 独立设置均覆盖。
- [x] T006 [US-001] 复现并窄修 Stop 缺陷；绑定 context、取消/发布锁屏障和 worker 回收，
  测试重复 Start/Stop 与阻塞 selector，声明无法强制终止回调的约束。
- [x] T007 [US-001] 完成四包 race、新增测试十次重复 race、gofmt；记录精确命令和结果。
- [x] T008 [US-001] 同步本目录 spec/plan/tasks/tests/checklist，保留外部链路、
  过期等值边界和 selector 资源所有权限制。
- [x] T009 [US-001] 父进程审阅、按 008 归并并统一隔离 clone wrapper 构建；
  双平台构建及全仓验证结果见 [最终验收](../005-core-fork-contracts/acceptance.md)。

本类别核心补测和修复已完成，尚未声明整个发布阶段完成。
历史记录见 [reconstruction](../005-core-fork-contracts/reconstruction.md)；
本轮证据见 [evidence-008-009](../005-core-fork-contracts/evidence-008-009.md)。
