# 监听与 UDS 验证矩阵

继承 001 的 V-005 至 V-009。受支持的补测已实现并通过，不再是待实现 harness。
Core 路径相对 `/Volumes/Linux/opensource/cuber/xray-core-spec`。
[原补测证据](../001-core-fork-contracts/evidence-006-007.md)记录完整跨 002/003
race 三轮命令（8 包、240 个通过事件、退出 0）和最终 TCP/UDP 计数补测
（三轮、18 个通过事件、退出 0）。通过事件包含子测试和重复运行。

| ID / 原 FR | 已实现测试 | 已通过的断言 |
|---|---|---|
| V-005 / 003 | TestListenerContractUnixDialAndFreedom、TestFreedomUnixContractMissingClosedCanceled；原 Unix Dial/redirect/network 用例 | 两条真实 stream 链路 64KiB 一致，缺失/关闭/取消返回错误，Freedom 目标始终 UNIX |
| V-006 / 003 | TestListenerContractSocksAuthentication；TestSocksLocalAddress | Unix 与 TCP 对照，错密码/未知用户业务零到达，正确用户 64KiB 一致；非 TCPAddr 不 panic、helper 回退本地地址 |
| V-007 / 003 | 三个 TestInboundListen* | 省略、旧字符串、顶层 null、单/双元素有效数组；无效数组/TUN/缺 port 拒绝，proto 互斥及往返保留 |
| V-008 / 003 | 两个 TestMultiListen*；TestListenerContractMultiUserAndCounters | 两地址 TCP/UDP 每次计数精确 +5/+5；一次增删用户作用于两地址；失败释放先前 TCP/UDP listener |
| V-009 / 003,008 | TestListenerContractRPCFailureRetry | 真实 gRPC + dokodemo-door，单地址/第二地址冲突，失败无 ghost，原 tag/port 重试回显，删除后 TCP/UDP 重绑 |

## V-005：真实 Unix stream 与失败路径

复验：
```sh
go test -json -race -count=3 -timeout=3m ./app/proxyman/inbound ./proxy/freedom ./transport/internet -run '^Test(ListenerContractUnixDialAndFreedom|FreedomUnixContractMissingClosedCanceled|ProcessRedirectsToUnixDestination|DialUnixDestination|IsStreamNetwork)$'
```

在短路径 UDS 上运行回显，通用 Dial 与真实 Core Freedom 的 unix: redirect
分别传输 65536 字节。并发写/读避免 macOS 小 socket 缓冲导致夹具自己阻塞。
通用 Dial 使用不存在路径、已关闭 listener、已取消 context；直接 Freedom.Process
也分别验证三种失败，每次拨号的目标 network/path 必须仍是 UNIX/原路径。
业务错误有界返回，关闭所有连接与 listener，join 自有回显任务后移除目录。
Freedom 真实入口在 UDS 消失后关闭 TCP 调用侧；不以调用侧超时冒充正确关闭。

## V-006：认证与地址类型

复验：
```sh
go test -json -race -count=3 -timeout=3m ./app/proxyman/inbound ./proxy/socks -run '^Test(ListenerContractSocksAuthentication|SocksLocalAddress)$'
```

Unix SOCKS 与 TCP SOCKS 对照都使用仅供测试的凭据。先测错误密码和未知用户，
必须报错且业务 accept 数不增加；正确用户回显 64KiB 且仅增加一个业务连接。
Unix 分支实际经过服务端处理路径；独立 helper 测试确认 Unix 地址回退本地 IP、
TCP 地址不变。完整 session source 元数据未捕获，不把此边界写成已通过；
UDS UDP ASSOCIATE 不在契约内。Windows 只 skip unix 子测试，tcp 子测试仍执行。

既有 testing/scenarios/TestSocksUnixSocket 作为历史对照保留；
此次新增夹具的执行证据不冒充该场景包已重跑。

## V-007：解析兼容与拒绝

复验：
```sh
go test -json -race -count=3 -timeout=3m ./infra/conf -run '^TestInboundListen(Compatibility|RejectInvalidArrays|ArrayRequiresPortAndSupportedProtocol)$'
```

纯 Build 不启动 Core。有效输入包括省略、单字符串、顶层 null，以及单/双 IP 数组。
逐项拒绝空数组、[null]、重复 IP（含等价 IPv6）、通配地址、域名、UDS、
错误元素类型、TUN 及缺 port 的数组配置。检查旧 listen 与新 listen_addresses
互斥、数组元素数量及 JSON 往返。顶层 null 是旧兼容行为，不能误写为一律拒绝。
不宣称所有可能 JSON 排列均已穷举。

## V-008：多地址共享及回滚

复验：
```sh
go test -json -race -count=3 -timeout=3m ./app/proxyman/inbound -run '^Test(MultiListen(TCPUDP|BindFailureReleasesEarlierAddress)|ListenerContractMultiUserAndCounters)$'
```

127.0.0.1 与 ::1 两地址各跑 TCP/UDP；每次五字节回显使同 tag 计数精确增加
uplink=5、downlink=5。预占第二地址使 Start 失败，第一地址的 TCP/UDP 可重绑。
Linux splice 在 TCPConn.ReadFrom 返回后才累计 downlink，因此回显端点使用
io.CopyN(..., 5) 后关闭，TCP 客户端写后 CloseWrite、读完后验证 EOF，
再以原有有界等待核对精确 5/5；不让未结束的 io.Copy 会话阻塞统计提交。
Trojan 双地址 stream 入站通过一次真实 gRPC AddUser 开放同一用户；
每地址发送 64KiB 后 uplink=65604（含 68 字节握手）、downlink=65536；
一次 RemoveUser 后两地址均拒绝，业务 accept 数不再增长。
动态用户与 TCP/UDP 计数由不同协议夹具验证，不声称所有 UDP 协议动态用户已覆盖。
Darwin 与 Linux 容器两地址实测成功；不能绑定时测试失败，不修改网卡或静默 skip。

