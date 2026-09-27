# Feature Specification: AnyTLS 入站与用户可观测性

- Feature ID: 008-anytls-inbound
- Renumbered: 2026-09-27，按用户要求由 003 迁至最后编号 008；需求、测试编号及历史部署事实不变。
- Created: 2026-09-25
- Status: Implemented (internal pilot only)
- User intent: 在 Xray-core 增加 AnyTLS 入站，支持按用户分流和统计；明确不需要 outbound。
- Baseline: core `1e0c6a30f2a33080b4ce55121e2e414a09f99bec`；sidecar `95d1e30d2327b9ad5d80ed7c1a5f06306a170646`。
- Implementation status: Core/Sidecar 已有本地实现和测试，已部署 SHI/ZDS 内部使用，未公开发布。
  实际证据见 [implementation.md](implementation.md)，逐项映射见 [acceptance.md](acceptance.md)。
- Authorization: 2026-09-25 用户已授权先整体 commit/push，再开始实现，并使用本地
  sing-box 和一台 RFC TK CO 验证；端口已由最初的 2053 更正为 TCP 2083。
  最初选 `tk.rfc.co.micro.shi`；2026-09-26 用户追加授权 SHI/ZDS 两节点部署，
  用户和路由对齐各自标准 Hy2。仍未授权迁移既有入站、修改订阅或公开发布。

阅读顺序：[调研](research.md) → [契约与数据模型](contracts.md) →
[实施计划](plan.md) → [测试方案](tests.md) → [任务](tasks.md) →
[检查清单](checklists/requirements.md)；远端试点遵循 [试点与验证](pilot.md)。

## 问题与目标

通用分叉与附带修复的完整清单见 [001](../001-core-fork-contracts/spec.md)。
AnyTLS 合并提交中的 DNS、XHTTP、VLESS、manager 和测试设施改动由其 K2/K6
独立验收；不能仅凭本协议互通通过就认定这些附带改动通过。

允许支持 AnyTLS 的外部客户端连接我们的 Xray，认证后继续使用现有静态路由、
StatsService 和用户维度域名统计。无需在 Xray 前额外部署 sing-box 进程，
也不另建 AnyTLS 专用的用户数据库或统计体系。

AnyTLS 会在一条经过认证的 TCP/TLS 会话内承载多个逻辑流。路由和业务流量统计
必须发生在逻辑流层，而非仅在外层连接上。UDP 通过兼容客户端的 UoT 封装承载，
在服务端解封装后仍按 UDP 目的地进入 Xray 路由。

## 用户场景与验收

### US-001 — 多用户认证与分流（P1）

独立验收：仅运行本地 Core 入站、两个外部客户端和两个测试出口，不依赖 Portal。

1. Given 两个不同密码对应不同 email，When 同时访问同一目的地址，Then 按各自
   email 命中不同出口，不能使用客户端伪造的用户名改变身份。
2. Given email 为 `alice@la.aaitr.att`，When 存在 `domain:la.aaitr.att` 路由，
   Then 命中现有 user-domain 规则，不改变该规则的语义。
3. Given 一条已认证会话，When 同时打开多个目的地的流，Then 每条流独立分流，
   关闭其中一条不影响其余流；错误密码不能到达任何业务出口。

### US-002 — 用户流量与域名流量（P1）

独立验收：读取现有 StatsService 和 DomainTraffic 接口，不依赖新增 UI。

1. Given 已开启对应 policy 统计，When 用户传输已知数量的业务字节，Then 其
   `user>>>email>>>traffic>>>uplink/downlink` 增量准确，其他用户不增长。
2. Given 已开启域名统计，When 两个用户访问同一域名，Then 能分别查到两人的
   域名流量，现有域名汇总等于明细之和；不新增特殊聚合维度。
3. Given 域名统计关闭，When 使用 AnyTLS，Then 代理和常规用户计数正常，
   不绕过现有开关强行创建域名统计。

### US-003 — 无重启用户管理（P1）

独立验收：先以 gRPC 操作测试，再以 Sidecar usersync 验证同样行为。

1. Given 入站运行中，When AddUser 成功，Then 新用户无需重启即可认证。
2. Given 用户持有存活的复用会话，When RemoveUser 成功，Then 该会话被撤销，
   不再允许建立新业务流；已有流被关闭而不是继续无限传输。
