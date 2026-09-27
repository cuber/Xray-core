# 监听与 UDS 实施计划

1. 历史阶段与原提交逐文件对应，基线 tree 审计见
   [005 重建记录](../005-core-fork-contracts/reconstruction.md)。
2. 授权补测仅在 `/Volumes/Linux/opensource/cuber/xray-core-spec` 进行。
   新增 app/proxyman/inbound/listener_contract_test.go、
   proxy/freedom/uds_contract_test.go；补强原 listen_test.go 与配置数组测试。
   不修改原始 checkout 或其他 worker 的文件。
3. 已验证真实 UDS 64KiB、SOCKS 正负认证、配置解析、双地址 TCP/UDP 计数、
   Trojan 动态用户以及非 AnyTLS 真实 gRPC 添加失败/同 tag 重试/删除重绑。
4. 已执行定向 race 三轮；结果和重跑入口见 [tests.md](tests.md)。
   不把普通协议结果推广为任意 UDP 动态用户或 UDS UDP ASSOCIATE 保证。
5. CI 矩阵确认包含 Windows、Ubuntu、macOS。已将新增 UDS socket 放到短
   /tmp 私有目录，并仅在 Windows 局部 skip；不跳过 TCP SOCKS 对照或
   整个 listener 测试文件，不弱化 Darwin/Linux。两个受影响包 Windows
   amd64 交叉编译成功；平台实际执行与编译检查分别记录。
   011 的 Linux 全库运行暴露 splice 统计延后提交；测试改为固定五字节端点
   关闭及客户端半关闭/EOF 同步，保持精确 5/5。Darwin 十轮及 Linux
   network-none 定向 race 三轮通过，不修改统计实现。
6. 当前完成 spec/plan/tasks/tests/checklist 同步，无提交。父任务负责按 spec
   分组测试增量；基线 tree 等同性不适用于后续授权新增测试。
   Linux 定向验证后 006/007 源码已冻结；011/父流程用新快照复验全库，
   本 worker 不再修改源码，只继续这两个 spec 目录的文档。

回滚备份与历史提交映射由 005 管理；不更新远端、部署版本或 Sidecar pin。
