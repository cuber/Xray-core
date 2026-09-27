# 用户域名流量验证矩阵

继承 005 的 V-019 至 V-027 编号。2026-09-27 用户明确选择：**Core 与 Sidecar
均为有界尽力缓存，容量淘汰直接删明细、不搬 other；已采集历史不因 Core 后续
淘汰而撤回，未采集即淘汰允许丢失。** 旧 other 字段兼容读取，不重分配身份。
因此缓存总量不是完整 Stats；V-023/V-025/V-026 不要求淘汰前后字节守恒。

本轮 Core 路径是 `../xray-core-spec`；Sidecar 默认 replace 仍指向原 `../xray-core`。
联测使用临时 modfile，不修改 pin。完整实测、负向证据及限制见
[Core evidence](../005-core-fork-contracts/evidence-010-core.md) 与
[Sidecar evidence](../005-core-fork-contracts/evidence-010-sidecar.md)。
历史重建结果仍见 [005 reconstruction](../005-core-fork-contracts/reconstruction.md)。

## 公共步骤

遵守 [公共复验规程](../005-core-fork-contracts/test-procedure.md)。本轮仅格式化
自己修改的 Go 文件，Sidecar 另执行 `make check-fmt`。使用临时文件和 loopback
随机端口，不连接生产。每组保存 `go test -json` 输出、退出码和实际命中的测试。

Core 定向验证（在 `../xray-core-spec`）：

```sh
go test -json -race -count=3 -timeout=5m ./app/stats ./app/stats/command ./app/dispatcher -run '^(TestDomainTraffic|TestGetDomainTraffic)'
```

Sidecar 联测（在 `../xray-sidecar`，先创建临时 modfile）：

```sh
audit_dir=$(mktemp -d)
cp go.mod go.sum "$audit_dir/"
go mod edit -modfile="$audit_dir/go.mod" -replace=github.com/xtls/xray-core=/Volumes/Linux/opensource/cuber/xray-core-spec
make check-fmt
go test -modfile="$audit_dir/go.mod" -json -race -count=3 -timeout=5m . -run '^TestDomainTraffic'
```

测试代码自动清理 listeners、gRPC channels、临时 snapshot 和受控子进程。临时
modfile 与 JSON 日志是本地审计产物，不进入仓库。主流程统一提交和本地构建，
最终结果见 [验收](../005-core-fork-contracts/acceptance.md)，不部署发布包。
正式 build wrapper 的 worktree 限制见 evidence，不能把定向测试等同发布验收。

## 矩阵

