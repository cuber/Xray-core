# Observatory 实施计划

## 阶段与结果

1. 历史重建：按 FR-005 分类、核对依赖与累计 tree；历史结果见
   [001 重建记录](../001-core-fork-contracts/reconstruction.md)，不覆盖本轮新增差异。
2. 核心补测（已完成）：在 xray-core-spec 中实现 V-010 至 V-014 的真实 HTTP、
   健康恢复/缓存、快照和停止并发 harness，并补 JSON 配置测试。
3. 缺陷修复（已完成）：复现 Stop 返回后 HTTP 仍活动；绑定请求 context，
   调度 run 独立取消，跟踪并等待 worker/延迟采样，取消和发布共用结果锁。
4. selector 边界（已完成）：使用缓冲结果通道取消等待；回调本身仍须并发安全、
   及时返回并释放资源。禁止宣称可以强制中止不可取消的任意函数。
5. 验证（已完成）：四包完整 race、新增测试十次重复 race、gofmt 和 diff 检查；
   准确命令、失败到修复证据、文件归属见 [执行证据](../001-core-fork-contracts/evidence-008-009.md)。
6. 父进程收敛（未完成）：审阅并按 spec 归并修改，统一隔离 clone wrapper 构建；
   当前 worker 不提交、不 checkout、不再尝试构建、不部署。

## 文件与实现约束

004 归属：app/observatory/burst/ 下的 healthping.go、ping.go、manager.go、
contracts_http_test.go，以及 infra/conf/contracts_observatory_test.go。
保持随机分散采样和 3s/2s/20 设置，不借停止修复钳制为 10s。
Start/Stop 锁阻止 WaitGroup 跨 run 复用；新增任务来自已经计数的调度 worker。
取消屏障与调度发布在同一结果锁下判定，公共显式 PutResult 保持独立。

## 验证与回滚边界

按 [tests.md](tests.md) 复验；HTTP 服务器、空闲连接和测试 barrier 均清理，
阻塞 selector 由测试主动释放。测试不声称真实远端路由或精确三秒发包。
历史备份分支/tag 仍是重建回滚参考；新增修复由父进程审阅后单独可追溯归并。
不修改远端、Sidecar pin 或部署记录；遵守 constitution 的隔离与事实约束。

## HTTP/2 增量计划（2026-10-11）

1. 追加 protobuf 字段 9，将 `pingConfig.http2` 经 JSON、proto、运行时设置
   传到现有 HTTP transport；缺省关闭，不修改拨号器或共享连接生命周期。
2. 受控 TLS H1/H2 服务端验证协议协商、回退、HEAD/GET、连接数和清理，
   并覆盖证书失败、非 204、超时/取消、组隔离和 proto 编解码。
3. 重复 race、相关完整回归、全库测试、vet 和格式门禁；保留实际结果与跳过。
4. 代码/spec 本地提交后执行批准的双平台构建和 Darwin 配置校验。
   不 push、不部署；Sidecar egress 客户端和 Shlii 线上故障独立调查。

## Probe Retry Increment (2026-10-12)

1. Add a probe-only context-aware policy to `common/retry`, leaving existing
   protocol retryers unchanged; typed transient/permanent classification,
   bounded exponential jitter, attempts and first-error diagnostics.
2. Append proto field 10 and JSON policy; validate limits and HEAD/GET, carry
   per-group settings to existing ping clients. Use one timeout context per
   logical request, preserving the shared transport and one final sample.
3. Reuse the helper in Sidecar's own spec003, pinning the approved clean Core
   commit only after Core tests/commit. Sidecar does not read Core OB config.
4. Deterministic callback tests cover arithmetic/attempts/classification;
   loopback H1/H2 tests cover RTT, pool/concurrency, final health, cancellation
   and stop. Repeat race, run relevant/full regressions and vet.
5. Commit locally before approved Darwin/Linux builds and config tests.
   No production retry config, binary installation, push, or deployment.

## Independent Probe Transport Increment (2026-10-14)

1. Append field 11 and optional JSON/runtime transport tag, preserving all
   existing subject/default semantics; do not modify dispatcher or SOCKS dialing.
2. Use that tag only for the request channel and keep every result under the
   observed subject. Cover defaults, group isolation, failure/recovery and cancel.
3. Run targeted/repeated race, affected regressions, vet, format and full tests;
   commit locally before approved Darwin/Linux build.
4. xray-config owns Shlii's real Core/Sidecar fixture, config-only behavior and
   rollout: unchanged egress through Core, local Sidecar UDS OB, independent
   budgets, no extra backend credentials and no other node deployment.
