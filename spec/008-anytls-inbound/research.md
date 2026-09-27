# Research: 现状、选型与风险

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

调查时间：2026-09-25。以下为源码观察，不是生产支持声明，也不是已完成互通测试。

## 已核实事实

| 对象 | 证据 | 结论 |
|---|---|---|
| sing-box 最新稳定版 | `v1.14.2`，本地已 fetch tag | 使用 `github.com/anytls/sing-anytls v0.0.11` |
| sing-box 开发分支 | `testing b609f95`，本地 `/Volumes/Linux/opensource/sing-box` | 改用 `github.com/sagernet/sing-anytls`，不是与稳定版相同的实现 |
| 新协议库 | `7ca72921ac6a`，本地 `/Volumes/Linux/opensource/sing-anytls` | MultiService、UpdateUsers、每条内部流传递认证 context |
| 新库依赖 | `go.mod` | Go 1.24，`sing v0.8.14`；不能假设兼容 Core 当前依赖 |
| Core 现有依赖 | `../xray-core/go.mod` | `sing v0.5.1`、`sing-shadowsocks v0.2.7` |
| Core 用户路由 | `features/routing/session/context.go`、`app/router/condition.go` | 读取 inbound.User.Email，支持精确及 domain: 匹配 |
| Core 统计 | `app/dispatcher/default.go`、`stats.go` | 现成用户上下行和用户×域名包装器 |
| Core 接口 | `proxy/proxy.go`、`app/proxyman/command/command.go` | 现成 UserManager 和 HandlerService 操作 |
| Core 桥接 | `common/singbridge/`、`proxy/shadowsocks_2022/inbound_multi.go` | 可借鉴适配模式，但老接口不是新库的直接替代品 |
| Sidecar 同步 | `../xray-sidecar/usersync.go` 的 addUserToInbounds | 当前硬编码 SS-2022 Account，不能直接同步到 AnyTLS |

外部参考：

- [sing-box 稳定版入站](https://github.com/SagerNet/sing-box/blob/v1.14.2/protocol/anytls/inbound.go)
- [sing-box 开发版入站](https://github.com/SagerNet/sing-box/blob/b609f95/protocol/anytls/inbound.go)
- [新库认证和流处理](https://github.com/SagerNet/sing-anytls/blob/7ca72921ac6a/service.go)
- [原始协议库](https://github.com/anytls/sing-anytls)

## 关键发现

### 用户撤销并不等于替换认证表

新库 UpdateUsers 原子替换 password hash → user 映射，但已认证会话仍保留原来的
context。仅调用它不能保证旧会话不能开新流。适配层必须持有账户 generation、
活动会话集合，并在每次接受流时检查 generation；严格撤销还需关闭相关连接。

新库直接将重复密码映射覆盖，因此适配层必须在提交前拒绝重复密码，不能依靠
库替我们验证用户唯一性。密码摘要也属于认证秘密，不应进入日志或指标。

### 复用流必须隔离可变状态

流的 context 可以继承不可变账户快照和父会话取消信号，但 inbound、outbounds、
content/sniffing、日志访问记录和路由目标必须独立。不能对父 context 中共享的
session 指针就地修改。实现前逐项审计 Dispatcher 需要的上下文，不能仅复制 User。

### UDP 不是额外开放 UDP 监听端口

sing-box AnyTLS 使用 TCP 上的 UoT。要把 UoT 请求识别、读写和目的地址转换后再交给
Xray UDP 路由，不能把 UoT 的特殊目标当成普通域名送去 DNS 或 freedom 出站。
第一版明确支持 UoT v2；实际客户端版本和包格式由测试夹具锁定。

### 协议库不能未经评估直接接入

新库改动涉及会话生命周期和 UoT 修复，开发版不等于稳定兼容保证。
sing-box 和两个候选 AnyTLS 库的 LICENSE 均标注 GPL-3.0-or-later；Core 使用 MPL-2.0。
这里只记录原文差异，不作许可证兼容性的法律结论；公开分发前必须明确可行的
依赖/授权方案并保留通知。不得复制代码后假定原许可证失效。

## 待决项与阶段闸门

本地实现选择 `SagerNet/sing-anytls@7ca72921ac6aad397765670b6357fba7e67cc300`，
在 `proxy/anytls/internal/engine` 保留 GPL-3.0-or-later LICENSE 与 ORIGIN.md，
增加认证、准入、逐用户握手超时及关闭回收 hooks。不依赖完整 sing-box。
Core 的直接依赖改动仅为 sing v0.5.1 -> v0.8.14；sing-shadowsocks 维持 v0.2.7。
此选择用于本地实现验证，不等于已经解决公共分发许可问题，也不能宣称 MPL-only。

| 编号 | 待决事项 | 影响与处理 |
|---|---|---|
| D-001 | 内部试点已获接受；公开分发许可方案仍待闭环 | 2026-09-26 用户确认使用上述 pinned vendor 进行内部试点，保留原许可证与来源；不 push 含该实现的代码或发布二进制，不宣称 MPL-only；公开分发另行处理 |
| D-002 | sing 升至 v0.8.14，局部 AnyTLS 适配 | 最小依赖 diff、Core 相关测试和 Sidecar 全量 race 通过；既有全 Core 基线失败见 implementation.md，不声明全库绿 |
| D-003 | 资源上限已定稿 | 128 handler / 128 UoT link / 32 KiB pipe 上限，真实多用户饱和/回收及双客户端十分钟验收通过，见 resource-audit.md；不是进程 RSS 硬上限 |

协议范围、统计复用、严格用户撤销已实现；内部试点授权无需重复确认。
完整验收映射见 [acceptance.md](acceptance.md)，公开分发仍不在授权范围。
