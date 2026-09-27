# 单节点试点与验证

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

状态：2026-09-26 后续用户明确授权扩展到 SHI/ZDS，对齐标准 Hy2 用户和路由。
下方单节点空账户记录保留为历史验收；当前配置及扩展结果见文末。
用户后续明确授权将 Core 合并为单笔 `aab372dd` 并推送 `cuber/develop`；
许可证兼容性仍未因此完成审查，未授权公开发布二进制。

## 双端口及 phk.nano 扩展（2026-09-27）

当前目标：HK CO MINI/VGD、TK CO ZDS/SHI 的 AnyTLS 保留 TCP 2083 并增加
TCP 443；phk.nano 新增相同双端口入口。phk.nano 的用户、证书和入站路由
从本节点标准 Hy2 对齐，保留原有 SS2 和 Hy2，不改变出站或调度策略。
Core 使用 `e7a21974`，phk.nano 同时升级 Sidecar 至 `7c2b55b`。

静态验收使用五节点 Hy2/AnyTLS 一致性测试；独立 sing-box 烟测通过
`test/tools/anytls_routes.py --port 443 <nodes...>` 和 `--port 2083` 分别验证。
Grafana 入站枚举仍由 xrayctl 生成和部署。以下旧端口/版本记录是历史验收。

验收：五台远端 `Configuration OK`、服务 active、配置 SHA-256 与本地一致；
Core 审计均为 `e7a2197`，phk.nano Sidecar 审计为 `7c2b55b`。
TCP 443 全账户 54/54 返回 204，TCP 2083 五台 admin 5/5 返回 204。
两台 TK 的 GLBB trace 均为 `123.100.136.215`，五台 ATT trace 均为
`75.229.215.15`；phk.nano admin trace 为 `119.28.88.143`。
仅验证上述代表性最终出口，不将全账户 204 等同于所有出口业务验收。
Grafana 已经 `xrayctl grafana check/deploy --dashboards-only` 生成并部署。
本次 phk.nano 因 pgz.nano SSH 不通，部署临时经 pgz.luck 内网进入；
没有改写 SSH 软链接或永久管理链路。

## HK CO 扩展（2026-09-26）

`hk.rfc.co.mini`、`hk.rfc.co.vgd` 使用 TCP 2083 的 `anytls-in`，
各复制标准 `hy2-in` 的 10 个用户（完整 auth 作为 password）和 sniffing，
复用本机 TLS 证书；不复制 Hy2 的 h3 ALPN。10 条 Hy2 入站规则追加
`anytls-in`，顺序、条件、出站保持不变。Hy2 UDP、SS2022、VLESS 均保留。
Core 需先从旧版升级到 `aab372dd`，Sidecar 已为 `2da764d`。
通过 `test/routing/test_tk_co_anytls.py` 检查账户/路由一致性，
`test/tools/anytls_routes.py` 支持这两台的独立 sing-box 全账户烟测。

验收：两台 Core `aab372dd`，远端配置测试及安装后 SHA-256 核对通过；
20/20 用户返回 204。两台 ATT 用户均回显 `75.229.215.15`，admin 分别回显
MINI `23.249.19.253`、VGD `177.2.20.211`。这只验证代表性出口，未对全部
业务用户逐个验证最终出口。Grafana 入站枚举通过生成器同步。

## Surge 间歇失败调查（2026-09-26）

本节记录修复前证据；后续回收修正及验收见下节。短时间连续成功或重启恢复
不能作为验收通过。

- SHI 临时诊断 Core 为本地单笔 amend 的 `8a12eafe`，尚未推送；其他节点
  不因此视为已升级。诊断只增加每用户会话限额拒绝日志，不改变准入规则。
- SHI journald 使用 `MaxLevelStore=warning`，此前 Core stdout 默认 info
  会丢失文本中的 Warning。节点跟踪的 systemd 单元增加
  `SyslogLevel=warning`，已通过 `xrayctl deploy --systemd` 部署。
  这会同时保存 stdout 上的 access 日志，不能把它理解为按日志文本过滤。
- 19:36:29 至 19:36:47 使用实际 `surge-cli test-policy
  la.aaitr.att-tk.rfc.co.micro.shi-hy2`，间隔 2 秒，10/10 失败。
  服务端 19:36:31、19:36:37 同期记录
  `user="la.aaitr.att" sessions=16 limit=16 idle=10 connections=29`。
