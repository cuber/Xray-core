# 路由与调度任务

- [x] T001 [US-001] 从 001 提取 FR-001 契约和 V-015 至 V-018 编号。
- [x] T002 [US-001] 历史阶段检查重建内容与依赖，编译并执行原有定向测试。
- [x] T003 [US-001] 保留历史 tree 对比和差距记录，与本轮实际修复分开。
- [x] T004 [US-001] 更新 tests.md 可执行复验命令、独立断言和证据链接。
- [x] T005 [US-001] 实现并执行 V-015 至 V-018 核心补测；600 精确分布、
  阈值矩阵、user/leastLoad、真实 gRPC 增删替换及并发已完成。
- [x] T006 [US-001] 复现并修复 weightedLeastPing 非正权重错误回池；测试
  零/负/NaN/Inf、first-match、正则及数值标签，保留 leastLoad 行为。
- [x] T007 [US-001] 四包完整 race、新增测试十次重复 race、gofmt/diff 检查通过，
  精确命令和结果已记录。
- [x] T008 [US-001] 同步本目录 spec/plan/tasks/tests/checklist，
  记录整表替换语义及 core wire 与实际客户端验证的区别。
- [x] T009 [US-001] 父进程审阅归并并统一隔离 clone wrapper 构建；
  双平台构建/全仓验证结果见 [最终验收](../001-core-fork-contracts/acceptance.md)。
- [x] T010 [US-001] 实际 Sidecar/TUI 消费者通过本地真实 Core c1ed7ad4 RoutingService
  解码静态/追加/删除/整表替换规则；断言用户、域名、入站、出站和顺序及修改后复读，
  两边各十次 race 通过，TUI xrayapi 完整包 race 通过；无 fake ListRule、无 pin 修改。

T005 是核心范围完成；T010 的真实消费者 RPC 缺口已闭环，非生产网络或 UI 渲染验收。
[消费者命令与证据](../001-core-fork-contracts/@@SPECMAP0@@)
[历史重建](../001-core-fork-contracts/reconstruction.md) · [本轮证据](../001-core-fork-contracts/@@SPECMAP1@@)
