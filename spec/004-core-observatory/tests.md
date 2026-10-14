# Observatory 验证矩阵

## Ownership split (2026-10-14)

The shared `TestContractProbeConnectionOwnsDispatchContext` and implementation
were extracted from original 2074c223 before AnyTLS outbound. The independent
observer/router race suites passed at that intermediate revision after restoring
the canonical DAT test resources. HTTP/2, retry and independent-transport changes
retain their own increments and the tests below. Current whole-tree results and
old/new commit mapping: [split report](../001-core-fork-contracts/history-split-20261014.md).

2026-10-12 optional retry increment: V-016 is covered by
[retry-acceptance.md](retry-acceptance.md), including helper/config validation,
actual H1/H2 reuse, concurrent pool isolation, one final health sample,
unchanged recovery, total deadline and stopping during backoff.

V-010 至 V-014 核心补测已实现并通过 race（2026-09-27），不再是 harness 设计稿。
新增代码在 /Volumes/Linux/opensource/cuber/xray-core-spec；下列 core 路径相对该 worktree。
原有 manager/ping/healthping/config 测试保留并随完整目标包运行。
详细原始结果、失败复现、修复、日志哈希见 [执行证据](../001-core-fork-contracts/evidence-008-009.md)。

| ID / 原 FR | 已实现输入与断言 | 新测试入口 | 状态 |
|---|---|---|---|
| V-010 / 001 | 两组 URL/timeout 独立；A 超时不污染 B；selector 冲突/同组嵌套/旧空 no-op | TestContractManagerGroupIsolation、TestContractBurstJSONGroups、TestContractBurstJSONIndependentSettings | 核心 PASS |
| V-011 / 001 | a-x、a-b-x、z 的真实 RequestURI；最长前缀和 query 原样保留 | TestContractProbeQueryCapture | 核心 PASS |
| V-012 / 001 | 204/200/302/503/timeout；302 目标零访问；十次请求的连接数为 1 或 10 | TestContractHTTPStatusesAndConnections | 核心 PASS |
| V-013 / 001 | 容量 1/2/20；失败/连续恢复/重置；缓存过期及重新恢复 | TestContractHealthRecoveryCacheMatrix | 核心 PASS |
| V-014 / 001 | 快照和阻塞回调；在途 HTTP/延迟采样取消；3s/2s/20；Start/Stop race；阻塞 selector | TestContractManagerBlockedCallbackAndSnapshot、TestContractSchedulerStopCancelsInflight、TestContractSchedulerConcurrentStartStop、TestContractSchedulerStopDoesNotWaitForSelector、TestContractCanceledCheckDoesNotPublish | 核心 PASS，selector 约束见下 |

## 实际执行

以下两条在 xray-core-spec 执行，均 exit 0；JSON 位于同机 /tmp。
四包完整回归有 277 个测试/子测试通过事件，新增测试十次重复有 1170 个通过事件，
均无失败或跳过。这是 004/005 合计，不是仅 004 的数量；004 新增 11 个顶层测试。

```sh
go test -json -race -count=1 -timeout=5m ./app/observatory/burst ./app/router ./app/router/command ./infra/conf > /tmp/core-spec-008-009-race.json
go test -json -race -count=10 -timeout=5m ./app/observatory/burst ./app/router ./app/router/command ./infra/conf -run '^TestContract' > /tmp/core-spec-008-009-repeat.json
```

以下逐项命令是对已通过 suite 的可执行筛选复验，不声称本轮分别另跑过每条筛选命令。
复验遵循 [公共规程](../001-core-fork-contracts/test-procedure.md)，保存 JSON 和退出码，
零命中不算通过。

## V-010：独立探测组

```sh
go test -json -race -count=1 -timeout=5m ./app/observatory/burst ./infra/conf -run '^Test(ContractManagerGroupIsolation|ContractBurstJSONGroups|ContractBurstJSONIndependentSettings|ValidatePingGroups_.*|ResolvePingGroups_.*|BurstObservatoryConfig.*)$'
```

两个 loopback server 与独立 manager Check 调用：A 阻塞至 timeout，B 返回 204，
验证结果分组隔离和未匹配 tag 被忽略。这里不是证明同一次批量 Check 的各组并行执行。
JSON 层验证跨组相同/嵌套拒绝、同组重复拒绝/嵌套允许，以及 null、空 selector、
缺 pingConfig 的错误。旧空 selector 的 manager 不调用 resolver。
独立 destination/interval/timeout/sampling/method/keepAlive 在 Build 到运行时完整保留。
清理 manager/server；超时 handler 随 context 退出。

## V-011：最长前缀与 query

