# Observatory

- Feature ID: 004-core-observatory
- Status: Implemented（核心补测、窄修复、提交归并及本地构建完成；未部署）
- Authorized: 2026-09-27，用户授权历史重建、隔离 worktree 补齐 V-010 至 V-014、窄修复及本目录文档同步；未授权部署。
- Source: 历史基线为原 Core e7a21974，相对 b4f08981；见 [001 清单](../001-core-fork-contracts/inventory.md)。
- Scope: 独立探测组、URL 重载、健康恢复、快照和调度停止；不吸收上游、不改原 core 或生产。
- 原 001 FR-005 与 V-010 至 V-014 保持稳定。当前结果见 [执行证据](../001-core-fork-contracts/evidence-008-009.md)。

## US-001 — 分类维护与独立验证（P1）

Given 历史分叉和隔离 worktree 中的受控输入，When 重建后补齐测试并修复暴露的缺陷，
Then 分别记录历史一致性、当前实际运行时变化和验证边界，不以测试文件存在替代验收。

## FR-001 — 当前实现契约

- pingGroups 独立配置 selector、destination、interval、timeout、sampling、
  httpMethod、keepAlive、http2；旧 subjectSelector/pingConfig 保持可用。
- 跨组相同或前缀覆盖 selector 拒绝，同组完全重复拒绝；同组嵌套允许。
  旧空 selector 是 no-op，不解析标签、不启动探测。
- destinationsByPrefix 使用最长前缀，未命中回默认 URL；query 原样保留。
  ob 参数仅属于 URL，Core 不解释其 egress 含义。
- keepAlive 缺省 false；true 允许同一 transport 复用，不保证服务端保活或跨轮复用。
- 主探测 HTTP 成功必须是 204，不追随重定向；200、302、503、超时有真实 HTTP 负向测试。
- 空样本不健康，初始成功即健康，失败立即不健康；恢复需 min(3,容量) 次连续成功。
  恢复中失败重置计数；过期缓存不沿用健康，长空档后重新恢复。
- WalkResults 锁内取快照、锁外回调；阻塞或重入回调不阻塞结果写入，快照修改不污染内部。
- Start/Stop 串行化；Stop 取消并等待本次调度的在途请求、延迟采样及受管理 worker。
  取消和调度结果发布共用结果锁，取消屏障之后不发布该调度的样本。
  公共 PutResult 和无关的一次性 Check 不因此被全局禁用。
- selector 必须并发安全、及时返回并释放自身资源。旧签名不支持传入取消 context，
  Stop 取消等待 selector，不强制终止其函数；违规阻塞回调可能在 Stop 后继续存活。
- 3s interval / 2s timeout / 20 sampling 在 JSON 构建与运行时保持原值。
  interval < 10 的 duration 数值比较本轮未改；不承诺精确每三秒发包。

## SC-001 — 验收与状态

V-010 至 V-014 的核心 harness 已实现并在四包完整 race 及新增测试十次重复 race 中通过。
本类别新增 11 个顶层测试，具体命令和断言见 [测试矩阵](tests.md)。

历史重建阶段的累计 tree 一致性保留在 [重建记录](../001-core-fork-contracts/reconstruction.md)；
本轮增加实际取消修复及测试，当前累计 tree 不再要求等于原历史 HEAD，差异须由父进程审阅归并。
不能将本状态解释为已提交、已发布或全仓验收完成。

## 剩余边界

- HTTP harness 使用 loopback tagged dialer，不覆盖生产 dispatch/远端链路。
- 过期测试控制样本时间戳，不改系统时间；未验证有效期纳秒级相等边界。
- selector 资源由回调方负责，不能把取消等待写成强制回收任意回调。
- 全仓测试及 Darwin/Linux 发布构建不在本轮通过声明内；父进程统一隔离 clone 构建，
  本 worker 不再尝试 wrapper。已有 worktree wrapper 拒绝记录保留在证据中。

## 导航

## Probe connection ownership (extracted from 2074c223)

This protocol-neutral increment belongs to spec004, not the AnyTLS adapter.
Dispatch may return a pipe before protocol admission/handshake finishes. Each
HTTP transport connection therefore owns its dispatch cancellation callback:
Close stops that connection's pending admission, and observer cancellation
propagates. Closing one connection must not cancel sibling streams or their
shared physical pool. Error paths close any returned connection and unregister
callbacks. The actual dispatch ownership primitive is defined by spec002.

Independent verification: `TestContractProbeConnectionOwnsDispatchContext` in
`app/observatory/burst/contracts_http_test.go`; run
`go test -race -count=1 -timeout=5m ./app/observatory/... ./app/router/...`.
The later AnyTLS integration suite verifies this consumer with real TLS/pools;
it does not replace the protocol-independent contract test.

[技术计划](plan.md) · [任务](tasks.md) · [检查清单](checklists/requirements.md)

## FR-002 — 可选 HTTP/2（2026-10-11）

用户授权增加 `pingConfig.http2`，只扩展 Core 主 OB 请求的协议协商，
不改变已部署配置或 Sidecar 的 egress HTTP 客户端。

- `http2` 为布尔值，缺省/false 保持 HTTP/1.1。旧单组和 `pingGroups` 均支持，
  protobuf 追加字段 9，保留原字段编号；每组独立传递到运行时。
