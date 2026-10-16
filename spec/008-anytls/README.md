# AnyTLS 文档分层

本目录保留一个 spec，不把认证、逻辑流、池和出站链式互通拆成互相竞争的规格。
当前范围以 [spec.md](spec.md) 为准；原入站阶段的非目标不再禁止已实现的出站扩展。

| 层 | 契约 / 复验 | 版本绑定证据 |
|---|---|---|
| 入站、认证、控制面及统计 | [contracts](contracts.md)、[tests](tests.md)、[pilot](pilot.md) | [acceptance](acceptance.md)、[implementation](implementation.md) |
| 出站、连接池、UoT、OB 与链式代理 | [outbound](outbound.md)、[outbound-tests](outbound-tests.md)、[impact](outbound-impact.md) | [final acceptance](outbound-final-acceptance.md)、[audit](outbound-current-audit.md) |
| 协议安全与资源预算 | [security-hardening](security-hardening.md) | 同页注明的源版本/测试，不覆盖后续版本 |
| singbridge 解耦 | [address-adapter](address-adapter.md) | 同页原始来源与验证边界 |

维护顺序：对应契约 → 测试方法 → 任务 → 带提交版本的验收。历史日志不改写为新 PASS。
共享连接所有权归 [Core002](../002-core-connection-lifecycle/spec.md)，OB 取消/健康归
[Core004](../004-core-observatory/spec.md)，通用回归归 [Core007](../007-core-regression/spec.md)。
升级候选的整体状态另见 [Core010](../010-upstream-main-upgrade/implementation.md)。
内部试点/公开分发的许可证边界继续有效，文档分层不授予发布或部署权限。