- Surge 日志显示 TCP/TLS 成功，发送认证和 stream #1 后收到
  `Read stream EOF`，尚未收到服务端 settings/SYNACK。因而这批失败发生在
  AnyTLS 准入阶段，而不是目标 Sidecar 的 HTTP 204/keepalive 处理阶段。
- 服务端 `ss` 和本机 `lsof` 都仍显示 Surge 的多条 ESTABLISHED TCP2083
  连接。至少存在真实保留连接，不能仅凭限额日志断言会话计数泄漏。
  后续拒绝日志仍为 16 个会话，其中 11 个已无业务 handler。
- 当前会话空闲截止采用用户 policy 的 ConnectionIdle，未覆盖时为 300s；
  HTTP Sidecar 的 600s idle_timeout 是另一层，不能混为一谈。
- 另观察到连接 `SGAnyTLSConnector-268023`（SHI）在 19:37:55 发送 FIN，
  随后记录 `wait server FIN` 并进入客户端池。当前引擎 `closeByPeer`
  使用 `finishStream(id, false)`，不回发 FIN。这只是复用兼容性线索，
  尚未证明它是积累会话的根因；需要隔离的协议测试和真实 Surge A/B 验证。

下一步验收：确认 FIN/复用回收语义，覆盖空闲池占额、活跃会话不能被误杀、
并发新连接与旧流关闭竞态，再重复 Surge 2s 间隔测试和真实业务并行测试。
不要仅增加 maxSessionsPerUser 或取消限额掩盖问题。

### 回收修正验证（2026-09-26）

- Core 单笔 amend 为 `e7a21974`；本地 Darwin/Linux 均由 xrayctl 构建。
  移除默认每用户 16 配额，加入独立 30s 双观测业务空闲回收，保留总体预算。
- FIN 不回发符合官方规范及 sing-box/Mihomo 参考实现，未修改 FIN 行为。
- `go test -race ./proxy/anytls/... ./common/mux ./infra/conf` 全部通过；
  `go vet ./proxy/anytls/...` 通过；独立 sing-box/Mihomo 客户端互通和
  `TestVLESSAfterAnyTLSRemoval` 通过。未因此宣称本次重新执行整库测试。
- SHI 经 xrayctl 部署 Core 和配置，远端版本 `e7a2197`；配置 SHA-256
  `da491c76a0bb19d70364dd80250b413e64d598358f343ed5be2f5d401333923c`。
- 线上真实 TLS 连接只发送 AnyTLS 心跳，收到 29 次响应后在 30.94 秒关闭，
  随后新连接认证并收到 ServerSettings。证明心跳不阻止回收，重新连接可用。
- 独立 sing-box 经 SHI 的 ATT 用户连续 5 次出口为 `75.229.215.15`；
  服务端 Core/Sidecar 均 active，观察期间未出现新的 user session limit 日志。
- 四台 RFC CO（SHI、ZDS、HK MINI、HK VGD）均已通过 xrayctl 部署 Core
  和配置，最终 `xrayctl version` 全部 CURRENT `e7a2197`。四台配置均移除
  显式每用户 16 会话限制；总会话、并发 handler 和 UoT 预算仍保留。
- SHI 实际 Surge CLI 两条 AnyTLS policy（自身及 ATT）各 60 次，按 2 秒
  轮次检测，共 120/120 成功；自身 RTT 通常 58-62ms，ATT 通常 166-173ms。
  对照修复前同一 ATT policy 连续 10/10 EOF，此轮未再复现准入失败。
- ZDS/VGD 自身及 ATT 四条实际 Surge policy，另一次完整记录的 10 轮复测
  共 40/40 成功。此前 30 轮测试的最终汇总输出未保留，不将其计入成功总数。
  VGD ATT 仍有约 1.2s 的偶发 RTT 峰值，未定位原因，不宣称延迟问题已解决。
- HK MINI 的 Surge ATT policy 仍是 Hy2，不能当作 AnyTLS 验收；改用独立
  sing-box AnyTLS 连续 20/20 成功，HTTP 204 及 ATT 出口 `75.229.215.15`
  均符合预期，未为此修改 Surge 配置。
