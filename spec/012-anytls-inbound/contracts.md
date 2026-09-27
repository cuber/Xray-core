# 契约与数据模型

此文是验收契约，实际实现及 SHI 内部试点进度见 implementation.md；不授权其他节点推广。

## 配置形态

### Xray 原生配置分层（强制）

对齐 `infra/conf/trojan.go`、`infra/conf/hysteria.go` 的 JSON → protobuf →
MemoryUser 构建方式，不直接暴露 sing-box 或协议库自己的配置结构。

| Xray 配置层 | AnyTLS 使用方式 |
|---|---|
| `inbounds[]` | 复用 `tag`、`listen`、`port`、`protocol`、`sniffing` 等通用字段；不在 settings 里重复定义监听 |
| `settings.clients[]` | 使用 `password`、`email`、`level`，不使用 sing-box 的 `users[].name` |
| `settings` | 只承载协议用户、padding 和必要的 AnyTLS 会话限制，新增 JSON 字段使用 lowerCamelCase |
| `streamSettings` | `network: raw`、`security: tls`、`tlsSettings` 与现有证书配置；不新增顶层 `tls` 或第二份证书字段 |
| `policy.levels` | 按用户 `level` 复用握手/空闲/上下行超时、用户统计及适用缓冲策略；不在 clients 内嵌另一份 policy |
| `routing.rules` | 继续用 `user`、`inboundTag`、`domain`、`network` 等字段，选现有 `outboundTag`/`balancerTag` |
| `stats`、`api` | 沿用现有 StatsService、HandlerService 和 DomainTraffic 开关，不放进 AnyTLS settings |

JSON 的 `clients` 经 `infra/conf` 构建为 `ServerConfig.users`，类型必须是
`repeated xray.common.protocol.User`。其中 email/level 归公共 User，password 归
`serial.TypedMessage` 包装的 `xray.proxy.anytls.Account`，不能在 Account 内重复
保存 email/level。静态配置和 gRPC 动态用户使用同一个 Account 转换与校验路径。

用户分流规则单独配置，例如下列拟议测试规则，不在 client 对象里增加 `route`：

```json
{
  "type": "field",
  "inboundTag": ["anytls-in"],
  "user": ["domain:la.aaitr.att"],
  "outboundTag": "existing-test-outbound"
}
```

`existing-test-outbound` 必须在测试配置里真实定义；它是已有协议的出站，
不是 AnyTLS outbound。示例规则与下方入站对象都只是完整 Xray 配置的片段。

### 建议端口与 Hy2 对齐

部署约定：AnyTLS 使用目标节点 Hy2 的相同数字端口，传输协议为 TCP；
Hy2 保持 UDP。标准主端口建议为 TCP 443 / UDP 443；若节点实际使用 Hy2 2083，
对应 AnyTLS 可用 TCP 2083。仅对齐计划启用的端口，不自动复制所有 Hy2 备用监听。

NAT 节点需分别核对公网和内部映射，例如已有 UDP 公网 P → 内部 Q，AnyTLS 应申请
TCP 公网 P → 内部 Q；UDP 映射不代表 TCP 已开放。安全组、防火墙和供应商映射
均需按 TCP 独立检查，通过现有受管工具配置，不在规格阶段自动执行。

同一 IP/数字端口的 TCP 与 UDP 可以共存，但 TCP 可能已被 Nginx、VLESS、rathole
等服务占用。部署前必须检查真实监听及仓库声明；冲突时停止该端口部署并报告，
不得抢占、停掉已有服务或擅自改变其端口。不引入端口共享/协议嗅探分发器。

此约定不改变 Xray 配置结构：仍由通用 inbound.port 指定端口，不增加 anytlsPort
字段，不把端口写死在协议实现中。下方 14433 仅为隔离本地测试示例，不是生产建议。

### 入站示例

示例仅展示拟议入站对象，端口不代表任何节点将被修改：

```json
{
  "tag": "anytls-in",
  "listen": "127.0.0.1",
  "port": 14433,
  "protocol": "anytls",
  "settings": {
    "clients": [
      {
        "email": "alice@la.aaitr.att",
        "level": 0,
        "password": "<unique-secret-from-test-fixture>"
      }
    ],
    "maxSessions": 256,
    "maxSessionsPerUser": 16,
    "maxStreamsPerSession": 128
  },
  "streamSettings": {
    "network": "raw",
    "security": "tls",
    "tlsSettings": {
      "certificates": [
        {
          "certificateFile": "<test-fullchain.pem>",
          "keyFile": "<test-private-key.pem>"
        }
      ]
    }
  }
}
```