| ID / 原 FR | 输入和操作 | 精确预期 | 本轮测试 |
|---|---|---|---|
| V-019 / 008 | disabled / enabled-empty，普通 counter 加 123；真实 gRPC 查询两类统计 | enabled 可区分；零域名桶；普通 Stats 都为 123 | `TestDomainTrafficRPCDisabledAndEnabledEmptyKeepOrdinaryStats` |
| V-020 / 008 | alice/example 100/200、bob/example 300/400、alice/other 50/60；data+nil/EOF/timeout、writer 成功/失败；WrapLink + sniff cache 重放 | 无淘汰 oracle=450/660，三组合隔离；writer 返回前已计数；缓存重放不双计，完整 user Stats=100/200 | `TestDomainTrafficByteOracleErrorsAndWriterFailure`、`TimeoutDataAndEmpty`、`SniffCacheReplayCountsOnce` |
| V-021 / 008 | Target/RouteTarget/OriginalTarget 逐一替换为 IP；最后 outbound；空白/大小写/尾点；跨用户同域名和有身份 unknown | 当前 outbound 的 Target > RouteTarget > OriginalTarget；不回溯前一 hop；认证 unknown 占容量，匿名 unknown 不占 | `TestDomainTrafficAttributionPriority`、`BucketsNormalizeAndTrackDirections`、`UserIsolation`、`AuthenticatedUnknownAndTieEviction` |
| V-022 / 008 | fake clock 100、104.999999999、105；回拨 99；前进 110/170/175；retention cutoff 400/405 再回拨 90 | 开放桶不可读；回拨钳制、不重开已发布桶、不恢复已过期桶；60s idle 无伪 sequence；start==cutoff 保留 | `TestDomainTrafficBoundariesIdleAndRollback`、`SequenceHasNoFalseGapAcrossEmptyBuckets` |
| V-023 / 008 | 容量2、A/B/刷新A/C；同时间平局；MaxUint64 附近；四 writer 各500次并发 Snapshot | B 直接删除、other=0；平局只指定容量不指定牺牲者；饱和不回绕；完整 Stats=2000，缓存仅2/4 | `TestDomainTrafficLRUUsesLastSeenAndDiscardsEvictedBytes`、`SaturatingCounters`、`AuthenticatedUnknownAndTieEviction`、`ConcurrentRecordSnapshotAndCompleteStats` |
| V-024 / 008,009 | 同 boot 分页1、重复cursor、开放 latest=3、过旧cursor、新boot及 MaxUint64 cursor | 应用一次；cursor只到收到的最大seq；开放latest不跳过；过旧标gap但不补造；新boot保留Sidecar历史 | Core `TestDomainTrafficPagedCursorOpenLatestGapAndBoot`；Sidecar `RPCPagingOpenCursorAndBootMerge`、`RPCExpiredCursorReportsGap` |
| V-025 / 008,009 | Core容量1，alice/A=100/200闭合后S1先拉；bob/B=30/40淘汰A，S1增量与S2晚拉 | 新Core及独立fixture严格S1=130/240、S2=30/40、other=0；真实RPC消费者兼容旧Core other100/200，late明细严格仅bob30/40，不回溯撤回early | Core `TestDomainTrafficLRUDiscardsUncollectedHistory`；Sidecar `RPCBestEffortEarlyLateCollection`、`NewCoreResponseEarlyLateCollection` |
| V-026 / 009 | t599/600边界；每10m 32个新组合，重复3虚拟日；组合7/entry11与组合64/entry9；跨桶同组合 | 直接10m桶；最多145桶；entries/索引精确为7或9；内部汇总一致但淘汰损失允许；旧other/unknown不新增/不分给用户 | Sidecar `TestDomainTrafficThreeVirtualDaysExactBudgets`、`BoundaryAndSharedIdentityEntryBudget` |
| V-027 / 009,012 | schema2保存/重放，schema1重分桶，坏JSON/schema/重复/索引/匿名汇总错误；写失败；真实子进程kill/restart | 失败恢复不污染；保存字节与cursor一起恢复；旧other保持；文件0600目录0700；失败保存旧文件hash不变；未落盘字节允许暂失 | Sidecar `TestDomainTrafficMalformedSnapshotsAreAtomic`、`SnapshotFailurePreservesOldAndPermissions`、`LegacySnapshotBoundaryBudgetsAndReplay`、`SnapshotRestoresWithSmallerEntryBudget`、`ConcurrentApplyResultSnapshot`、`SnapshotKillRestart`、`MalformedSnapshotProcessStillServesHTTP` |

表中省略重复前缀的测试名均以 `TestDomainTraffic` 开始。精确复验筛选如下。

## 逐项命令

在 Core worktree 执行：

```sh
# V-019
go test -json -race -count=1 -timeout=5m ./app/stats/command -run '^TestDomainTrafficRPCDisabledAndEnabledEmptyKeepOrdinaryStats$'
# V-020
go test -json -race -count=1 -timeout=5m ./app/dispatcher -run '^TestDomainTraffic(ByteOracleErrorsAndWriterFailure|TimeoutDataAndEmpty|SniffCacheReplayCountsOnce)$'
# V-021
go test -json -race -count=1 -timeout=5m ./app/dispatcher ./app/stats -run '^TestDomainTraffic(AttributionPriority|BucketsNormalizeAndTrackDirections|UserIsolation|AuthenticatedUnknownAndTieEviction)$'
# V-022
go test -json -race -count=1 -timeout=5m ./app/stats -run '^TestDomainTraffic(BoundariesIdleAndRollback|SequenceHasNoFalseGapAcrossEmptyBuckets)$'
# V-023
go test -json -race -count=1 -timeout=5m ./app/stats -run '^TestDomainTraffic(LRUUsesLastSeenAndDiscardsEvictedBytes|SaturatingCounters|AuthenticatedUnknownAndTieEviction|ConcurrentRecordSnapshotAndCompleteStats)$'
# V-024
go test -json -race -count=1 -timeout=5m ./app/stats -run '^TestDomainTrafficPagedCursorOpenLatestGapAndBoot$'
# V-025
go test -json -race -count=1 -timeout=5m ./app/stats -run '^TestDomainTrafficLRUDiscardsUncollectedHistory$'
```