- 真实 TLS 上同一个 AnyTLS stream 在 0/20/40/60 秒发送 HTTP 请求，4/4
  返回 204，跨越两个回收周期未被误杀。发送 FIN 后继续心跳，在 60.71 秒
  后关闭；此时间包含 handler 退出及两次 30s 观测，不是严格 30s 断线。
- 本次 Core 仍只有相对父提交 `1e0c6a30` 的一笔 amend，工作树干净，未 push。
  以上仅证明已复现的空闲占额/准入 EOF 在当前回归中消失，不是长期无故障保证。

## 固定范围

| 项目 | 约定 |
|---|---|
| 服务端 | `tk.rfc.co.micro.shi`，另一台 `tk.rfc.co.micro.zds` 不变更 |
| 连接地址 / TLS SNI | `shadow.tk.rfc.co.micro.shi.hourui.de` |
| AnyTLS 入站 | `anytls-in`，监听 TCP 2083；静态用户为空，首条规则默认阻断 |
| 保留服务 | Hy2 UDP 443/2083、所有备用入站和 SS-2022 TCP/UDP 2053 均不迁移 |
| 测试客户端 | 本机独立 sing-box 进程，不使用本地 Xray AnyTLS outbound |
| sing-box 源码 | `/Volumes/Linux/opensource/sing-box`；稳定版基线 v1.14.2，运行前记录实际版本、提交和二进制 SHA-256 |
| SSH 验证路径 | `ssh -J hk.rfc.t1.ctc tk.rfc.co.micro.shi`，目标 SSH 8822 |
| 凭据 / 临时状态 | 忽略目录 `.cache/anytls/pilot-*`，目录 0700、秘密文件 0600；不进 Git/日志；备份在 `shi-backup/` |
| 隔离客户端监听 | 动态选择空闲的 127.0.0.1 高位 SOCKS 端口；不占用正式 8080、1089、1090、1465 |

服务端复用当前节点证书，不新增 DNS 或另签测试公网证书：

```text
/etc/letsencrypt/live/shadow.tk.rfc.co.micro.shi.hourui.de/fullchain.pem
/etc/letsencrypt/live/shadow.tk.rfc.co.micro.shi.hourui.de/privkey.pem
```

以上路径来自仓库节点配置，已通过配置加载和独立客户端实际 TLS 验证；到期 2026-11-08。
客户端必须使用正常 CA/SNI 校验，不能以 insecure 掩盖证书问题。

## 已完成的只读核查（2026-09-25）

- 两台 TK CO 本地声明均为 `hy2-in` UDP `443,2083`，`ss2-in` 使用 2053。
- 默认 SSH 路径访问两台均在 banner 阶段超时；SHI 的 `ssh -G` 显示 ProxyJump
  为 `pgz.nano`。没有将这个配置当成当前可用访问路径。
- 改用显式 CTC 跳板登录 SHI 成功，执行：

```bash
ssh -o ConnectTimeout=10 -J hk.rfc.t1.ctc tk.rfc.co.micro.shi \
  'ss -H -lntu "sport = :2083"'
```

返回仅有：

```text
udp UNCONN 0 0 *:2083 *:*
```

因此核查时 TCP 2083 无监听。此命令不是 TCP 公网可达性证明，更不是 AnyTLS 握手测试。
2026-09-26 已将目标 alias 的 CTC ProxyJump 固化并验证，保留 `~/.ssh/config`
软链接，仅修改 SHI Host；已同步 docs/server/ssh.md，ZDS 未改。

## UDP 清洗的测试解释

用户报告 RFC TK CO 的 UDP 流量正在清洗。这不是本次独立测得的丢包结论。
AnyTLS 的客户端到服务端只需 TCP 2083，不要求客户端能访问服务器的 UDP 2083。

UoT 验证分两层：先使用服务端回环 UDP 回显/DNS 夹具，确认 TCP 承载、解包、
路由和计数；再探测外部 UDP 目标。若回环成功、外部失败，必须记录目标及服务端
直连 UDP 对照结果，不将公网 UDP 清洗误判成 AnyTLS 实现错误，也不将失败写成通过。
回环夹具仅允许专用测试账户访问，不能为全部用户放开新的管理/回环路由。

