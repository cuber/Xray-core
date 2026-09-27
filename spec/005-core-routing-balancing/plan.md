# 路由与调度实施计划

## 阶段与结果

1. 历史重建：依 FR-006/003 核对文件和依赖；保留
   [001 重建记录](../001-core-fork-contracts/reconstruction.md) 中的历史 tree 对比。
2. 核心补测（已完成）：独立 600 次 oracle、阈值矩阵、fallback/恢复、
   user/leastLoad 边界，以及真实 TCP gRPC 增删替换、深复制和并发 harness。
3. 缺陷修复（已完成）：复现零/负权重错误回池，给 weightedLeastPing 使用独立的
   字面 first-match 读取与加锁缓存，排除 NaN/Inf；不改共享 WeightManager/leastLoad。
4. 控制面语义（已验证）：重复追加拒绝、整表替换、不存在删除幂等、空 tag 拒绝；
   真实 gRPC 和 direct service 嵌套响应修改均不污染内部状态。
5. 验证（已完成）：四包完整 race、新增测试十次重复 race、gofmt/diff 检查。
   [执行证据](../001-core-fork-contracts/@@SPECMAP0@@) 保存命令、结果和日志哈希。
6. 父进程收敛（未完成）：审阅归并、统一隔离 clone wrapper 构建、实际 Sidecar/TUI
   解码验证。当前 worker 不提交、不 checkout、不再尝试构建、不部署。

## 文件与协作边界

005 归属：app/router/strategy_weightedleastping.go、contracts_balancing_test.go、
app/router/command/contracts_rpc_test.go、infra/conf/contracts_balancing_test.go。
command.go 的取消修改属于 007，不纳入本类别的代码修改声明。
正则和普通匹配保持原 first-match 顺序；第一匹配项无效权重不穿透后续项。
JSON 验证和 protobuf 运行时过滤分别测试，不把合法 JSON 当作全部 API 输入边界。

## 验证与回滚边界

[tests.md](tests.md) 保留逐 V 编号复验。使用真实 TCP gRPC 和显式关闭的 client/server，
独立互斥计数器统计串行/并发结果，所有测试 goroutine join 后结束。
旧 wire 字段兼容仅是 core 范围证据，不替代实际 Sidecar/TUI 测试。
历史备份仍是重建回滚参考；本轮新增修复由父进程按 spec 可追溯归并。
不改变远端、Sidecar pin 或部署记录。
