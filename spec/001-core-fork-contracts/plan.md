# Plan: 回溯契约与独立验证

- Spec: [spec.md](spec.md)
- Status: Implemented；分类、每 spec 一笔提交、缺口补测及记录中的必要修复已完成；未部署。

## 原则检查

I/事实：固定 base/head，逐提交及逐文件建 inventory；已有测试不等于已执行。
II/可追溯：Core 独立 worktree 重建，原分支与备份引用保留；不改生产配置。
III/兼容：将上游候选与本地契约分离，未知行为登记差距，不自动“优化”。
IV/边界：008 管 AnyTLS，002–007 管各类别契约，001 管完整性和重建追溯。
V/验证：独立回显、固定时钟、真实 RPC、资源计数，不以实现自产预期作 oracle。
VI/副作用：本次不运行部署、公网探针、DNS 或服务重启。
VII/渐进：先补规格，后续按模块补缺、验证，最后才讨论上游吸收。

## 阶段

1. 审计提交和累计 diff，拆出 AnyTLS 提交中的独立运行时变更。
2. 记录 K1-K6 契约，关联已有测试，标记未覆盖边界与风险。
3. 更新入口和旧 DomainTraffic 文档的当前/历史边界，校验链接与清单集合。
4. 按追加授权实现 V-025/V-028 等缺口测试与修复；LRU 以用户确认的尽力缓存
   为准，其余问题先保留负向复现，不得先改预期掩盖问题。
5. 运行模块单测、race、完整回归、双平台构建和隔离端到端；
   保留原始失败，不将已存在缺陷归咎于上游升级。
6. 本地基线稳定后才进入独立上游吸收计划，以同一输入比较 before/after。

## 证据要求

每次验收记录 Core/Sidecar/config 提交、Go/OS、配置摘要、测试 fixture、
命令、退出码、PASS/FAIL/SKIP 数量、日志和资源前后值。报告不含密码/私钥/token。
临时运行使用随机高端口和独立 UDS/快照路径；不占用正式 8080/9302，
不改现有 Surge/本地 Xray 服务；异常退出也应回收子进程和监听。

性能不预设未经测量的 MB 数值：同一负载做至少三次 before/after，
报告 RSS、heap、FD、goroutine、吞吐、p95 延迟。
功能 oracle 必须精确；资源泄漏用已关闭实例的 FD/监听归零和多轮稳定趋势判断，
GC 后 heap 不必字节完全相同。容量项则严格检查组合数和记录预算。

## 迁移与回滚

历史重建的备份、依赖与验收见 [reconstruction.md](reconstruction.md)。
第一阶段最终 tracked tree 必须与原 HEAD 相同；原 develop 保持不变。
未来 proto 或配置变动先验证消费者读取旧/新服务；
新增 listen_addresses、domainTraffic 和 AnyTLS 不允许直接喂给不支持的旧二进制。
兼容旧版本须准备配套配置，不以替换 core 文件作为唯一回滚步骤。
双平台 release 构建沿用 xrayctl，clean tree、VCS 元数据和本地 Darwin 执行要求不变。

## 实际结果

首轮文档静态校验见 [validation.md](validation.md)。
分类重建的实际编译、测试和原始问题见 [reconstruction.md](reconstruction.md)。
追加验收已分别记录于 evidence-*.md，主流程完整回归、逐笔验证及本地构建
汇总见 [acceptance.md](acceptance.md)，未执行项与实际通过保持区分。
