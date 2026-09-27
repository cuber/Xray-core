# AnyTLS 入站测试与验收计划

状态：验收清单；实际执行结果单独记录在 [implementation.md](implementation.md)。
未列出明确证据的用例仍为待执行，不能把部分通过视为整项通过。

2026-09-27 的隔离历史重建补验见 [独立客户端及资源证据](../001-core-fork-contracts/evidence-012.md)
和 [最终验收](../001-core-fork-contracts/acceptance.md)。本轮修正测试夹具的
Mihomo 转发就绪等待与 TCP/UDP 共用端口选择，不代替历史远端压测，也未重新部署。

远端试点已选 `tk.rfc.co.micro.shi:2083/TCP`，客户端为本地 sing-box；
经 CTC 登录、UDP 清洗边界、实际执行步骤与证据格式见 [pilot.md](pilot.md)。
本文件的隔离单测和压力测试仍为前置门槛，不在生产试点执行恶意输入/极限压测。

## 测试装置

会话回收回归：`TestIdleObservations` 覆盖活跃流、两次检查之间的短请求、
心跳不能延长空闲以及 teardown 前不归还配额；`TestIdleAdmissionRace`
验证新流与回收互斥；`TestIdleMonitorLifecycle` 检查定时器退出；
`TestDefaultUserSessionsExceedSixteen` 检查默认可超过 16 个会话，原显式
限额用例保留。`TestAnyTLSIdleHeartbeatsReleaseQuota` 使用真实 TLS/协议
心跳持续保持传输活跃，验证 30 秒业务空闲回收及限额 1 下重新连接成功。
上线必须再执行真实 Surge 2s 间隔重复拨测，记录成功/失败与服务端日志，
不能仅凭重启后短期恢复宣布修好。

- 单测使用 mock dispatcher、可控时钟和计数器；集成测试必须启动真实 Core。
- 锁定 sing-box 稳定版 v1.14.2；Mihomo 版本在 T002 固定并记录 hash，不使用浮动 latest。
- 两个独立外部客户端，各自持有不同密码；不需要实现 Xray AnyTLS outbound。
- 独立 TCP/UDP 服务返回不同 marker 并记录收到的字节与来源；以 marker 和服务日志
  判断实际出口，不能仅断言生成的路由 JSON。
- 临时 CA 签发带测试 SAN 的证书，客户端验证 CA/SNI，禁止默认 skip-cert-verify。
- 所有测试凭据仅在夹具中随机生成；日志、报告不记录秘密，临时文件 0600，退出清理。
- 固定数据量测试使用无重试的原始 TCP/UDP payload，避免 HTTP 头、DNS 重试等污染
  精确计数；另设 HTTP/TLS 场景验证域名嗅探。

## 用例矩阵

