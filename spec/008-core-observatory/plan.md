# Observatory 实施计划

## 阶段与结果

1. 历史重建：按 FR-005 分类、核对依赖与累计 tree；历史结果见
   [005 重建记录](../005-core-fork-contracts/reconstruction.md)，不覆盖本轮新增差异。
2. 核心补测（已完成）：在 xray-core-spec 中实现 V-010 至 V-014 的真实 HTTP、
   健康恢复/缓存、快照和停止并发 harness，并补 JSON 配置测试。
3. 缺陷修复（已完成）：复现 Stop 返回后 HTTP 仍活动；绑定请求 context，
   调度 run 独立取消，跟踪并等待 worker/延迟采样，取消和发布共用结果锁。
4. selector 边界（已完成）：使用缓冲结果通道取消等待；回调本身仍须并发安全、
   及时返回并释放资源。禁止宣称可以强制中止不可取消的任意函数。
5. 验证（已完成）：四包完整 race、新增测试十次重复 race、gofmt 和 diff 检查；
   准确命令、失败到修复证据、文件归属见 [执行证据](../005-core-fork-contracts/evidence-008-009.md)。
6. 父进程收敛（未完成）：审阅并按 spec 归并修改，统一隔离 clone wrapper 构建；
   当前 worker 不提交、不 checkout、不再尝试构建、不部署。

## 文件与实现约束

008 归属：app/observatory/burst/ 下的 healthping.go、ping.go、manager.go、
contracts_http_test.go，以及 infra/conf/contracts_observatory_test.go。
保持随机分散采样和 3s/2s/20 设置，不借停止修复钳制为 10s。
Start/Stop 锁阻止 WaitGroup 跨 run 复用；新增任务来自已经计数的调度 worker。
取消屏障与调度发布在同一结果锁下判定，公共显式 PutResult 保持独立。

## 验证与回滚边界

按 [tests.md](tests.md) 复验；HTTP 服务器、空闲连接和测试 barrier 均清理，
阻塞 selector 由测试主动释放。测试不声称真实远端路由或精确三秒发包。
历史备份分支/tag 仍是重建回滚参考；新增修复由父进程审阅后单独可追溯归并。
不修改远端、Sidecar pin 或部署记录；遵守 constitution 的隔离与事实约束。