3. Given 用户被删除后以同名新密码添加，When 旧会话或旧密码再请求，Then
   仍然拒绝；新凭据可连接，不因 email 相同复活旧会话。
4. Given usersync 配置 SS-2022 和 AnyTLS 两类入站，When 同步用户，Then
   按实际入站类型构造账户；既有 SS-2022 行为保持不变。

### US-004 — UDP 与异常连接（P1）

独立验收：外部客户端通过 AnyTLS 发起受控 UDP 回显和 DNS 查询。

1. Given UDP-over-TCP 流，When 访问不同 UDP 目标，Then 用户身份、目标地址、
   包边界和回包源地址正确；网络规则按 UDP 而非外层 TCP 匹配。
2. Given 慢速、不完整或非法协议输入，When 到达握手/资源限制，Then 有界拒绝，
   不阻塞其他用户，不留下持续增长的连接、协程或缓冲区。

## 功能需求

- FR-001: 仅新增 AnyTLS inbound，复用 Xray TCP/TLS 监听与证书体系。
  配置必须遵循 Xray 原生分层：通用监听、settings、streamSettings、policy、routing、
  stats/api 各归其位；不得照搬 sing-box 配置或在协议内重复建立这些通用能力。
- FR-002: 配置采用 Xray `clients` 中的 `email`、`level`、`password`；email 和
  password 必填，同一入站内 email、认证密码均唯一；冲突必须拒绝。
- FR-003: 每个逻辑流建立独立 session 上下文，携带认证用户、真实来源、入站 tag
  和实际目的地，进入现有 Dispatcher；保持嗅探、域名路由和用户路由语义。
- FR-004: TCP 用户上下行使用现有 policy 和 StatsService，禁止重复计数。
- FR-005: 接入现有用户×域名统计及开关，不改变存储、桶、快照和淘汰契约。
- FR-006: 实现 `proxy.UserManager` 的增删查及数量接口，使用现有 HandlerService。
- FR-007: 用户撤销与会话注册必须原子协调；密码替换撤销旧 generation 的全部会话。
- FR-008: 支持与选定外部客户端兼容的 UoT v2；解包后按实际 UDP 目标逐一调度，
  同一 UoT 流更换目的地不得沿用错误出口；不支持的封装显式拒绝。
- FR-009: 复用 Xray policy 超时；外层握手、逻辑流和会话资源均有有界控制。
- FR-010: AnyTLS 内建复用与 Xray Mux 分离；不将逻辑流再次当作 Xray Mux 协议处理。
- FR-011: Sidecar 根据控制面入站类型创建 AnyTLS Account，沿用既有身份渲染和
  同步机制；不能把 SS-2022 Account 发给 AnyTLS。
- FR-012: 正确关闭监听、TLS 连接、会话和逻辑流；服务停止后所有后台任务可退出。
- FR-013: 控制面识别协议为 `anytls`，日志可关联 email/tag/目标，禁止输出密码、
  密码摘要、TLS 私钥；沿用现有证书可观测性，不能重新扫描磁盘冒充已加载证书。
- FR-014: 无 AnyTLS 配置时不启动相关服务，不更改已有协议配置和路由。
- FR-015: AnyTLS 必须完整接入现有 gRPC 控制面，包括 AddInbound/RemoveInbound、
  ListInbounds、AlterInbound 用户增删、GetInboundUsers/GetInboundUsersCount。
  ServerConfig/Account 的 protobuf 类型必须在 Core 和 Sidecar 正确注册和解码；
  不能只支持 JSON 启动而在控制面显示 unknown、无法管理用户或读取配置。
  入站生命周期、失败回滚和配置持久化边界见 contracts.md。
- FR-016: 完整支持 inbound tag、user email、user email + domain 三个维度的
  上行/下行统计。入站总量沿用 proxyman 连接计数器和 policy.system 开关，不能因
  AnyTLS 接管连接而绕过；用户与域名量沿用 Dispatcher，不按复用流重复累加入站量。
  三个维度均须经现有控制面和 Sidecar 验证可读，计数口径和覆盖边界分别验收。

## 非功能需求

