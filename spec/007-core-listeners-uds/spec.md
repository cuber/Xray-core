# 监听与 UDS

- Feature ID: 007-core-listeners-uds
- Status: Implemented（隔离分支补测、提交归并及本地构建完成；未部署）
- Authorized: 2026-09-27，用户授权分类重建、缺口补测及文档/平台审查；主流程统一提交，不部署。
- Source: 原 Core e7a21974，相对 b4f08981；清单见 [005](../005-core-fork-contracts/inventory.md)。
- Scope: Unix stream、认证 SOCKS、多地址共享与监听失败原子性。
- 原 005 FR-003 保留为稳定追溯编号；不修改运行时语义。

## US-001 — 分类维护与独立验证（P1）

Given 原始分叉和受控输入，When 在隔离 worktree 执行补测，
Then 以业务可达、拒绝、精确计数和资源重绑验证行为，不以测试存在代替通过。

## FR-001 — 当前行为契约

- Freedom redirect 的配置使用 unix: 前缀；通用 dialer 将 Unix 目标按 stream
  处理，不落 UDP。真实连接分别验证 64KiB、缺失路径、关闭 listener 与取消。
- SOCKS Unix stream 认证生效；错误密码/未知用户不进入业务端点。
  非 TCPAddr 不 panic；地址 helper 对 Unix 回退本地地址，不伪造公网源。
  不承诺 UDS UDP ASSOCIATE，亦不声称完整 session source 元数据已被捕获。
- listen 保留旧字符串、省略以及顶层 null 行为；数组只接受唯一、非通配
  的显式 IP，单元素数组有效。空数组、[null]、域名/UDS/通配/重复 IP、
  TUN 和缺 port 的无效数组配置拒绝。顶层 null 与数组内 null 不混淆。
- 多地址共享协议实例、动态用户、tag 和计数器。TCP/UDP 共享计数用
  dokodemo-door 验证；动态用户用 Trojan 的双地址 stream 会话验证，
  不推广为所有 UDP 协议的动态用户验收。
- 后续地址绑定失败释放已绑定 TCP/UDP listener；同 tag、同 port 重试可用。
  AddInbound 失败不发布 ghost handler；RemoveInbound 后列表为空且地址可重绑。
  验证使用真实 loopback gRPC、非 AnyTLS 协议，不以 012 的对照用例替代。
- receiver proto 的 listen 字段 2 与 listen_addresses 字段 7 互斥。
  旧 core 不理解新字段，多地址配置不能无条件回滚到旧 core。
- ValidateStream 钩子失败清理协议对象的 AnyTLS 专项验收仍归 012，
  不计入本轮普通协议覆盖。

## 平台边界

新增真实 UDS 夹具使用短 `/tmp/xray-uds-*` 私有临时目录，
不继承可能很长的 TMPDIR/工作区路径，清理时关闭 socket 并移除目录。
Windows 仅跳过真实 UDS 用例/子用例：这是夹具的 POSIX 路径范围，
不是声称现代 Windows 没有 AF_UNIX。TCP SOCKS 对照、普通多地址监听、
RPC 和纯配置解析仍启用。Darwin/Linux 不按能力探测跳过；实际绑定失败应报错。

## SC-001 — 验收标准与状态

历史 tree 等同性仅约束重建基线；授权新增测试不要求与原始 HEAD 逐字节相等。
V-005 至 V-009 在上述范围内已实现并通过 Darwin race；新增监听/UDS 用例亦在
Linux network-none 容器 race 三轮通过。Windows 受影响测试包已交叉编译，
未执行 Windows 测试程序。skip 不计为运行通过。
[tests.md](tests.md)记录平台审查命令；
[补测证据](../005-core-fork-contracts/evidence-006-007.md)保留此前的完整执行记录。
本轮无生产实现变更、提交、全量或部署声明。

Linux splice 的 downlink 统计在 TCPConn.ReadFrom 返回后提交；测试回显端点
完成固定载荷后关闭，客户端 CloseWrite 并等待 EOF，再核对精确 5/5 计数。
不以延长等待、关闭 splice 或放宽断言绕过会话完成条件。006/007 源码已冻结，
父流程可按 spec 归并提交；后续全库复验由 011/父流程在新快照执行。

## 导航

[技术计划](plan.md) · [任务](tasks.md) · [检查清单](checklists/requirements.md)