| ID | 场景与动作 | 验收结果 | 需求 |
|---|---|---|---|
| AT-001 | Xray 原生分层配置、JSON → protobuf → MemoryUser、合法/空用户配置、真实证书 run -test | 公共 User 承载 email/level，Account 仅承载协议身份字段；合法通过；空表运行但拒绝认证 | FR-001/002/014 |
| AT-002 | 空 email/password、重复 email/password、非法 padding/限制 | 配置失败，错误不含秘密 | FR-002/005/013 |
| AT-003 | 明文/非 RAW/REALITY/AnyTLS outbound 配置 | 明确拒绝，不误启其他协议 | FR-001/006/014 |
| AT-004 | 正确、错误密码及错误 CA/SNI | 只有正确身份和 TLS 验证可进入出口 | FR-001/002/013 |
| AT-005 | 两用户同目标、不同出口 marker | 分别命中对应出口，0 串流 | FR-003 |
| AT-006 | 精确 email 与 domain:route、inboundTag、network 组合 | 路由优先级与现有 Core 一致 | FR-003 |
| AT-007 | 同会话并发 100 条流，多个域名/出口，随机关闭部分流 | 目标独立，单流关闭不杀其余流 | FR-003/006/008 |
| AT-008 | 单用户 TCP 上行 1 MiB、下行 2 MiB | drain 后计数器增量精确等于 payload | FR-004 |
| AT-009 | 两用户同域名/多域名，切换 padding 大小 | 明细不串；汇总等于明细；payload 计数不受 padding 影响 | FR-004/001 |
| AT-010 | stats user policy 关闭、domain stats 分别开关 | 各开关独立生效，代理不中断 | FR-004/001 |
| AT-011 | IP 目标，无嗅探域名；UoT 特殊目标 | 无伪造域名归属、无 UoT 标记进入榜单 | FR-005/004 |
| AT-012 | Add/Get/List/Count/Remove 及冲突、不存在对象 | 既有 gRPC 语义、快照一致且无 panic | FR-006/013 |
| AT-013 | 100 个新流与 Remove 并发，设置同步屏障 | 撤销点之后没有新准入；旧会话断开 | FR-007 |
| AT-014 | 认证查表后暂停，Remove，再继续登记会话 | 不出现已删除用户新登记会话 | FR-007 |
| AT-015 | 删除后同 email 换密码重加；旧连接继续开流 | 旧 generation 永久失效，新密码可用 | FR-007 |
| AT-016 | UoT v2 DNS/回显、IPv4/IPv6/域名目标 | 包边界、目的地、响应源和用户正确；IPv6 在隔离可用环境验证 | FR-008 |
| AT-017 | 同 UoT 流向两个目标交替发包，UDP 路由分别匹配 | 两目标确实走不同 marker 出口，不固定首个目标 | FR-003/004 |
| AT-018 | UDP 零长度、小包、接近实现允许上限和超限包 | 支持范围保真，超限有界拒绝、不截断为成功 | FR-008/005 |
| AT-019 | 固定数量 UDP 回显，双用户并发 | 只计 payload；UoT 头不计入用户业务量 | FR-004/004 |
| AT-020 | 非法帧长度、无效 stream ID、截断地址、握手分片/慢读 | 无 panic/越界/无限等待；合法分片不误当非法 | FR-009/008 |
| AT-021 | 达到会话/用户会话/流上限，恶意用户持续建流 | 有界拒绝；其他未超额用户仍可访问 | FR-009/NFR-002 |
| AT-022 | 客户端不读、断网、RST、服务 Close | pending I/O 解除，监听/FD/协程回收 | FR-012 |
| AT-023 | Sidecar 同步 SS-2022+AnyTLS、Core 重启、类型缓存失效 | 两种正确 Account，重同步收敛 | FR-006/007 |
| AT-024 | 同步同密钥多 email、未知入站类型、部分 RPC 失败 | 预检或明确失败，无覆盖错误身份、无整批假成功 | FR-002/007 |
| AT-025 | admin 静态用户、受管用户删除、重复同步 | admin 不误删；用户增删可幂等收敛 | FR-011 |
| AT-026 | Core/Sidecar 分别重启、snapshot 恢复 | 用户计数重置语义及域名 epoch 去重沿用现有契约 | FR-005/007 |
| AT-027 | 状态/访问日志/错误日志与证书控制面 | 正确协议/用户/加载证书，无认证材料泄漏 | FR-013 |
| AT-028 | 未配置 AnyTLS，运行现有 SS-2022/Hy2/VLESS 夹具 | 行为与基线相同，无新监听和任务 | FR-014/NFR-004 |
| AT-029 | 对真实 gRPC 服务调用 AddInbound → ListInbounds → 客户端连接 → RemoveInbound | proto 配置可往返解码，协议为 anytls；删除后端口释放、旧会话关闭 | FR-015 |
| AT-030 | AddInbound 重名 tag、端口冲突、错误证书、非法设置 | 明确失败，无幽灵 handler/残留监听；正常入站不受影响 | FR-015 |
| AT-031 | gRPC 动态用户增删后由 Sidecar 查询配置/用户/数量/统计 | 读取运行时状态，不回退成 unknown 或启动文件旧数据，公开响应无密码 | FR-006/013/015 |
| AT-032 | 仅 API 添加入站后重启 Core；受管配置加 usersync 再启动 | 不承诺 API 持久化；按权威配置与同步恢复；无残留监听 | FR-015 |
| AT-033 | 在受管连接计数边界记录字节；单会话多流、两用户并发、改变 padding | inbound 增量与边界观测值完全一致；每个字节只计一次，user payload 不受 padding 影响 | FR-016 |
| AT-034 | 分别开关 inbound/user/domain 统计，由 gRPC 和 Sidecar 查询 | 三维上下行按各自开关工作，无 AnyTLS unknown、缺失或重复计数；关闭不创建对应计数 | FR-016 |
| AT-035 | 同 email 跨 SS-2022/AnyTLS；包含可归属域名与纯 IP 流量 | inbound 分开计、user 合并计；域名仅统计可归属部分，不伪造域名补齐总量 | FR-016 |
| AT-036 | 隔离测试中同数字端口启动 Hy2 UDP 与 AnyTLS TCP；另测已被占用的 TCP 端口 | 双协议可同时连接且统计分开；TCP 冲突时失败并清理，不影响原 TCP 服务或 Hy2 | FR-001/014/015 |

