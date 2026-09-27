# 路由与调度检查清单

- [x] 定义历史来源、当前授权、核心补测/窄修复及非部署范围。
- [x] V-015 至 V-018 核心 harness 已实现并运行，不再仅核对测试名。
- [x] 600 次串行/并发独立 oracle 与阈值、fallback、候选恢复矩阵已通过。
- [x] 非正/非有限权重被排除；普通/正则 first-match 和 leastLoad 兼容边界已明确。
- [x] user 空后缀、多 @、大小写、非法/空正则及 leastLoad tolerance 已有断言。
- [x] 真实 TCP gRPC 增删替换、嵌套响应修改、并发快照和旧字段解码已验证。
- [x] 四包 race 与十次重复测试记录可追溯；历史 tree 一致性不覆盖新增实际修复。
- [x] 保留与 007 的文件所有权边界；未声称已审阅共享 tree 的其他修改。
- [x] 父进程完成审阅归并及统一隔离 clone 发布构建。
- [x] 实际 Sidecar/TUI 客户端解码通过；见真实消费者 RPC 证据，core wire 测试不替代此项。

[本轮证据](../../001-core-fork-contracts/@@SPECMAP0@@) · [测试矩阵](../tests.md)
