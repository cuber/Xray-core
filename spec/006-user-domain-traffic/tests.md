# Core 用户域名流量复验

本页只定义提供方的复验方法，不声称文档拆分时重新执行测试。
历史命令与实测见 [2026-09-27 矩阵](history/validation-20260927.md) 和
[Core evidence](../001-core-fork-contracts/evidence-010-core.md)。
Sidecar 消费方 V-024 至 V-027 见
[Sidecar spec007/tests](../../../xray-sidecar-rebase-20261014/spec/007-domain-traffic-aggregation/tests.md)。

## 复验命令

从本 Core 根目录执行，不启动生产服务，不修改消费者 pin：

```sh
go test -json -race -count=3 -timeout=5m ./app/stats ./app/stats/command ./app/dispatcher -run '^(TestDomainTraffic|TestGetDomainTraffic)'
```

保存实际 HEAD、工具链、完整命令、退出码、命中的测试和 SKIP 原因。
跨仓联测须核对消费者的 Core pin 或显式临时 modfile，不能把历史路径直接当作当前源码。

## 原编号与精确 Oracle

| ID | 场景 / 预期 | 对应测试（均以 TestDomainTraffic 开头） |
|---|---|---|
| V-019 | disabled 与 enabled-empty 可区分；普通 Stats 都保持 123 | `RPCDisabledAndEnabledEmptyKeepOrdinaryStats` |
| V-020 | 三组合 oracle 450/660；带数据和错误仍计数；写失败不改字节口径；sniff 重放只计一次 | `ByteOracleErrorsAndWriterFailure`、`TimeoutDataAndEmpty`、`SniffCacheReplayCountsOnce` |
| V-021 | 最终 outbound 的 Target > RouteTarget > OriginalTarget；标准化；跨用户隔离；认证 unknown 占容量 | `AttributionPriority`、`BucketsNormalizeAndTrackDirections`、`UserIsolation`、`AuthenticatedUnknownAndTieEviction` |
| V-022 | 开放桶不导出；回拨不重开、不复活；idle 不制造空 sequence；start==cutoff 保留 | `BoundariesIdleAndRollback`、`SequenceHasNoFalseGapAcrossEmptyBuckets` |
| V-023 | 刷新 A 后淘汰 B，other=0；饱和不回绕；完整 Stats=2000 而缓存仅保留容量内组合 | `LRUUsesLastSeenAndDiscardsEvictedBytes`、`SaturatingCounters`、`ConcurrentRecordSnapshotAndCompleteStats` |
| V-024 提供方 | 分页1、开放 latest、旧/未来 cursor、新 boot；只导出闭合桶，不伪造 gap | `PagedCursorOpenLatestGapAndBoot` |
| V-025 提供方 | 容量1：alice/A=100/200 后被 bob/B=30/40 淘汰；晚查询仅 B，other=0 | `LRUDiscardsUncollectedHistory` |

同域名跨用户及 LRU 同时间平局用集合/容量断言，不能依赖 map 遍历顺序。
Core 只保存短窗口；普通 Stats 与有损域名缓存分别断言，不要求淘汰前后守恒。
已导出桶可因 LRU 删明细；消费者早采集 130/240、晚采集 30/40 是接受的边界，
不是提供方承诺撤回消费者旧数据。消费者双向验证归 Sidecar spec007。

## 覆盖边界

上述 deterministic/race 测试不替代主服务启动、定时器、24h 真实运行或生产灰度。
实际升级回归版本与 FAIL/SKIP 见 [Core spec010](../010-upstream-main-upgrade/implementation.md)。