- `clients` 可以为空，方便由用户同步填充；此时所有新认证拒绝。
- email 区分大小写，沿用 Core 用户管理约定；domain: 路由匹配保持现有行为。
- 空 email、空 password、重复 email 或 password、非法 level/负资源值均在配置
  测试时失败。密码按字节使用，不 trim、不隐式派生；不得将测试占位符当真实密码。
- 第一版限定 RAW TCP + TLS，不支持其他传输组合。拒绝明文、REALITY、Vision 和
  AnyTLS outbound；使用真实临时证书完成 `run -test`，不绕过证书校验。
- `paddingScheme` 可选，类型为字符串数组，保持客户端兼容的行格式。
  不配置使用所锁定协议库的明确默认方案；非法配置在启动前拒绝，不静默回退。
- 三个资源字段未设置或为 0 表示采用有限默认值，不表示无限。默认值保持
  256/16/128，另有以下共享准入预算；不以会话数量声称进程 RSS 有硬上限。
- 上述资源字段是新增的 AnyTLS 专有语义。握手、空闲时间和更小的 pipe 缓冲
  继续复用公共 policy，不另造重复开关。协议复用缓冲预算
  与 Dispatcher 的通用缓冲策略分别审计，不能假设后者自动约束前者。
- 外层 TLS/认证握手在用户未知时使用 level 0 的 Handshake 超时预算；认证后逻辑流
  使用账户 level 的策略。外层空闲回收不得误杀仍有活动逻辑流的会话。

当前实现补充：每个 inbound 另有固定的 128 个活动 handler 总预算，每个预留
256 KiB 协议缓冲预算，共 32 MiB；这不是进程 RSS 硬上限，也不包含 Dispatcher、
TLS、UDP 多目标 link 和运行时开销。handler 真正退出后才退还预算。
单 UoT association 最多保留 64 个活动目标，按用户空闲策略回收。
全入站 UoT 目标另受 128 个 link 的共享准入限制；worker 退出并 interrupt
双向 pipe 后释放额度，不能在 map 淘汰时提前释放。Dispatch 失败立即归还。
AnyTLS pipe 阈值最多 32 KiB，覆盖 unlimited 和平台默认，更小的非负 policy
保持不变；不影响其他协议。保守按 128 TCP + 128 UDP link 计算，双向 pipe
标称阈值合计 16 MiB；单次写批可越过阈值，不含 outbound 异步清理及内核开销。
共享预算耗尽拒绝新 UoT association，不保证新请求的用户间公平配额。
已有其他 association 和 TCP 不因该拒绝而被关闭，资源释放后重新允许准入。
UDP payload 支持 1..8192 字节；零长度及更大报文显式拒绝，不静默截断。
这是现有 Core UDP reader 的 8 KiB 边界，不宣称支持最大 UDP 报文。

AnyTLS 创建时对 TLS 服务端证书/私钥做配对校验（包括原生 gRPC 创建），空表、
无效材料或不匹配直接拒绝，不发布无法握手的监听。错误不输出证书/私钥原文。
沿用既有 dispatcher 统计时点：IP 请求嗅探前的第一批上传可能记为 unknown，
嗅探后的下行和后续上传归属域名，不对历史字节做协议专用的追溯重分配。

## 实体与生命周期

| 实体 | 关键字段 | 生命周期 |
|---|---|---|
| Account | password；关联 MemoryUser 的 email/level | 静态配置或 HandlerService 提交 |
| UserRecord | 不可变用户快照、generation、revoked 状态 | 每次添加创建新 generation；删除永不复活 |
| Session | 会话 ID、UserRecord 引用、TLS conn、cancel、流集合 | 认证到关闭；一个会话只能属于一个用户 |
| Stream | 独立 session.Inbound/Outbound/Content、目标、link、cancel | 每个逻辑流一份，关闭只回收自己的资源 |
| UDP association | 用户、UoT 流、目标到路由 link 映射、有界空闲清理 | 不同目标独立路由；流结束关闭所有关联 |

generation 是进程内不可复用标识，不需入库。会话索引为内部结构，不增加公开 API。
统计 key 仍只用 email，不加入 generation/session ID，不造成指标基数膨胀。