## 压力与资源门槛

默认预算已定稿，见 [resource-audit.md](resource-audit.md)；以下清理与隔离门槛不可省略：

1. 单元/并发测试启用 `-race`；认证解析、地址解析、padding/帧输入进行至少 60 秒
   定向 fuzz，保存崩溃种子，协议依赖自身测试不能代替适配层测试。
2. 两个用户各运行 50 条并发流至少 10 分钟，使用双客户端分别执行；校验数据散列、
   出口 marker 与每用户增量，无串账、panic、死锁或业务字节丢失。
3. 100 轮连接/关闭后，内部 active session/stream 数回到 0；关闭后 5 秒内测试拥有
   的任务退出，FD 回到基线附近（差异必须可解释且跨轮不增长）。
4. Core Close 后 5 秒内相关 goroutine 退出；用注册表和 goroutine profile 同时核对，
   不要求整个进程的后台任务绝对数量不变。
5. 固定负载预热后采集堆/协程/FD，至少三轮比较；堆缓存可保留但不得随关闭轮次
   线性增长。测得每会话/流增量后记录资源预算，若未定稿不得宣称可生产发布。
6. 慢读/大量新流压力下检测协议库内部缓存，必须能定位最大队列/窗口/缓冲预算；
   发现无法控制的无界缓冲应视为选型失败或必须修复，不用 OS OOM 限制代替解决。
7. 显式测试 Linux amd64 等价的 dispatcher buffer policy，而不只依赖本机
   arm64 默认值；UoT 多目标共享预算的实测见 [resource-audit.md](resource-audit.md)。

逐项执行映射见 [acceptance.md](acceptance.md)。认证 prologue 使用独立的
`FuzzAuthenticationPrologue`（输入覆盖摘要、padding 长度/内容及分片读取），
不以固定认证头的帧 fuzzer 冒充认证覆盖。

## 证据格式与测试分层

报告记录：仓库 commit、客户端版本/hash、平台、配置夹具标识、场景 ID、出口 marker、
期望/实际字节增量、资源曲线、失败原因和残留清理结果。不得打印完整账户配置。

Core 的 Go 单测放在 `proxy/anytls/*_test.go` 和必要的公共包；Sidecar 测试留在 sibling。
跨组件自动化置于 `../xray-config/test/`，由该仓库测试入口组织，不散落在生产节点目录。
集成测试若依赖客户端缺失，显式报告 skipped 和原因；最终 SC 不允许以 skipped 通过。

复现帧 fuzz 时加 `-fuzzminimizetime 1s`，避免默认缩减阶段长时间占用整个测试预算。
示例：`go test ./proxy/anytls/internal/engine -run '^$' -fuzz FuzzServerFrames -fuzztime 60s -fuzzminimizetime 1s -parallel 2 -timeout 90s`。
Core 有进程级全局 dialer 状态，双客户端压力夹具必须共用一个 Core、使用不同用户；
不能并行初始化/关闭两个嵌入 Core 实例后将其干扰误判为 AnyTLS 协议失败。
