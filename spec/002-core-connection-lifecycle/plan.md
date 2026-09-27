# 连接生命周期实施计划

1. 历史阶段按 [spec.md](spec.md) 与原提交逐文件对应，基线 tree 对比结果保留在
   [001 重建记录](../001-core-fork-contracts/reconstruction.md)。
2. 后续授权补测在独立 `/Volumes/Linux/opensource/cuber/xray-core-spec` 完成；
   不修改原始 checkout，不混入其他 worker 的工作，不改运行时语义。
3. 已增加 transport/dispatch_contract_test.go、
   app/proxyman/outbound/dispatch_contract_test.go、
   transport/internet/hysteria/lifecycle_contract_test.go，覆盖 V-001 至 V-004。
4. 已执行定向 race 三轮；[tests.md](tests.md) 给出可复验命令，
   [证据](../001-core-fork-contracts/@@SPECMAP0@@) 保存退出码和失败试跑。
   不把父任务的全量回归、其他平台 CI 或发布构建冒充本类别已执行。
5. 契约明确 packet 仅响应方向、取消后显式 Close，以及重复 clean 与私有
   client.close 的区别。FD 枚举失败采用自有资源 join/关闭断言，不虚报数字。
6. 本轮同步规格、任务和清单；不提交。父任务按 spec 分组后续测试提交。
   历史基线等同性不适用于这些经授权新增的测试文件。
   003 Linux 定向复验结束后已一并通知父流程：002/003 源码冻结，后续仅同步文档。

提交映射、回滚备份由 001 管理；不修改远端、Sidecar pin 或已部署版本记录。