- NFR-001: 多用户、同会话多流、增删用户并发测试通过 `go test -race`。
- NFR-002: 超限拒绝不得拖垮普通用户；配置有限的会话/流上限，且不出现无界排队。
- NFR-003: 测试需报告内存、goroutine、FD、连接数和清理后的残留，不仅验证连通性。
- NFR-004: 锁定协议依赖版本，确认许可证方案；对受影响的 sing bridge、SS-2022、
  用户管理和统计执行回归，不为追新无边界升级整套依赖。
- NFR-005: 第一版至少与锁定版本的 sing-box、Mihomo 真实互通；不能只用自产客户端
  证明协议正确。生产构建仍需 Darwin/Linux 双平台和 VCS 元数据。

## 非目标

- 不实现 AnyTLS outbound，也不添加 Core/Sidecar 的 AnyTLS 出站 probe。
- 不新增订阅协议、分享链接、Portal AnyTLS 管理页或数据库字段。
- 不迁移 SS-2022、Hy2、VLESS 用户，不更改现有端口、DNS、证书和生产拓扑。
- 不支持额外的 Xray Mux、Vision、REALITY、WS/XHTTP 或明文公网入站组合。
- 不承诺 TLS 流量具有全随机外观，不承诺解决所有 TCP/UDP 网络过滤。
- 不重写 DomainTraffic 的容量、分桶、聚合、保留时间或磁盘快照策略。

## 边界与决策

email 是服务端标识，不是 AnyTLS 客户端在线路上声明的可信身份。一个外层认证
会话只属于一个账户，同一会话内的流不得切换用户。

删除用户默认采用严格撤销：禁止新流并关闭已有会话/流。操作成功之后可能仍有
已在途字节完成计数，但不能产生新获准路由。细节见 [契约](contracts.md)。

静态用户及动态用户的归属遵循既有 usersync 所有权规则；不得因扩展协议删除
不受同步管理的 admin 用户。数据库不用为这个协议另开一套用户表。

内部试点的依赖选型与许可边界已确认，资源预算已验收，公开分发许可审查仍需闭环，
详见 research；不得宣称具备可公开发布实现，不能默认用完整 sing-box 作为 Core 依赖。

## 完成标准

- SC-001: US-001 至 US-004 和 FR-001 至 FR-016 均有通过的可复现测试证据。
- SC-002: 双客户端 TCP/UDP 互通、精确计数、用户撤销和并发隔离通过。
- SC-003: Core 与 Sidecar 既有相关功能回归通过，Darwin/Linux 构建可追溯。
- SC-004: 资源压力和退出清理达到 tests.md 门槛；无未解决的身份绕过或重复计数。
- SC-005: 控制面沿用现有用户/统计 API；没有引入 outbound 或生产迁移副作用。

## 授权与修订记录

- 2026-09-26: 本地功能/资源/fuzz/恢复验收完成，Core `187bf0ab`、Sidecar `663362e`
  经批准入口构建并部署 SHI；完整十分钟复验与清理通过。状态改为 Implemented，
  仅表示已授权内部试点完成；公开分发、其他节点推广与订阅接入均未执行。
- 2026-09-26: 用户接受保留 GPL-3.0-or-later 引擎的内部试点，公开分发另行处理。
  此决定不代替技术验收，也不授权 push 含该实现的代码或公开发布二进制。
- 2026-09-25: 用户确认不需要 outbound，要求详细 spec；保持 Draft，只写文档。
- 2026-09-25: 用户明确配置文件遵循 Xray-core 设计；补充原生分层、公共 User/Account
  构建契约及配置回归要求，不改变仅规格的授权范围。
- 2026-09-25: 用户明确要求控制面支持 AnyTLS；新增 FR-015 和对应实际 gRPC 验收。
- 2026-09-25: 明确 inbound/user/user+domain 三维流量统计，新增 FR-016 与计数口径测试。
- 2026-09-25: 用户确定建议端口与 Hy2 对齐，AnyTLS TCP / Hy2 UDP 互补；
  NAT 分协议映射，部署前检查 TCP 占用，不授权当前执行端口或生产变更。
- 2026-09-25: 后续用户明确授权实现和单节点测试。现有改动已提交并推送 `b7e306a`。
  选定 `tk.rfc.co.micro.shi`，通过 CTC 核实 UDP 2083 有监听、TCP 2083 无监听；
  AnyTLS 尚未部署。状态改为 Approved，完整验收和技术选型仍未完成。
