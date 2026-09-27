# 连接生命周期检查清单

- [x] 定义范围、来源、用户场景、现有契约及非目标。
- [x] 保留 V-001 至 V-004，并对应实际实现的测试与通过记录。
- [x] 区分历史重建 tree 等同性与后续授权测试增量。
- [x] 定向三轮 race 结果和失败试跑可追溯到
  [补测证据](../../005-core-fork-contracts/evidence-006-007.md)。
- [x] 明确 packet 只在响应方向、Cancel 不等于 Close、deadline 不提供超时。
- [x] 明确 manager clean 的重复调用不推广为私有 client.close 的无条件幂等。
- [x] /dev/fd 不可枚举不标数字通过；受测资源以 join、关闭断言验收。
- [x] 未覆盖的全平台、生产 Hy2/NAT、全局 FD 保证已列为证据边界。
- [x] spec、plan、tasks、tests 状态一致；补测完成不代表提交或发布完成。
