# 监听与 UDS 任务

- [x] T001 [US-001] 提取 FR-001 契约和 V-005 至 V-009 稳定编号。
- [x] T002 [US-001] 对照历史重建提交检查内容与依赖，执行原有定向测试。
- [x] T003 [US-001] 记录历史基线 tree 对比，区分授权新增测试。
- [x] T004 [US-001] tests.md 对照现有测试名提供可执行复验入口。
- [x] T005 [US-001] 实现并执行 64KiB UDS、缺失/关闭/取消、SOCKS 正负认证、
  单元素数组、双地址精确 TCP/UDP 计数及动态用户、真实非 AnyTLS RPC 原子性补测。
- [x] T006 [US-001] 明确顶层 null 与 [null]、UDS UDP ASSOCIATE、session source
  元数据、不同协议动态用户的证据边界，不把边界写成未实现的既定验收。
- [x] T007 [US-001] 审查 Windows CI；缩短新增 UDS socket 路径，Windows 仅局部
  skip UDS，保留 TCP 对照；Darwin race 三轮零 skip，Windows 两包交叉编译成功。
- [x] T008 [US-001] 同步 spec/plan/tasks/tests/checklist 与实际结果。
- [x] T009 [US-001] 修复 Linux splice 完成前断言计数的夹具时序：固定载荷、
  CloseWrite/EOF 同步，不放宽 5/5；Darwin 十轮及 Linux network-none race 三轮通过。
- [x] T010 [US-001] 定向验证后通知父流程 002/003 源码冻结，后续仅更新文档。

[补测证据](../001-core-fork-contracts/evidence-006-007.md)及
[本次平台复验](tests.md#平台审查与实际执行)分别保留原执行和平台修正记录。
补测完成不表示全平台 CI、提交或发布完成；Linux 定向测试已在容器执行，
Windows 仅交叉编译。007/父流程在新快照接续全库复验，本轮无提交。
