# 路由与调度

- Feature ID: 009-core-routing-balancing
- Status: Implemented（核心补测、归并构建及真实 Sidecar/TUI RPC 消费者验证完成；未部署）
- Authorized: 2026-09-27，用户授权历史重建、隔离 worktree 补齐 V-015 至 V-018、
  窄修复及本目录文档同步；未授权部署。
- Source: 历史基线为原 Core e7a21974，相对 b4f08981；见 [005 清单](../005-core-fork-contracts/inventory.md)。
- Scope: 健康池加权轮询、用户域匹配和路由控制面；不吸收上游、不改原 core 或生产。
- 原 005 FR-006、FR-007 与 V-015 至 V-018 保持稳定；见 [本轮证据](../005-core-fork-contracts/evidence-008-009.md)。

## US-001 — 分类维护与独立验证（P1）

Given 历史分叉和受控的健康观测/真实 loopback gRPC，When 重建后补测并修复暴露的缺陷，
Then 记录选择分布、规则状态及并发行为，不以测试文件存在或 wire 兼容代替客户端验收。

## FR-001 — 当前实现契约

- weightedLeastPing 候选去重，只有有限正权重入池，默认权重 1。
  按配置顺序使用普通 substring 或正则的第一个有效匹配项，权重取字面值；
  无效正则跳过，首个匹配项的零/负/NaN/正负 Inf 不回退到后续项。
- 原共享 WeightManager 会将非正权重解释为自动数值/默认权重，本轮仅修复
  weightedLeastPing 的读取并保留加锁缓存，不改变 leastLoad 自动权重。
  JSON 继续拒绝零/负配置权重；原始 protobuf 可传非有限值，运行时排除。
- Alive、minSamples、失败率 tolerance、maxRTT 和 rttTolerance 决定健康资格。
  maxRTT 相等排除；bestRTT + rttTolerance 相等保留；tolerance=0 关闭失败率过滤。
- Burst 使用 Average，无 HealthPing 则用 Delay 毫秒。存在健康候选不混入 dead；
  观测缺失/出错或全不健康时回到全部有限正权重候选，空池返回空。
- 按 tag 稳定排序、平滑加权轮询、并发选择有锁；池外累计状态清理。
  稳定同健康池权重 1.5:1:0.5，串行及 12x50 并发 600 次均为 300:200:100，
  不声称字节比例相同。
- leastLoad 的 tolerance 生效；本轮仅补边界测试，不改变其算法。
- user 精确匹配大小写敏感；domain: 使用最后 @ 后完整后缀，忽略后缀大小写，
  不匹配子域或无 @ 输入。空 domain: 规则忽略、尾部空后缀不匹配；
  @la.att 允许匹配 domain:la.att。非法正则忽略，空 regexp: 仍按精确字面值匹配。
- ListRule 保留旧 tag/ruleTag 字段并返回详细规则和原有顺序，返回内容为深复制。
  AddRule shouldAppend=true 追加，重复 ruleTag 拒绝；false 替换整表，不是按 tag upsert。
  RemoveRule 删除后不残留，不存在 tag 幂等成功，空 tag 拒绝。
- 真实 gRPC 并发 Add/List/Remove 及整表替换/List 通过 race 验证；
  响应修改不改变内部规则。此结论不自动覆盖未测试的全部路由数据面并发路径。

## SC-001 — 验收与状态

V-015 至 V-018 核心 harness 已实现，新增 11 个顶层测试通过四包完整 race 及十次重复 race。
[测试矩阵](tests.md) 列明独立 oracle、操作序列和命令。
历史 tree 一致性见 [重建记录](../005-core-fork-contracts/reconstruction.md)；
本轮权重修复有意改变原始 protobuf 非正/非有限权重行为，累计 tree 不再要求等于历史 HEAD。
当前状态不是已提交、已发布或跨组件全部通过。

## 剩余边界

- 实际 Sidecar/TUI 解码未执行，core 旧字段 wire 测试不能替代真实客户端验收。
- 父进程负责审阅归并、统一隔离 clone wrapper 构建及后续跨组件收敛；
  本 worker 不再尝试构建。全仓测试和 Darwin/Linux 发布构建未声明通过。
- command.go 的取消修复属于 011；本类别只新增独立 contracts_rpc_test.go。
  测试运行在共享累计 tree，不代表已独立审阅其他 agent 的修改。

## 导航

[技术计划](plan.md) · [任务](tasks.md) · [检查清单](checklists/requirements.md)