## 控制面契约（必须支持）

复用当前 Xray 的 gRPC 服务和请求格式，不新增 AnyTLS 专用控制服务。

| 能力 | 接口/路径 | AnyTLS 验收要求 |
|---|---|---|
| 添加入站 | HandlerService.AddInbound | 接受标准 InboundHandlerConfig，解码 AnyTLS ServerConfig 并启动受管 TLS 入站 |
| 删除入站 | HandlerService.RemoveInbound | 停止接受连接，关闭全部认证会话和流，释放监听和用户注册表 |
| 查询入站 | HandlerService.ListInbounds | 返回真实受管 tag、receiver/proxy settings，ServerConfig 类型可解码 |
| 增删用户 | HandlerService.AlterInbound + AddUserOperation/RemoveUserOperation | 解码 AnyTLS Account，执行与静态配置一致的账户校验及严格撤销 |
| 查询用户 | GetInboundUsers/GetInboundUsersCount | 返回运行时用户快照/数量，能反映动态增删，不能只读启动 JSON |
| 路由 | 既有 RoutingService 与用户匹配 | 每个 AnyTLS 流携带真实用户；无需新增 AnyTLS 专用路由操作 |
| 流量 | StatsService、现有 DomainTraffic 接口 | 同一 email 的统计可被既有客户端查询、聚合，不新增协议专用指标体系 |
| Sidecar 查询 | 既有 xray-debug/协议映射 | 注册对应 proto 类型，正确识别 anytls、读取运行时用户、沿用证书展示；公开响应脱敏 |

Core 与 Sidecar 均要测试 `serial.TypedMessage` 的实际序列化/反序列化；仅添加
协议字符串映射而无法解码 ServerConfig/Account 不算控制面支持。

AddInbound 的端口冲突、证书错误或非法配置必须返回失败并清理部分创建资源；
失败后不能遗留幽灵 handler 或半运行的监听。重名 tag、重复删除和未知用户等
错误沿用现有 HandlerService 语义，不单独发明一套返回码。

这是运行时控制能力，不是新的配置持久化机制：API 增删入站/用户不会自动回写
仓库 JSON 或服务器文件，进程重启后的恢复仍由本地权威配置与 usersync 完成。
不新增修改任意 settings/TLS 的通用热更新 API；端口、证书、padding 等仍遵循
既有配置部署或显式移除/重建流程，不宣称支持无损原地修改。

控制面仍使用原有受保护的监听与访问链路，不为 AnyTLS 额外开放公网管理端口。
Sidecar、CLI 的类型注册更新属于必需联动，但不增加 AnyTLS outbound、订阅生成
或新的 Portal 编辑页。

## 用户管理与并发语义

复用 `AddUserOperation`、`RemoveUserOperation`、`GetInboundUsers` 及现有数量接口；
新增 `xray.proxy.anytls.Account` protobuf 类型。接口名称/错误映射以当前 Core 为准，
不新建独立 gRPC service。

1. AddUser 校验 email/password 唯一性，构造完整新表后原子发布。重复添加返回
   现有约定的冲突错误，不把同名新密码默默当更新；Sidecar 负责比较与收敛。
2. 认证成功准备登记会话时，在受保护区域内再次校验 UserRecord 未撤销；防止
   RemoveUser 与认证交错，出现删除后才登记的漏网会话。
3. RemoveUser 先使账户失效并切断新流准入，再摘除其会话集合；锁外关闭连接和流，
   避免持锁阻塞网络 I/O。操作成功意味着撤销状态已发布且关闭动作已执行。
4. 关闭导致的清理任务允许短暂退出过程，但测试要求 5 秒内回收；已在途传输不能
   被表述为可追溯撤回。新流准入以同一撤销同步点为线性化边界。
5. 更换密码沿用 remove/add 路径，允许短暂不可用；不承诺跨两个 RPC 的事务。
   新 generation 不得让旧会话恢复权限，即使 email 或密码再次被复用。
6. Remove 不存在用户沿用现有 not-found 语义；同步器将已不存在视为收敛，
   不能在网络/服务错误时误记成功。GetUsers 返回快照，不暴露可变内部表。

默认采用严格断流，不添加保留旧连接的兼容开关。

## 路由与流量统计

三个必需维度如下，均支持 uplink/downlink；方向以服务端入站为基准，上行是客户端
向代理发送，下行是代理返回客户端。速率由现有消费方计算计数差分，不另存一套速率。