## 执行步骤和验收

涉及配置与部署均由本地声明和已有 xrayctl 入口完成；逐项证据见底部结果。

| ID | 步骤 | 必需证据 |
|---|---|---|
| P-001 | 部署前核查 | SSH/证书/监听、防火墙、Core/Sidecar 版本和配置哈希；正常 SS-2022 管理链路基线；记录已知 UDP 故障，不要求其恢复才可测 TCP |
| P-002 | 本地实现门槛与部署 | tests.md 本地必要门槛通过、双平台构建提交；远端配置测试成功后重启，Core/Sidecar 版本核对；2083 TCP/UDP 共存、2053 保持原状 |
| P-003 | sing-box TCP 互通 | 两个独立账户/客户端，通过临时 SOCKS 请求 HTTPS 与受控 TCP marker；正确证书通过，错误密码和错误 SNI 拒绝 |
| P-004 | 用户分流与复用 | 专用测试规则按测试 email + anytls-in 精确限定，分别使用现有不同出口/受控 marker；并发不同域名，验证实际出口，不只看配置；新入站不落入意外 catchall |
| P-005 | 三维流量 | 采集前后 inbound/user/user+domain，固定 payload 检查增量；经 Core API 和 Sidecar 读取；同时查询 stats 开关、域名桶、保留/淘汰条件 |
| P-006 | 动态控制 | 添加临时用户、查询、删除、重加；删除时旧复用会话失效；额外入站生命周期测试在隔离高位端口，不能删除其他生产入站 |
| P-007 | UoT | 回环 UDP marker + DNS，再外部目标；逐目标路由、数据报边界、用户计数；公网失败保留独立对照证据 |
| P-008 | 低风险稳定性与回归 | 至少 10 分钟低并发持续访问，记录 CPU/内存/FD/日志；SS-2022 管理、Sidecar、已有路由和同步未退化；极限/恶意负载只在隔离环境做 |

P-004 不复用普通用户凭据。测试用户与测试规则应有明确拥有者、唯一前缀和清理列表；
不得修改已有用户 route 或默认出口来伪造多用户路由验收。测试账户密码随机生成。
如使用 API 临时状态，记录其非持久语义；永久试点入站仍以本地仓库配置为权威。

P-005 的公网请求会受响应变化、应用协议开销及其他业务影响，只用于观察；字节精确
断言用隔离的受控 payload/独立账户，不能拿整机总量差或公网下载大小直接作等式。
若 SHI 尚未启用 DomainTraffic，为试点通过本地配置按需开启并验证资源预算，
记录回滚值；没有域名统计不能声称三维验证完成。

先使用本地 sing-box 完成用户指定的远端试点；Mihomo 的独立互通验收仍保留在
tests.md 的本地完整验收范围中，不因本次试点只用 sing-box 而宣称已经通过。

## 清理与回滚

- 测试结束关闭本地 sing-box/临时 SOCKS、服务端临时回显服务和临时控制面转发。
- 移除临时测试用户及精确限定的测试路由；保留试点所需配置时，必须在结果报告中
  列明实际保留的入站和账户状态，不能留下不知归属的可用凭据。
- 试点失败按 plan 的顺序回滚：停止 AnyTLS 相关同步、移除新增配置、必要时回退
  Core/Sidecar；验证 SS-2022 原管理链路和原入站，不清空统计历史。
- 不改动 ZDS、DNS、订阅、全局用户数据库；不扩展为批量部署。

## 结果记录

每次记录：日期、服务器、SSH 路径、客户端/服务端版本和 hash、TLS 校验结果、
P-001 至 P-008 逐项结论、实际出口、三维计数差分、UDP 对照、资源和回滚/残留状态。
PASS / FAIL / SKIPPED 必须明确；SKIPPED 不等于完成。报告不含密码、私钥或完整账户配置。

上一轮基线版本：Core `cb7410ca`，Sidecar `688d9b6`；客户端 sing-box 1.14.2。
Core/客户端产物 hash 见 implementation.md。配置 hash：
`52e49dd3bf137afbb24e0827108c2e245d08df560de37bad2532b264e62cf45b`。

可重复执行的显式夹具：

```bash
python3 test/tools/anytls_pilot.py --apply --duration 600
```