```sh
go test -json -race -count=1 -timeout=5m ./app/observatory/burst -run '^Test(ContractProbeQueryCapture|HealthPingSettings_destinationFor)$'
```

a-x、a-b-x、z 依次通过实际 HTTP 捕获 /a?ob=x、/ab?ob=y、
/default?ob=z&raw=%2F，精确比对 RequestURI 并确认成功样本。
默认/覆盖 URL 使用同一 server 不同路径；关闭 HTTP server/空闲连接。
Core 不解释 ob 的出口含义。

## V-012：HTTP 状态与连接复用

```sh
go test -json -race -count=1 -timeout=5m ./app/observatory/burst -run '^Test(ContractHTTPStatusesAndConnections|NewHTTPClientKeepAlive|MeasureDelayRequiresNoContent)$'
```

新 harness 使用真实 transport：204 成功，其余 200/302/503/timeout 返回错误和失败哨兵；
302 目标计数严格为 0。timeout handler 等待请求 context 取消后退出。
同一 client 连续十次请求，keepAlive=false 计数十个新连接，true 计数一个；
日志记录请求的 RemoteAddr。响应读取/关闭，空闲连接与 server 清理。
不外推为跨轮复用或任意服务端必然保活。

## V-013：健康恢复与缓存

```sh
go test -json -race -count=1 -timeout=5m ./app/observatory/burst -run '^Test(ContractHealthRecoveryCacheMatrix|HealthPing.*)$'
```

容量 1/2/20 各自使用新状态，逐样本输入 S、SF、FSSS、FSFSSS。
独立 oracle 验证空状态 dead、初始 S alive、F 即 dead、1/2/3 连续成功恢复、
中途 F 重置。随后先形成健康缓存，将样本时间戳置于明确超过有效期的位置，
缓存读取应 dead 且 All=0；继续输入成功并验证 1/2/3 恢复门槛。
不修改系统时钟，不依赖 sleep 等待过期；不是注入全局固定时钟，也未验证纳秒级等值边界。

## V-014：快照、取消与调度生命周期

```sh
go test -json -race -count=1 -timeout=5m ./app/observatory/burst ./infra/conf -run '^Test(ContractManagerBlockedCallbackAndSnapshot|ContractSchedulerStopCancelsInflight|ContractSchedulerConcurrentStartStop|ContractSchedulerStopDoesNotWaitForSelector|ContractCanceledCheckDoesNotPublish|ContractBurstJSONGroups|ManagerWalkResults_.*)$'
```

阻塞 WalkResults 回调期间并发 PutResult 必须完成，改回调快照不能污染下次读取；
原有重入回调测试一起保留。HTTP handler 阻塞时 Stop 返回后活动请求归零，取消样本不入库。
3s/2s/20 配置在 JSON 与运行时均不被钳制，8 个 caller 各 20 次 Start/Stop 通过 race。
阻塞 initial/scheduled selector 的测试证明 Stop 不等待其返回，随后主动释放并观察两个回调。
取消成功请求的测试确认取消后不发布该样本；清理 barrier/client/server/测试 goroutine。

实际修复不是仅补测试：scheduler run 持有可取消 context，Start/Stop 锁序列化并等待
受管理 worker；延迟采样可取消，HTTP/连接探测绑定 context。取消和调度结果写入共用锁，
消除先检查 context 再无条件 PutResult 的空隙。

selector 的旧签名无法强制取消任意回调：它必须并发安全、及时返回并释放自身资源。
Stop 只取消等待，违规回调仍可存活；不宣称所有任意外部 goroutine 都被回收。
公共显式 PutResult 与无关一次性 Check 不因 scheduler Stop 被禁止。

## 剩余验证边界

真实 HTTP 使用 loopback tagged dialer，不覆盖生产 dispatch 或远端链路；
3s 只验证配置不变，不保证精确周期。全仓测试和 Darwin/Linux 发布构建未标 PASS。
父进程负责统一隔离 clone wrapper 构建，本 worker 不再尝试。
历史 tree 一致性仅属于重建阶段，本轮取消修复和新测试有意增加差异。

## V-015：HTTP/2 协商、复用与兼容（2026-10-11）

新增 `app/observatory/burst/http2_test.go` 和
`infra/conf/contracts_observatory_test.go` 中的 HTTP/2 契约测试。
TLS fixture 使用专用受信任根，不关闭证书验证；tagged dialer 只改为串行
loopback 测试钩子，结束恢复原实现，不声称已验证远端 SOCKS 或生产链路。

