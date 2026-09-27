# 连接生命周期验证矩阵

继承 005 的 V-001 至 V-004。以下受支持契约已实现并验证，不再是待实现 harness。
Core 路径相对隔离 worktree `/Volumes/Linux/opensource/cuber/xray-core-spec`。
2026-09-27，Go 1.26.1 darwin/arm64；原始 checkout 未修改。

[证据报告](../005-core-fork-contracts/evidence-006-007.md)记录完整跨 006/007
命令、失败试跑与退出码：主选择 8 个包、race 三轮退出 0，240 个通过事件
（含子测试与重复次数，非 240 个独立测试）。
最终三入口补充命令三轮退出 0、48 个通过事件。无全量或 Windows/Linux 运行声明。

| ID / 原 FR | 已实现测试 | 已通过的断言 |
|---|---|---|
| V-001 / 002 | TestDispatchContractRunnerExit64KiB；原 NewDispatchConn 两个所有权用例 | runner 交出 link 后 65536 字节回显一致；不抢先 EOF；Close 解除阻塞、重复关闭 |
| V-002 / 002 | TestDispatchContractConcurrentCloseInterrupt、InterruptUnblocksWaitingRead、PacketResponseBoundaries；原接口保留用例 | 两种输出模式各 100 轮、32 并发调用；真实读超时、Interrupt 唤醒；响应 buffer 长度 1/17/1200 不合并 |
| V-003 / 002 | TestDispatchEntrancesLifecycle；TestProxySettingsDialPreservesTimeoutReader | 三入口并行；每入口四种场景各 100 轮，真实 loopback 字节对照、所有自有 runner 退出 |
| V-004 / 004 | TestClientContractOtherConfigurationProgress、FailedCreationCleanup、AuthenticationFailureClosesPacketConn、UDPFailureDoesNotPanic；原 manager 锁用例 | 第二配置一秒内取得；失败原始连接关闭；100 次真实认证拒绝和 100 次 TLS 拒绝，UDP socket 均已关闭 |

## V-001：runner 与 pipe 所有权

运行：
```sh
go test -json -race -count=3 -timeout=3m ./transport -run '^Test(NewDispatchConn(DoesNotCloseWhenRunnerExits|CloseUnblocksRunnerRead)|DispatchContractRunnerExit64KiB)$'
```

双向 pipe 夹具将 runner 的 link 交给回显任务；调用者在交接后发送固定 64KiB。
既有 runner-return 测试与新增大载荷测试共同覆盖提前 EOF 回归。
读取逐字节比较，Close 后等待 copier，重复 Close。等待使用独立的一秒时限，
不依赖 cnc 的空 deadline 方法。摘要：
`ef4636928161808e87035fa51983821677527ccd9661991c5d0126a778b2268a`。

## V-002：超时、Interrupt 与响应 packet

运行：
```sh
go test -json -race -count=3 -timeout=3m ./transport -run '^Test(NewDispatchConn(PreservesTimeoutReader|ForwardsInterrupt)|DispatchContract(ConcurrentCloseInterrupt|InterruptUnblocksWaitingRead|PacketResponseBoundaries))$'
```

每轮新建 pipe；实际调用 TimeoutReader 得到 buf.ErrReadTimeout。32 个
Close/Interrupt 并发调用后 join，双向等待者一秒内结束；两种模式各 100 轮。
另有只调用 Interrupt 的读唤醒测试。packet 测试一次提交含三 buffer 的
MultiBuffer，使用足够大的切片逐个读出 1、17、1200 字节并比较内容。

**边界：** packet 是响应 reader 的拆分策略，net.Conn.Write 仍为流。
不测试或承诺请求包边界、任意大数据报、短 Read 保留包余量；这些需要另行
定义 API，不是本编号残留的待实现项。

## V-003：三个真实入口

运行：
```sh
go test -json -race -count=3 -timeout=3m ./app/proxyman/outbound -run '^Test(DispatchEntrancesLifecycle|ProxySettingsDialPreservesTimeoutReader)$'
```

proxySettings Handler.Dial、DialSystem/dialerProxy 与 singbridge.NewOutboundDialer
三个并行子测试，使用受控 outbound processor 连接真实 TCP loopback。
每入口分别 100 次：64KiB 回显、拒绝拨号、远端提前关闭、调用者取消后 Close。
与直接 TCP 回显逐字节对照；有界等待每个 runner，清理并 join listener/accept 任务。

**边界：** redirect 使用 WithoutCancel，Cancel 本身不是 Close。测试主动 Close，
不改变或假定不存在的取消传播。受控 processor 的退出证据不代表每种代理协议
都经过此测试。进程级 /dev/fd 在本机报 bad file descriptor；只记录不可用，
用自有任务 join、连接退出验收，不标“FD 数量不增长”通过。

## V-004：Hy2 锁与失败清理

运行：
```sh
go test -json -race -count=3 -timeout=3m ./transport/internet/hysteria -run '^Test(ClientContract.*|ClientManager(GetOrCreateDoesNotHoldManagerLockWhileSettingContext|CleanDoesNotHoldManagerLockWhileCleaningClient))$'
```

人为持住 A 客户端锁，分别启动 setCtx/clean，B 配置一秒内取得客户端，
释放锁后任务结束；新增测试使用 deferred 解锁保证失败路径也释放锁。
100 轮注入拨号失败及不受支持连接类型，检查 peer 观察到关闭、缓存对象可重用，
每轮 32 次并发 clean。真实本地 HTTP/3 服务使用测试证书：
100 次认证 handler 到达并返回拒绝，随后 100 次不受信 TLS 握手失败且
handler 到达数不再增长。每次原始 UDP socket 再写必须为 net.ErrClosed。
服务和 packet listener 最终关闭、join；重复 clean 后不留客户端资源引用。

**边界：** manager clean 可重复，不等于要求无活动连接时反复调用私有 client.close。
成功 Hy2 会话、NAT/UDP-hop 和生产端点不在本次验收范围；inactive hop 拒绝已有断言。
没有全局 goroutine 数量或任意失败分支穷尽声明。

## 证据使用规则

上述分项命令是从已执行主选择中抽取的复验入口，不宣称每条拆分命令都单独执行。
零测试命中、平台 skip、编译失败不得计作运行通过。历史重建结果仍见
[005 reconstruction](../005-core-fork-contracts/reconstruction.md)，与补测增量分开。