夹具先核对配置 hash、空账户和临时前缀无残留；通过 API 添加随机临时账户，
临时规则只匹配 anytls-in 和这些身份，结束后恢复完整原路由并删除账户/出口。
测试账户的已采集流量历史保留，不清空生产统计。服务端回显仅绑定回环，
通过 SSH 启动的临时进程带 20 分钟兜底退出，不安装服务。

- P-001/P-002：预检、私有备份、四平台/组件产物校验及 xrayctl 部署通过。
- P-003/P-004/P-006：双用户 A/B marker、正常 TLS、错误密码/SNI 拒绝、删除旧流
  立即关闭、换密码重加和另一用户不受影响通过。HTTPS trace 出口 82.40.33.214 / JP。
- P-005：用户 TCP 固定 payload 精确 13 上行/14 下行，inbound 同时包含两用户
  payload 和协议开销；Sidecar 初次被旧 Core 能力缓存阻塞，已修复为限频重试。
  新一轮独立身份的用户/域名明细返回成功，10m 聚合；纯 IP DNS 流量归入 unknown。
  完整日志 `.cache/anytls/shi-pilot-final.log`；inbound 精确边界断言在本地夹具完成，
  远端只验证包含 payload，不假定 TLS/AnyTLS 开销恒定。
- P-007：远端 UoT 回环 marker、受控 DNS A 响应，以及实际 `1.1.1.1:53` DNS
  查询均通过。外部 UDP 没有失败，不需要将公网故障解释为协议失败。
- P-008：最终完整十分钟低并发、域名采集和自动清理通过，日志
  `.cache/anytls/shi-pilot-600.log`。Core PID 571385、Sidecar PID 571884 全程不变，
  两服务 NRestarts=0；Core RSS 49.3-52.2 MiB、FD 32-37，Sidecar RSS 35.2-35.8 MiB、
  FD 8-9。这是低负载观测，不是最大容量证明。
- 清理：原生 API 验证 AnyTLS 用户数为 0、临时 `spec003-anytls-` 路由/出口全部移除，
  回环回显和本地 sing-box/隧道进程退出；永久首条默认拒绝规则保留。
- 回归：2083 TCP AnyTLS / UDP Hy2 并存；2053 TCP/UDP 未变。原 SS2 管理链路
  `xrayctl sidecar probe-ip --config-node asz.lite --node tk.rfc.co.micro.shi --spec dns:google`
  返回 82.40.33.214，123ms。Core/Sidecar 版本审计均 CURRENT，配置 hash MATCH。
- Grafana：通过 `xrayctl grafana deploy --dashboards-only` 同步生成的 SHI AnyTLS
  inbound 枚举；无手改 JSON、无订阅或其他节点的协议部署。

首轮十分钟数据访问完成，但结尾 SSH 控制隧道因 ASZ 跳板超时断开，不能把该轮
记作完整通过。恢复 SSH 后已通过原生 API 删除两账户/三出口并恢复仓库原路由；
随后 60 秒复验完整通过且自动清理成功。夹具增加 SSH 保活和清理前重连，清理失败
时保留不含密码的 `.cache/anytls/shi-pilot-recovery.json`，不隐去残留。

## 最终版本复验（2026-09-26）

- 经 `xrayctl sidecar deploy`、`xrayctl deploy --core` 按顺序更新 SHI：Core
  `187bf0ab`、Sidecar `663362e`；两端配置测试通过，产物 SHA 见 implementation.md。
- `shi-convergence-pilot.log`：P-003 至 P-008 完整通过，包括公共证书/SNI、
  两账户不同 marker、回环和外部 UoT DNS、精确 TCP 13 上行/14 下行、删除旧
  generation 断流及另一账户继续可用。HTTPS trace 为 `82.40.33.214 / JP / TLSv1.3`。
- 600 秒低负载访问全部成功，Sidecar 返回两账户 `pilot-anytls.test` 和 UDP
  域名明细，bucket interval 为 10m；纯 IP DNS 归 unknown，未改聚合语义。
- 十次资源采样：Core PID 580331、RSS 45.05-51.15 MiB、FD 33-37；Sidecar
  PID 580111、RSS 35.17-36.59 MiB、FD 8-9。PID 全程未变，NRestarts=0。