```sh
go test -race ./app/observatory/burst ./infra/conf \
  -run 'Test(HTTP2|ContractBurstHTTP2Config|ContractBurstJSONIndependentSettings|NewHTTPClientKeepAlive)' \
  -count=10 -timeout=5m
go test -race ./app/observatory/... ./app/router/... ./infra/conf -count=1 -timeout=5m
go test ./... -count=1 -timeout=15m
go vet ./app/observatory/... ./app/router/... ./infra/conf
bash .github/check-gofmt.sh
```

- `TestHTTP2ProbeNegotiationAndReuse`: HEAD/GET 均覆盖默认 H1、显式 H2、
  H1-only TLS 服务端回退。每个组合连续十次请求，服务端观察协议、原始 query
  和 RemoteAddr；keepAlive=false 新建十个连接，true 复用一个；清理后全部关闭。
- `TestHTTP2ProbeStatusAndTLSValidation`: 真实 H2 下只有 204 成功，200/302/503
  失败；重定向目标零命中。不受信任证书失败，HTTP/2 不绕过 TLS 验证。
- `TestHTTP2ConcurrentProbeStreamsReuseConnection`: 建立连接后并发八个请求，
  在释放响应前八个 handler 都进入；协议均为 H2、RemoteAddr 相同、连接数为一。
  这验证多流并发，不承诺冷启动时 Go transport 的投机拨号只创建一个连接。
- `TestHTTP2ProbeTimeoutAndCancellation`: H2 handler 阻塞，客户端超时和 observer
  parent 取消分别结束请求，服务端流 context 被取消，客户端清理空闲连接。
- `TestHTTP2OptionDoesNotEnableH2C`: 开启选项后明文 HTTP 仍为 HTTP/1.1。
- `TestContractBurstHTTP2Config`: 单组/多组、true/false、protobuf 编解码及
  运行时配置保持一致；字符串 `"true"` 拒绝；不连带启用 keepAlive。
- 原独立组测试覆盖 A 显式开启 H2，B 缺省关闭；原 HTTP/1 状态、连接、
  健康恢复、调度取消和 router 测试必须一起通过。

正式构建从 xray-config 执行 `./scripts/xrayctl.py build core`，源代码须先本地
提交。检查 Darwin/Linux 产物的格式和 VCS 版本，再用 Darwin `run -test`
验证新增选项与现有节点配置。不因本地验证自动启用生产配置或部署。

实际执行结果与跳过边界见 [HTTP/2 验收证据](http2-acceptance.md)。

## V-017: Independent Probe Transport

```sh
go test -race ./app/observatory/burst ./infra/conf \
  -run 'Test(ProbeOutbound|ContractBurstProbeOutboundConfig)' -count=3 -timeout=3m
go test -race ./app/observatory/... ./app/router/... ./app/proxyman/outbound ./infra/conf -count=1 -timeout=5m
go vet ./app/observatory/... ./app/router/... ./app/proxyman/outbound ./infra/conf
go test ./... -count=1 -timeout=15m
bash .github/check-gofmt.sh
```

`TestProbeOutboundKeepsSubjectHealthAndGroupIsolation` captures actual HEAD/query
and transport tag, checks unchanged defaults in another group, immediate failure
and three-success recovery, and missing-transport failure without another dial.
Only the selected subject has results. `TestProbeOutboundTimeoutAndStop` checks
timeout and scheduler cancellation, no canceled result, and resource cleanup.
JSON tests cover legacy/groups, empty/trimmed tags, string validation and proto
round-trip. These Go HTTP harnesses replace the tagged dialer; they do not prove
real routing or Unix redirect.

After local commit and the approved Darwin/Linux build, xray-config adds a real
Core/Sidecar test, using a private Core API/egress/Sidecar UDS and authenticated
SOCKS fixture. Run `python3 test/run.py nodes/hk.shlii.tr` there. A wrong ASN must
make OB dead despite a reachable SOCKS backend; expected ASN restores health.
Backend refusal makes OB dead and business uses direct, while egress never uses
that fallback. Recovery returns business to SOCKS. Assert the backend never sees
`/generate_204`, direct never sees `/client-ip`, no result uses `sidecar-uds`, and
Sidecar config contains no backend password. Keep existing native VLESS/Mux tests.
Every process, HTTP/SOCKS fixture and private temporary directory must be closed.

Node config/rollout evidence belongs in xray-config, not a duplicated Core spec.

Executed 2026-10-14: targeted race passed at count=3 and count=10, full affected
package race and vet passed, and `go test ./... -count=1 -timeout=15m` exited 0
(AnyTLS 524.424s, protocol scenarios 276.366s). Default opt-in external/soak
tests remain opt-in; this is not a new public-network or soak acceptance claim.
Format/whitespace gates and approved Darwin/Linux wrapper build passed.
The real Core/Sidecar fixture passed, then xray-config's full suite passed
437 tests with one existing skip. No Sidecar runtime source change was needed.