| 维度 | 控制面数据 | 开关和计数位置 |
|---|---|---|
| inbound | `inbound>>>tag>>>traffic>>>uplink/downlink` | `policy.system.statsInboundUplink/Downlink`；沿用 proxyman 的 stat.Connection |
| user | `user>>>email>>>traffic>>>uplink/downlink` | `policy.levels[level].statsUserUplink/Downlink`；Dispatcher 的业务流 link |
| user + domain | 现有 DomainTraffic bucket 中的 user/domain/up/down | 现有域名统计开关与 Dispatcher 记录器；不新增每域名 StatsService counter |

AnyTLS 必须使用 proxyman 交付的受管连接，不剥离 stat.Connection 后直接读写底层
socket。入站计数是这层连接实际读写的字节，包含在该层可见的认证、padding 和
AnyTLS 帧开销；不是网卡字节，也不承诺包含外层 TLS/IP/TCP 头或重传。
精确 TLS 包装顺序以现有 worker/transport 为准，测试须观测此边界而非猜测。
一个外层连接的同一批数据只能在入站计数一次，不能为每个内部流再加到 inbound。

user 的 email 计数保持 Core 的跨入站汇总语义：同一 email 同时使用 SS-2022 和
AnyTLS 时汇入同一个 user counter。user+domain 同样沿用既有身份键，不暗中引入
inbound+user 或 inbound+user+domain 的新维度。

域名量仅覆盖能归属域名且被现有记录器接受的流量。纯 IP、无嗅探线索、容量淘汰
或历史桶过期时，不能强制要求域名汇总等于用户总量；只有完整覆盖的受控夹具才
做严格等式检查。三类开关独立测试，不因关闭 user counter 而误关 DomainTraffic。

- 入站建立认证身份后，按每个逻辑流的实际目标调用 Dispatcher；不得直接 net.Dial
  绕过 routing、policy 和统计。
- TCP 通过既有 Dispatcher link 的用户计数器，计业务字节，不含外层 TLS、认证、
  padding 和 AnyTLS 帧头。避免同时手动加同一计数器。
- UoT 解包后计 UDP payload，保留报文边界；UoT 帧头、目的地址头不计入用户业务量。
- inbound/outbound 总计数沿用原有层级与口径，可能包含不同开销，不能要求它们与
  用户 payload 汇总精确相等。验收分别解释每种计数器。
- 域名使用现有 sniffing/路由目标解析结果；仅 IP 且没有域名线索时，不添加 PTR
  查询或伪造域名归属。UoT 特殊标记域名不得进入用户域名排行榜。
- 用户×域名汇总完全交由现有 Core → Sidecar 流程，沿用 epoch/sequence、重启恢复、
  聚合桶和快照行为，不重复实现。

## Sidecar 边界

仅扩展 usersync 的账户构造和必要的协议识别，不扩展订阅输出、probe 链或 UI。
从已获取的 Core 入站配置识别协议类型，按 tag 缓存已确认的类型；Core 重连或
配置刷新时失效重查。未知类型必须报错并跳过写入，不能默认当 SS-2022。

新增账户 factory：SS-2022 维持现有 key 语义；AnyTLS 将同一受管用户的现有派生
密钥字符串作为 password，email/route/level 保持一致，不新增派生算法或数据库列。
同一个 AnyTLS 入站里不能用一个相同密钥同时创建多个路由身份；发现这种同步计划
必须预检失败，不能随机保留其中一个。需要并存时应另行设计身份专属密钥。

同步使用显式选择的入站集合，保留现有静态/admin 用户保护。两种协议分别报告
成功/失败，单个入站失败不能误报整批已完成；重试可幂等收敛。

## 错误与安全

- 未认证/密码错误：关闭，禁止进入 Dispatcher；对端不获知哪个 email 存在。
- 非法帧/超大长度/非法 padding：有界拒绝；日志不输出原始认证数据或业务包。
- 超限：拒绝新会话/流而非无界等待，已有合规流尽量不受影响。
- TLS 证书仍由现有 listener 加载和观测，证书更新沿用现有受管流程。
- 不提供默认明文 fallback 代理，不因认证失败转发到任意目标。
- GetUsers/Account 的敏感字段访问遵循既有受保护控制面；日志和公开状态必须脱敏，
  不擅自破坏现有管理 API 的账户序列化契约。