在 Sidecar 执行，`audit_dir` 使用上面的临时 modfile：

```sh
# V-024
go test -modfile="$audit_dir/go.mod" -json -race -count=1 -timeout=5m . -run '^TestDomainTrafficRPC(PagingOpenCursorAndBootMerge|ExpiredCursorReportsGap)$'
# V-025: 默认旧Core和临时modfile新Core都须通过；Core自身测试负责严格淘汰契约。
go test -json -race -count=1 -timeout=5m . -run '^TestDomainTraffic(RPCBestEffortEarlyLateCollection|NewCoreResponseEarlyLateCollection)$'
go test -modfile="$audit_dir/go.mod" -json -race -count=1 -timeout=5m . -run '^TestDomainTraffic(RPCBestEffortEarlyLateCollection|NewCoreResponseEarlyLateCollection)$'
# V-026
go test -modfile="$audit_dir/go.mod" -json -race -count=1 -timeout=5m . -run '^TestDomainTraffic(ThreeVirtualDaysExactBudgets|BoundaryAndSharedIdentityEntryBudget)$'
# V-027
go test -modfile="$audit_dir/go.mod" -json -race -count=1 -timeout=5m . -run '^TestDomainTraffic(MalformedSnapshotsAreAtomic|SnapshotFailurePreservesOldAndPermissions|LegacySnapshotBoundaryBudgetsAndReplay|SnapshotRestoresWithSmallerEntryBudget|ConcurrentApplyResultSnapshot|SnapshotKillRestart|MalformedSnapshotProcessStillServesHTTP)$'
```

## 独立 Oracle 与边界

- V-020 无淘汰时三组合450/660；alice150/260、bob300/400。载荷大小是独立输入，
  不用 HTTP/协议封装字节替代。sniff 测试调用真实 WrapLink/cachedReader，但不假称完整网络路由 E2E。
- V-022 回拨行为是本轮明确实现的逻辑时间策略，不再把偶然顺序当作既定事实。
  提前向前跳时会过期丢数据；回拨不复活它，逻辑时间追上前不强行关桶。
- V-025 原 Core 的晚消费者为130/240且other=100/200，新 Core 必须为30/40且other=0。
  真实RPC的Sidecar测试精确保持实际other，明细和用户汇总严格仅bob30/40；独立新Core
  响应fixture严格验证early130/240、late30/40、other0，不依赖开发pin。Core测试仍严格
  验证新LRU。early/late不等不是失败；普通Stats与这个有损缓存严格分离。
- V-026 每步32条等大1/2字节明细，另输入兼容other=11/13及匿名unknown=5/7。
  若保留桶数B、明细预算K，总量必须为 `(16B+K)/(20B+2K)`，而非全部写入量。
  每一步核对桶边界、entryCount与实际map项、lastSeen与实际组合的一一关系，
  独立检查user/domain汇总。每虚拟日记录GC后heap和snapshot字节上限；heap曲线不是替代容量断言。
- V-027 子进程运行受控测试二进制，报告snapshot完成后才SIGKILL，独立进程恢复；
  落盘450/660之后未保存的9/13应缺失，重放后一次恢复，新Core boot再追加7/11。
  坏snapshot时真实临时HTTP仍可用，但未启动production main、collector定时器或服务管理器。
- 故障注入覆盖JSON/schema/结构、mkdir及rename失败；不宣称覆盖真实磁盘满、fsync硬件故障、
  断电目录持久性或生产一分钟调度。主服务恢复日志与定时器集成仍由最终系统验收负责。

## 状态

本轮上述定向测试和跨消费者联测已实际执行，详见 evidence 的命令、退出码与日志。
全量测试、生产构建和统一提交由父进程负责，本页不替代其验收。
