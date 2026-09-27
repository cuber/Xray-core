# Tasks: AnyTLS

## Outbound extension (2026-09-27)

The following new work is not covered by the checked historical inbound tasks.
See [outbound contract and acceptance](outbound.md).

- [x] O-T01 ClientConfig, JSON/protobuf validation and registration (OT-01); tables and real gRPC rejection pass, see outbound-implementation.md.
- [x] O-T02 Native TCP pool, dialing, cancellation and graceful retirement (OT-02..05, OT-07, OT-12); lifecycle, resource and chained-admission closures in outbound-current-audit.md.
- [x] O-T03 UoT v2 packet adapter (OT-06); adapter/native boundary and interoperability evidence reconciled in outbound-current-audit.md.
- [x] O-T04 Real control-plane and statistics integration (OT-08..11); native gRPC/wire counters and candidate Sidecar live consumer tests mapped in outbound-final-acceptance.md.
- [x] O-T05 External-server interoperability, regression and builds (OT-02, OT-13..14); final local acceptance and artifact evidence in outbound-final-acceptance.md.
- [x] O-T06 Complete requirement-to-evidence review; outbound-final-acceptance.md maps OT/B/C rows and preserves revision/platform boundaries.
- [x] O-T07 Execute real OB and egress matrix B-01..B-08 in outbound-tests.md; composition and cancellation closures recorded in the audit.
- [x] O-T08 Execute chain matrix C-01..C-07 in outbound-tests.md; exact counts, pending-hop cancellation repair, external chains and separate-process verification passed.
- [x] O-T09 Review every shared-code diff using outbound-impact.md; per-file/hunk reconciliation and independent shared-module regressions recorded at fca957f5. This review does not close the remaining protocol-local lifecycle or release gates.

Local outbound implementation and acceptance are complete. Final OT/B/C mapping
and execution boundaries are in [outbound-final-acceptance.md](outbound-final-acceptance.md).
Historical failures and their resolutions remain in the chronological evidence;
production pin updates, push and deployment are not part of this local acceptance.

Current bounded gap review: [outbound-current-audit.md](outbound-current-audit.md).

实现及 SHI 单节点试点已完成，仅内部试点；公开分发与推广仍不在范围。
完成时补实际提交和验证证据，不因授权就勾选测试任务。

当前进度见 [implementation.md](implementation.md)，逐项验收映射见
[acceptance.md](acceptance.md)。本地功能、恢复及资源闸门已通过；最终构建
SHI 十分钟验收、独立残留核验和版本检查通过。

## 前置

- [x] T001 决定内部试点的协议库版本及许可边界 — research D-001；依赖：无；覆盖：NFR-004；2026-09-26 用户接受 pinned GPL 引擎用于内部试点，保留原通知；公开分发另行处理，不 push 含该实现的代码或发布二进制。
- [x] T002 验证 sing 兼容与客户端基线 — Core go.mod/singbridge，测试夹具；覆盖：NFR-004/001；最小依赖 diff、双平台构建、独立客户端及 SS-2022/Hy2 回归，证据见 implementation.md。

## TCP 与统计

- [x] T003 [US-001] 新增 Account/ServerConfig、JSON 校验和 inbound 注册 — Core proxy/anytls、infra/conf、all.go；依赖：T002；覆盖：FR-001/002/006/014；AT-001 至 AT-004、本地二进制 run -test 通过，Core 187bf0ab。
- [x] T004 [US-001] TLS/认证/逻辑流上下文桥接 — Core proxy/anytls；依赖：T003；覆盖：FR-003/005/008/013；验证：AT-005 至 AT-007、AT-020/022/027，含嗅探与存活流复验。
- [x] T005 [US-002] 接入 inbound/user/user+domain 三维计数 — Core 协议适配、Sidecar 查询及测试，不另建统计模块；依赖：T004；覆盖：FR-004/001/016；验证：AT-008 至 AT-011、AT-033 至 AT-035，八种开关、精确上下行、快照重放通过。

## 动态身份与 UDP

- [x] T006 [US-003] UserManager、generation 和严格撤销 — Core proxy/anytls；依赖：T004；覆盖：FR-006/003；验证：AT-012 至 AT-015，含 -race；同锁准入及旧 generation 隔离。
- [x] T007 [US-004] UoT v2 与逐 UDP 目标调度 — Core 协议适配；依赖：T005/T006；覆盖：FR-003/004/004；验证：AT-016 至 AT-019，双用户精确上下行和回包源。
- [x] T008 [US-004] 资源预算、握手超时、异常关闭 — Core 入站/会话适配；依赖：T006/T007；覆盖：FR-009/008、NFR-001/002/003；验证：AT-020 至 AT-022、四种 60 秒 fuzz、RST/慢读/真实 UoT 回收。

## Sidecar 与收敛

- [x] T009 [US-003] 用户账户 factory、入站类型识别及混合同步 — sibling sidecar；依赖：T006；覆盖：FR-006/007/013；验证：AT-023 至 AT-027，SS-2022 无回归，Sidecar 全量 race/vet 通过。
- [x] T013 [US-003] 完整控制面集成与生命周期验收 — Core HandlerService 集成测试、AnyTLS proto 注册、Sidecar xray-debug 类型解码；依赖：T003/T006/T009；覆盖：FR-015；验证：真实 gRPC AT-029 至 AT-032，原生 proto 坏证书拒绝、API-only 入站带用户重启恢复。
- [x] T010 [US-004] 双客户端互通和压力验收，定稿默认上限 — xray-config 的 test/、Core/Sidecar 测试；依赖：T005/T007/T008/T009/T013；覆盖：NFR-001 至 NFR-005；验证：十分钟 100 流/双 marker/精确计数、80 轮 UoT 资源回收，resource-audit.md。
- [x] T011 全量相关回归与 Darwin/Linux 可追溯构建 — Core/Sidecar/build 入口；依赖：T010；覆盖：FR-014、NFR-004/001；验证：AT-028/036、SC-001 至 SC-004，包含 Hy2 UDP / AnyTLS TCP 同数字端口共存；整库既有失败明确记录。
- [x] T012 文档、命令识别和最终验收报告 — 组件 README/docs、适用 xrayctl 检查测试、此 spec；依赖：T011；覆盖：SC-001 至 SC-005；验证：acceptance.md/implementation.md 映射与复现命令，无新增 outbound/订阅或其他节点变更，基线失败和内部许可边界可见。

## 已授权的单节点试点

- [x] T014 核实并固化 SHI 的可用 SSH 跳板、备份和试点声明 — xray-config 节点配置、适用 SSH/docs、私有测试目录；依赖：T011 的单节点必要门槛（通用容量验收不外推）；验证：pilot.md P-001，CTC 路径、TCP 2083 空闲和证书通过，旧二进制/配置已备份，保留全部旧入站。
- [x] T015 部署并由本地 sing-box 进行远端验证 — xrayctl 构建/部署入口、SHI、隔离客户端；依赖：T012/T014；覆盖：FR-001 至 FR-016；验证：pilot.md P-002 至 P-008，最终 Core 187bf0ab / Sidecar 663362e，600 秒及三维统计/控制面/清理通过，不扩大到另一台 CO。
- [x] T016 清理临时测试状态并记录试点结果/回滚状态 — SHI 受管配置、隔离客户端、spec；依赖：T015 受控试点；验证：pilot.md 清理与回滚章节；最终完整十分钟通过，临时账户/路由/出口/进程清零，保留空账户、默认拒绝配置，未测项目未记通过。
