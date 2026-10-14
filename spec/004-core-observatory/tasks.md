# Observatory 任务

- [x] T001 [US-001] 从 001 提取 FR-001 契约和 V-010 至 V-014 编号。
- [x] T002 [US-001] 历史阶段检查重建内容与依赖，编译并执行原有定向测试。
- [x] T003 [US-001] 保留历史 tree 对比和差距记录；与本轮新增修复明确分开。
- [x] T004 [US-001] 更新 tests.md 的可执行复验命令、逐项断言与证据链接。
- [x] T005 [US-001] 实现并执行 V-010 至 V-014 核心 harness；真实 HTTP、缓存恢复、
  快照、Stop、3s/2s/20 与 JSON 独立设置均覆盖。
- [x] T006 [US-001] 复现并窄修 Stop 缺陷；绑定 context、取消/发布锁屏障和 worker 回收，
  测试重复 Start/Stop 与阻塞 selector，声明无法强制终止回调的约束。
- [x] T007 [US-001] 完成四包 race、新增测试十次重复 race、gofmt；记录精确命令和结果。
- [x] T008 [US-001] 同步本目录 spec/plan/tasks/tests/checklist，保留外部链路、
  过期等值边界和 selector 资源所有权限制。
- [x] T009 [US-001] 父进程审阅、按 004 归并并统一隔离 clone wrapper 构建；
  双平台构建及全仓验证结果见 [最终验收](../001-core-fork-contracts/acceptance.md)。

本类别核心补测和修复已完成，尚未声明整个发布阶段完成。
历史记录见 [reconstruction](../001-core-fork-contracts/reconstruction.md)；
本轮证据见 [evidence-008-009](../001-core-fork-contracts/evidence-008-009.md)。

## HTTP/2 增量任务（2026-10-11）

- [x] T010 将布尔 http2 从 JSON/proto 传至主 OB transport，默认 H1，TLS ALPN 回退。
- [x] T011 补受控协商、连接复用/关闭、失败、取消、h2c 排除和配置往返测试。
- [x] T012 重复 race、相关回归、全库测试、vet、格式门禁通过并记录结果。
- [x] T013 本地提交、Darwin/Linux 正式构建及新/旧配置校验，保持生产不变。

HTTP/2 增量验证记录见 [验收证据](http2-acceptance.md)，明确全库的 opt-in 跳过项。

## Probe Retry Increment

- [x] T014 [FR-003] Implement the shared bounded/context-aware helper, typed
  errors and exponential capped jitter; unit tests and arithmetic boundaries.
- [x] T015 [FR-003] Add JSON/proto/runtime policy and idempotent-method gates;
  round-trip, default, invalid and group-isolation tests.
- [x] T016 [FR-003] One-deadline main probes, total RTT and one final sample;
  failure/recovery, H1/H2 pool isolation, timeout and scheduler-stop tests.
- [x] T017 [FR-003] Full regression, repeated race, vet and documented evidence;
  record the initial UDP scenario timeout and five successful isolated reruns.
- [x] T018 [FR-003] Local commit, approved dual-platform build and Darwin config
  validation. Later authorized Shlii-only enablement belongs in xray-config;
  this does not enable retries by default or authorize fleet deployment.

## Independent Probe Transport Increment

- [x] T019 [FR-004] Append JSON/proto/runtime transport tag; preserve subject keys.
- [x] T020 [FR-004] Add configuration, HTTP isolation, failure/recovery and cancel tests.
- [x] T021 [FR-004] Complete full tests/format, local commit and dual-platform build.
- [x] T022 [FR-004] Verify real Core/Sidecar UDS fixture through xray-config;
  production rollout and node-specific evidence remain owned there.