- true 使用 Go 标准 `http.Transport.ForceAttemptHTTP2`，通过 TLS ALPN 尝试
  HTTP/2；服务端只支持 HTTP/1.1 时正常回退。`http://` 不启用 h2c。
- `http2` 不隐式打开 `keepAlive`，也不修改 method、URL/query、超时、selector、
  204 成功判定或健康恢复策略。`keepAlive=true` 才允许跨请求保留连接；
  同一 tag/轮次共享 transport，轮末释放空闲连接，不保证跨轮复用。
- 保留真实出站 tagged dialer、请求/连接取消所有权和 TLS 证书验证，
  不新增协议专用拨号器，不采用 `InsecureSkipVerify` 绕过测试。
- 本地验证必须观察服务端 `Request.ProtoMajor`、连接数与 query，不以
  `ForceAttemptHTTP2` 字段为 true 代替实际协商验证。详见 tests.md 的 V-015。

配置示例（HTTP/2 与连接复用均为显式启用）：

```json
"pingConfig": {
  "destination": "https://cp.cloudflare.com/generate_204",
  "interval": "30s",
  "timeout": "5s",
  "sampling": 25,
  "keepAlive": true,
  "http2": true
}
```

## FR-003 - Bounded Probe Retry (2026-10-12)

Authorized by the user after the HTTP/2 commit. The initial implementation scope
was local testing/build only. Subsequent authorization on 2026-10-12 permits
Core/Sidecar rollout and HKT retry enablement only on `hk.shlii.tr`, after tests;
node configuration/rollout evidence stays in xray-config. No fleet rollout or push.

- `pingConfig.retry` is optional in both legacy and grouped configurations.
  `maxRetries` defaults to zero, accepts 0..3, and counts additional application
  attempts (not TCP dials or Go transport's own stale-connection retries).
- `initialBackoff` defaults to `100ms`, `maxBackoff` to `400ms`; configured delays
  must be nonnegative, with zero using defaults, and max >= initial. Exponential
  waits have +/-20% jitter and an absolute cap, with overflow-safe arithmetic.
- Enabled retry is restricted to HEAD/GET. Refused/reset/broken connections,
  EOF and temporary DNS/transport timeouts are eligible; recognizable auth,
  TLS certificate, NXDOMAIN, HTTP status, parse/config, cancellation and ASN
  failures are not. An opaque bridge EOF cannot identify its underlying cause.
- A logical main probe has one timeout context spanning attempts, response
  reads and waits. No attempt resets the deadline. Insufficient budget for the
  next backoff ends the probe without waiting; cancellation interrupts waits.
- Publish exactly one final RTT/sample. RTT includes failed attempts and waits.
  Exhaustion publishes one dead sample; recovery still needs separate scheduled
  successes. Startup, stale-window, selector and balancer rules are unchanged.
- Keep the same tagged outbound, destination/query, method and transport across
  attempts. Do not close a shared pool on one retry or cancel other streams.
  Existing direct `connectivity` diagnostics remain a separate one-shot stage
  with their existing timeout; they are not part of the main retry budget.
- `common/retry.ProbePolicy` is shared with Sidecar. It creates no goroutines,
  requires context-aware operations, exposes attempts/first error and supports
  explicit permanent parser errors. Do not alter existing `ExponentialBackoff`
  or protocol dialer retry behavior as part of this feature.
- Proto appends `HealthPingConfig.retry = 10` with a separate policy message.
  JSON and proto configuration validation enforce bounded settings.

```json
"retry": {
  "maxRetries": 2,
  "initialBackoff": "100ms",
  "maxBackoff": "400ms"
}
```

Consumer contract: [Sidecar spec003](../../../xray-sidecar/spec/003-egress-probe-retry/spec.md).
Validation and limitations: [retry acceptance](retry-acceptance.md).

## FR-004 - Independent Probe Transport (2026-10-14)

Authorized for Shlii's local HKT guard consumption, not a fleet rollout.
`pingConfig.probeOutboundTag` is an optional transport outbound, appended as
protobuf field 11. Empty/absent preserves probing through each observed subject.
Legacy and grouped JSON trim surrounding whitespace and preserve it through
protobuf and runtime settings. Normal URL/query, HEAD/GET, status, timeout,
retry, cancellation, sampling and recovery rules remain unchanged.

The selected subject remains the result key for success, failure and connectivity
diagnostics; the transport tag never replaces it or enters balancer candidates.
The existing tagged dispatcher still owns dialing, so an unknown transport fails
closed rather than using the default outbound. This is explicit trust in another
outbound's health response, not independent proof that the subject carried traffic.

Shlii reads `http://127.0.0.1:8080/generate_204?ob=hkt` through `sidecar-uds`,
which redirects to the local Sidecar HTTP UDS. Sidecar independently checks QoS
through Core's authenticated `egress-in` and the fixed HKT SOCKS outbound. It
does not read backend passwords or use the business balancer. Local OB reads
cached health, does not trigger a new egress round, and records local response
time, not upstream RTT. Keep OB and egress budgets/retries independent; native
recovery gates remain separate and can add recovery delay. Existing randomized
batch scheduling is unchanged; interval is not a strict maximum reaction time.

Verification: [V-017](tests.md#v-017-independent-probe-transport).
