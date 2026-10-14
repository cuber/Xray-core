# AnyTLS 资源审计

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

2026-09-26，基线 Core `cb7410ca`；本地补强及验收版本 Core `187bf0ab`。
这是已实现边界的静态审计，不能代替容量压测。
最初审计为 SHI 临时受控账户试点；后续用户授权 SHI/ZDS 对齐 Hy2 用户，
部署范围及实测见 [pilot.md](pilot.md) 文末。

## 已确认的边界

| 对象 | 当前上限 / 生命周期 |
|---|---|
| 外层会话 | 默认 256，单用户默认 16；SHI/ZDS 已对齐该值，最初隔离试点为 8 / 4；未认证会话也计入总数 |
| 每会话流 | 默认 128；SHI/ZDS 已对齐该值，最初隔离试点为 16 |
| 整个入站的活动 handler | 固定 128；按每条 256 KiB 保留协议缓冲预算，即 32 MiB |
| 协议读队列 | 每流一个 pending 加一个 read cache，生产者背压；不是无限排队 |
| 每个 UoT association 的目的地 | 最多 64 个 dispatcher link；空闲取消后在后续报文时回收 |
| 全入站 UoT 目的地 | 本地补强为 128 个；worker 退出并 interrupt 双向 pipe 后释放，淘汰 map 项不释放 |
| AnyTLS 双向 pipe | 本地补强为每个 pipe 至多 32 KiB 阈值；更小的非负用户 policy 保留，unlimited 被限制 |
| 用户撤销 | 先标记 revoked，再关闭旧会话；旧 generation 不复活 |
| stream deadline | 关闭时停止 timer，关闭后拒绝重新设置；已修复持有会话的问题 |

依据：Core `proxy/anytls/server.go`、`udp.go`、`internal/engine/stream.go`。
三轮真实 TLS 100 流关闭测试：预热后堆约 7.6 MB、FD=4、goroutine=3，
测试进程不随轮次线性增长；包含客户端/回显夹具，不能当作服务端独占开销。

## 原始问题：UoT 多目标的放大

基线中 128 是逻辑流限制，不是 dispatcher link 限制。128 个 UoT association
理论上可持有 `128 * 64 = 8192` 个目的地 link。每个 link 有双向 pipe，
还伴随 worker、timer、上下文和实际 outbound 资源。

`features/policy/policy.go` 的默认 pipe 缓冲在 amd64 是 512 KiB，
Darwin arm64 则是 4 KiB。因此，默认 amd64 policy 下，仅按双向 pipe 的
标称阈值计算就有 `8192 * 2 * 512 KiB = 8 GiB` 的放大空间。
这是潜在缓冲规模，不是预分配，也不是严格的 RSS 上界：pipe 可因一个批次
越过阈值，实际分配取决于背压、响应速率和 outbound。TLS、内核 socket、
路由、统计和 Go 开销还没有计入。

所以本机 arm64 压测不能证明 amd64 默认配置可承受最坏多目标流量；
32 MiB 仅是协议层预算，绝不是整个 AnyTLS 或 Core 的内存上限。
SHI 的 8 / 4 / 16 也允许总计 128 条逻辑流，不能靠这组参数消除上述放大；
试点安全边界仍是随机受控账户、低并发、结束后零账户。

## 本地补强与验证

已将共享 UoT link 准入和 32 KiB pipe policy 实现在 AnyTLS 内部，不修改
dispatcher/全局 policy。保守按 128 TCP link 加 128 UoT link 计算，双向 pipe
阈值合计不超过 16 MiB；实际 TCP/UoT 还共享 128 个 handler，所以此值偏保守。
这不是 RSS 硬上限：仍有单批越界、outbound 异步收尾及 TLS/runtime 开销。

新增 `proxy/anytls/udp_budget_test.go`，本地 race 测试通过：

- 两个 association 各 64 目标占满共享预算，第三个被拒绝且不调用 Dispatch。
- 三轮饱和、全部断开、额度归零、重新准入。
- Dispatch 失败、取消 context、关闭入站不泄漏/新增额度。
- 人为延迟 worker 退出，确认取消不能提前归还额度。
- amd64 等价 512 KiB 与 unlimited policy 均限制为 32 KiB；真实 pipe 的
  慢消费者触发背压，Interrupt 后阻塞写退出；保留 0/4 KiB 等更小 policy。

日志 `.cache/anytls/budget-tests.log`，AnyTLS/engine/API 的 race 测试通过。
夹具级测试不等同于真实多用户多目标高负载容量结果。

## 真实负载补验

- `TestAnyTLSUoTResourceRounds`：真实 Core/TLS、两个认证用户、各 64 个 UDP
  目标，显式 512 KiB 用户 policy。第 129 个目标拒绝，另一用户 TCP 及已有 UDP
  正常；经真实 gRPC 撤销第一用户后，第二用户可完整补足 64 个新目标。
- 预热 + 三轮完整回收，连续 20 次 race（共 80 轮）通过；运行时约 143 FD、
  535 goroutine，清理后回到 4 FD、3 goroutine。测试包含 Core、客户端和回显器，
  不能称作独立服务端 RSS。日志 `uot-loopback-repeat.log`。
- `TestAnyTLSUoTSlowConsumerRevocation`：unlimited policy、64 目标响应突发且
  客户端不读；采样堆约 9.4 MB，撤销后阻塞读/写退出。实际共享上限由准入单测
  和真实 128 目标测试共同验证，不把一次采样当作内存硬上限。
- 双独立客户端各 50 TCP 流、不同 marker 出口、持续十分钟，逐用户实际发送/
  接收字节和 StatsService 增量精确相等；显式 512 KiB 用户 policy。
  `distinct-client-stress.log`，601.8 秒通过；活跃 goroutine 约 934，无串流串账。
- 新增 UoT 适配器输入 fuzz，60 秒 632,939 次输入通过；`adapter-fuzz.log`。

原始重复测试曾在本机双栈 UDP 随机端口 59870/59871 上超时；日志与 lsof
显示 IPv4 端口由 Logitech 进程占用。隔离 IPv4 回显夹具使用 `sendThrough:
127.0.0.1` 后，20 次重复全过；没有关闭宿主程序、忽略错误或引入报文重试。
原始失败记录保留在 `uot-repeat.log` / `uot-debug.log`。

## 定稿与边界

默认会话/单用户会话/每会话流保持 256/16/128，协议 handler 总数 128，
UoT link 总数 128，单 association 64，pipe 阈值最多 32 KiB。
共享预算不保证新请求的用户间公平配额；满载时可以拒绝新 UDP 请求，但不应
断开其他用户已有连接。并未改变平台全局 policy、outbound 的异步销毁协议或
内核 socket 缓冲，不以此宣称整个进程有严格 RSS 上限。
T008/T010 的本地资源闸门已补齐；试点重跑与整份 spec 状态见 implementation.md。
