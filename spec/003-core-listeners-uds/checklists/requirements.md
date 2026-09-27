# 监听与 UDS 检查清单

- [x] 定义范围、来源、用户场景、现有契约和非目标。
- [x] V-005 至 V-009 对应实际实现的正负向测试，不保留过时的待实现标记。
- [x] 历史重建 tree 对比与后续测试增量分开。
- [x] 定向三轮 race、精确计数和真实非 AnyTLS RPC 证据可追溯到
  [报告](../../001-core-fork-contracts/evidence-006-007.md)。
- [x] 明确顶层 null 可兼容、[null] 拒绝，不混淆数组约束。
- [x] 明确 UDS UDP ASSOCIATE、完整源元数据、全协议动态用户不在证据范围。
- [x] 新增 UDS 路径长度不依赖 TMPDIR；Windows 局部 skip 不影响 TCP 对照。
- [x] Darwin/Linux 不跳过 UDS；Darwin 平台修正后 race 三轮零 skip。
- [x] Windows 编译与运行结果分开；本机仅交叉编译，未虚报 Windows CI 通过。
- [x] Linux splice 以传输结束/EOF 同步计数，保留精确 5/5，不关闭 splice。
- [x] Linux network-none 两包定向 race 三轮退出 0，60 通过事件、0 skip、0 fail。
- [x] 定向验证后源码冻结已通知父流程；全库复验仍交由 007/父流程。
- [x] spec/plan/tasks/tests 状态一致，补测通过不等于提交、全平台或发布完成。