## V-009：真实 RPC 失败原子性

复验：
```sh
go test -json -race -count=3 -timeout=3m ./app/proxyman/inbound -run '^TestListenerContractRPCFailureRetry$'
```

API 监听随机 loopback 端口。dokodemo-door 覆盖单地址占用和第二地址占用：
AddInbound 返回 RPC 错误、ListInbounds 为空；部分绑定的 TCP/UDP 可重绑。
释放占用后，以原 tag/port 重试成功，每地址完成 64KiB 回显；
RemoveInbound 后列表为空，所有 TCP/UDP 地址可重绑。关闭 API channel、
Core 和占位 listener。无需 AnyTLS 用例替代此证据；008 专项不在本轮重跑范围。

## 平台审查与实际执行

CI `.github/workflows/test.yml` 的矩阵包含 windows-latest、ubuntu-latest、
macos-latest。新增夹具之前使用 os.MkdirTemp 的默认临时根，可能过长，
且 Windows 盘符路径不符合这些测试的 POSIX socket 路径约定。

修正仅在两个新增测试文件：
- app/proxyman/inbound/listener_contract_test.go：UDS 专用 helper 先判断 Windows，
  再在 /tmp 建立 xray-uds-* 私有目录，Cleanup 在 socket/Core 关闭后移除目录。
  SOCKS 的 tcp 子测试不调用这个 helper。
- proxy/freedom/uds_contract_test.go：仅整条 UDS 专项在 Windows skip，随后在
  /tmp 建立短目录。其他 Freedom 测试不变。

不使用 t.TempDir 的长测试名路径，不依赖 TMPDIR。Darwin/Linux 不 skip，
目录创建或绑定失败仍报错。Windows skip 是此 POSIX 夹具的范围限制，
不是否定 Windows AF_UNIX 的存在，也不是把 skip 当成运行通过。

实际在 Go 1.26.1 darwin/arm64 执行：
```sh
go test -json -race -count=3 -timeout=2m ./app/proxyman/inbound ./proxy/freedom -run 'Test(ListenerContract|MultiListen|FreedomUnixContract)' > /tmp/evidence-007-platform-darwin.json
env GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/xray-spec007-inbound-windows.test.exe ./app/proxyman/inbound
env GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/xray-spec007-freedom-windows.test.exe ./proxy/freedom
```

三条命令退出码均为 **0**。Darwin race 为 **60 个通过事件、0 skip、0 fail**；
Windows 两包交叉编译成功，**未执行 Windows 二进制**。inbound 包在下面的
Linux 同步修复之后重新执行同一 Windows 编译命令，退出 0。不声称全平台 CI 已通过。

## Linux splice 同步修复与冻结

007 全库禁网测试曾发现 TestMultiListenTCPUDP/127.0.0.1/tcp 的 uplink=5、
downlink=0。源码定位 proxy/proxy.go 的 CopyRawConnIfExist：Linux splice
在 tc.ReadFrom(readerConn) 返回后调用 writeCounter.Add(w)。原测试端点
io.Copy(c,c) 等待会话结束；客户端仅 ReadFull 五字节不保证这一步已返回。
这不是把 downlink=0 接受为正确值，也不是靠更长 sleep 修复。

修正 app/proxyman/inbound/listen_test.go 的固定五字节回显与 EOF 同步，
不改生产统计、不关闭 splice、不放宽任何计数。实际执行：

```sh
go test -json -race -count=10 -timeout=2m ./app/proxyman/inbound -run '^TestMultiListenTCPUDP$' > /tmp/evidence-007-splice-darwin.json
docker run --rm --network none -v /Volumes/Linux/opensource/cuber/xray-core-spec:/work:ro -v /Users/cube/Dev/go/pkg/mod:/go/pkg/mod:ro -w /work -e GOPROXY=off -e GOSUMDB=off -e GOTOOLCHAIN=local golang:1.26.1 go test -json -race -p 2 -count=3 -timeout=3m ./app/proxyman/inbound ./proxy/freedom -run 'Test(MultiListen|ListenerContract|FreedomUnixContract)' > /tmp/evidence-007-splice-linux.json
```

两条命令均 **exit 0**。Darwin 十轮 **50 个通过事件、0 skip、0 fail**；
Linux 两包三轮 **60 个通过事件、0 skip、0 fail**，包含多地址精确计数、
动态用户、真实 RPC、UDS 与 Freedom 错误路径。Linux 使用共享只读源及预置依赖，
非完整库、非不可变 release tree 验收；没有改动宿主网络或防火墙。

验证后已通知父流程 **002/003 源码冻结**，本 worker 后续仅改 spec 文档。
007/父流程需用新快照接续全库复验；不能将本定向通过改写为此前全库通过。

## 证据使用规则

分项复验命令从主选择抽取，除上节实际执行记录外，不宣称每条拆分命令
均单独运行。零命中、skip、编译失败不能记作运行 PASS。
历史重建 tree 对比与新增测试增量分别记录；无全局 FD 无泄漏或生产部署声明。
