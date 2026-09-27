# 首轮文档审计记录

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

以下保留分类前的首轮检查事实；分类、重建及运行时测试的后续结果见
[重建记录](reconstruction.md)，不能将此处的“未执行”理解为后续也未执行。

## 逐项复验文档补充（2026-09-27）

用户要求补齐可由其他 agent 独立执行的验证步骤。本轮仅改文档：

- PASS：006–011 中 V-001 至 V-033 均有前置数据、已有命令、追加步骤、
  精确断言/覆盖边界、清理/证据及验证状态六项。
- PASS：35 条已有测试选择器与对应仓库测试源码核对，均存在匹配测试名；
  这是源码级检查，不是执行 go test。运行时仍须依 test-procedure.md
  验证每个预期测试有 run/pass 事件，skip/零命中不能算通过。
- PASS：005–011 的 104 个本地 Markdown 链接目标存在；编号连续唯一，
  必需段落、末尾空白和 git diff --check 检查通过。
- 未执行：本轮没有新增 harness、重跑 Core/Sidecar 测试、编译或部署。
  上轮实际回归证据保留在 reconstruction.md，不能冒充新增场景的验收结果。

下文保留分类前的原始审计结果。

- 日期：2026-09-27
- 范围：文档审计；未执行 Core/Sidecar 编译、单测、公网或生产验证。
- 固定 Core base/head 见 inventory.md；没有 merge/rebase/cherry-pick。

## 静态检查

- PASS：与 git rev-list 对照，21 个提交完全覆盖，无缺失或多余项。
- PASS：与 git diff --name-only 对照，147 个累计变更文件完全覆盖。
- PASS：新规格及受影响入口文档的 99 个本地链接目标存在。
- PASS：V-001 至 V-033 编号连续且唯一，12 个 FR 均有模块和验证归属；
  任务 T007-T011 明确分配后续测试范围，未执行任务保持未勾选。
- PASS：git diff --check 无空白错误；新增 Markdown 另做末尾空白检查。
- 仅 xray-config 文档变化；Core 和 Sidecar 工作区保持干净。

清单复核可从 xray-config 执行以下只读命令；必须按集合比较而不是只比较数量：

```sh
git -C ../xray-core rev-list b4f08981..e7a21974
git -C ../xray-core diff --name-only b4f08981 e7a21974
git diff --check
```

本次还用结构化 Markdown 表格提取对照上述两组集合，并检查相对链接目标。
这是文档检查，不是新的产品 CLI，也没有把测试矩阵当成自动通过报告。

## 运行时证据

没有新增。已有测试源码定位及历史记录只用于判断覆盖范围，不能算本次 PASS。
V-001 至 V-033 的补测/复验任务保持未勾选。特别是 V-025 的已发布桶改写、
V-028 的 uploadWriter 字节数等缺口，需后续授权再执行，不是已修复结论。