- 夹具退出码 0、自动清理成功；独立原生 API 再验：AnyTLS 用户 0，临时
  `spec003-anytls-` routes/outbounds 均为 0，恢复清理 manifest 不存在。
  不删除已采集测试历史，保留空用户和默认拒绝的受管配置。
- Core/Sidecar 审计均 CURRENT、配置 SHA MATCH；SS2 原管理链路
  `dns:google` 回显 `82.40.33.214`，135ms。2083 TCP/UDP 和 2053 TCP/UDP 保留。
- 最终相关 Core race 使用 `-count=1` 重跑通过；Sidecar 全量 race/vet、Python
  252 项（opt-in 二进制另跑通过）通过。公开许可待办及基线失败见 implementation.md。

## 双节点 Hy2 对齐（2026-09-26）

用户明确授权 SHI/ZDS 部署；范围仍为内部使用，不改订阅或上游出站。
两台 `anytls-in` 都监听 TCP 2083，复用本机证书并保留 Hy2 UDP 443/2083。
各自标准 `hy2-in` 的 12 个静态用户原样保留 email/level，完整 auth 字符串作为
AnyTLS password（不能只取 UUID，否则同一入站多个用户密码冲突）。AnyTLS 没有
单独 username 握手字段，按 password 认证后还原服务端配置的 email 用于分流/统计。
15 条包含 `hy2-in` 的规则追加 `anytls-in`，不改顺序、条件、出口或 balancer；
SHI 的临时 default-deny 规则移除。未配置 usersync，不引入新的动态用户来源。
显式资源限制采用已测试默认值：256 会话、每用户 16 会话、每会话 128 流；
引擎的全局 handler/UoT 预算仍有效。

ZDS SSH 与 SHI 一样改为 CTC 跳板，保留原 `~/.ssh/config` 软链接。
Core `8a400cf3`、Sidecar `73b9b15` 本地构建；先 Sidecar，再 Core，再配置，
逐台通过远端 -test 后重启。新入站 Grafana 枚举由 xrayctl grafana 生成并部署。

本地不变量测试：`python3 test/run.py routing`。
只读远端烟测：`python3 test/tools/anytls_routes.py tk.rfc.co.micro.shi tk.rfc.co.micro.zds`；
每个用户通过独立 sing-box 公共证书验证访问 sidecar 204，再对 admin、GLBB、ATT
检查实际 HTTPS trace。密码只读自仓库配置，临时客户端文件 0600、退出后删除。
原先要求空用户的 `anytls_pilot.py` 仅适用于历史隔离试点，不应再直接运行。

部署验收完成：两台 Core `8a400cf3` / Sidecar `73b9b15` 均 CURRENT，配置
SHA 均 MATCH；SHI 为 `929ee5d98b0a69e6c2689778115afc98d5bdbf8c75983a5d1836d3b6b1aa144b`，
ZDS 为 `9147adb444862f1dcc36400064ce3cbd574a32ab2f3fc2daff7ee5c1365cea21`。
两台 API 用户数都是 12，逐用户经 AnyTLS 请求 sidecar 204 全部成功，实际入站
流量计数非零；2083 TCP/UDP 并存，两服务 active、NRestarts=0。
ATT 代表用户经两台都返回 `75.229.215.15 / US`；admin 分别为
`82.40.33.214 / JP`、`82.40.35.243 / JP`；SHI SS2 对照出口一致。
Grafana 已通过生成脚本及 `grafana deploy --dashboards-only` 同步 ZDS 新入站枚举。
本地 Python 全量 253 项通过（1 项原有 opt-in 跳过），路由 9 项通过。

GLBB 首次 AnyTLS HTTPS trace 遇到一次 EOF，未修改出站后复测恢复；后续 SHI
AnyTLS 40/40、SS2 20/20，ZDS AnyTLS 20/20、SS2 20/20 全部成功，均为
`123.100.136.215 / JP / NRT`。服务日志无可定位报错，不把偶发 EOF 归因为
AnyTLS 或声称已修复根因。连续测试日志位于 `.cache/anytls/*-glbb-*.log`。
用 `--protocol ss2 --email bus-tk-glbb-zoro --repeat 20` 可做同路由对照；
显式清空 curl noproxy，避免本机环境把回环目标绕过代理。临时客户端已退出。
