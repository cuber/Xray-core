# Core 历史重建记录

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

## 第二阶段：每个 spec 一笔提交

用户在完成首轮等价重建后，要求每个 spec 一笔提交并补齐测试缺口，
批准按互不重叠的 spec 并行补测。当前工作分支为
`refactor/spec-series-20260927`，worktree 为 sibling `../xray-core-spec`。
初始七笔按 006、007、008、009、010、011、012 排序；补测及必要修复
归入对应 spec。最终分支 `refactor/spec-final-20260927`，worktree
`../xray-core-spec-final`，使用独立 worktree autosquash，不干扰运行中的补测。

| Spec | 最终 Core 提交 | 内容 |
|---|---|---|
| 006 | 3ddd5b5c | 连接生命周期、Hy2 锁及资源回收验证 |
| 007 | 0c9b2d16 | 多地址监听、UDS、控制面失败原子性 |
| 008 | 13a46cc5 | OB URL/健康状态、调度停止与取消 |
| 009 | 13e82315 | 路由控制面、用户匹配、加权健康池 |
| 010 | d220a582 | 用户域名流量、尽力 LRU、cursor、sniff 重放 |
| 011 | 5126d1af | 通用回归、格式门禁、资源释放、TLS 热重载 |
| 012 | cea5d5a1 | AnyTLS 协议及独立客户端/资源验证 |

编号调整：原末笔 c1e701d3 已 amend 为 cea5d5a1，标题改为 spec012，
仅更新 Core AnyTLS README 的规格路径。前六笔不变，仍为七笔，AnyTLS 最后。
`git diff c1e701d3 cea5d5a1 -- . ':(exclude)proxy/anytls/README.md'` 为空；
下文 c1e701d3 的测试与构建 hash 是实际历史证据，不改写成新 hash。

初次归并 tree `8c1962a6f2b7214ee73c360f86ed66e1d7791784` 与补测归并前
`945d56d2` 完全相同；最终追加 AnyTLS 测试夹具修复后 tree 为
`1d2252528bdaafd4c3351163b4ca75fd3712aed0`，仅 clients_test.go 和
integration_test.go 变化。详见 [012 证据](evidence-012.md)。
与原 e7a21974 的差异是本轮明确记录的补测及修复，不再宣称二者内容相同。
Sidecar 配套 spec010 一笔提交为 `4fad2a9`，spec009 消费者补测为 `78ec5c4`；正式 Core pin 仍为 e7a21974，
分别验证旧 Core 兼容和临时 modfile 指向新 Core 的消费者，不隐式切换依赖。

双平台 Core 构建通过：本地共享 clone 选取最终分支，通过原 wrapper 的
XRAY_CORE_SRC/XRAY_CORE_BRANCH/XRAY_BUILD_DIR 写入 `build/spec-final-20260927/`。
Darwin 实际执行版本 c1e701d，现有 local 配置 `run -test` 为 Configuration OK；
Linux 为静态 x86-64 ELF，VCS revision 为 c1e701d3ef9e30fa7c25d95326202c66b2d8931e，
modified=false。未覆盖默认 Core 产物，未部署或重启。
最终完整回归与逐笔结果见 [最终验收](acceptance.md)。

这一阶段允许已确认的缺陷修复，不再要求最终 tree 与旧 HEAD 完全相同；
等价重排和新增修复必须能分别追溯。原 develop、备份 branch/tag 不变。
跨仓库的 Sidecar 配套修改单独提交，不能声称两个仓库共享一笔 Git commit。

用户已确认域名统计是有界尽力缓存：Core LRU 淘汰直接删除明细，
不转移到 other；Sidecar 已采集历史独立保留，尚未采集的淘汰数据允许丢失。
不强求不同采集时机得到一致历史，不影响普通入站/用户流量计数器。
具体实现和验收以 spec/010 及最终验收记录为准。

以下是第一阶段已完成的等价重建记录，保留原始事实。

## 范围和备份

2026-09-27 用户批准分类整理、重建历史，并要求备份原分支。
此次不吸收 origin/main、不改变运行时行为、不部署、不改远端 develop。

- 原 develop / cuber/develop：`e7a2197424614e47d3e728da1d9cdded3454f559`。
- 本地备份分支：`backup/develop-before-restructure-20260927`。
- 本地 annotated tag：`archive/develop-before-restructure-20260927`。
- 分叉点：`b4f08981becb71eaa995fa98ed2098ade92566bb`。
- 重建分支：`refactor/fork-history-20260927`。
- 重建 worktree：sibling `../xray-core-restructure`。
- 单提交验证 worktree：sibling `../xray-core-history-check`，仅 detached checkout；验证完成后已移除。
- 重建 HEAD：`5841e6be177354fe578193f137ed7d7cd645ca05`。
- 新旧 tree：`5567fd700aa2b61cc8babf6d147f646e51cc28fe`，完全一致。

原 develop 保持不变；以上备份目前仅在本地，未声称已推送 GitHub。
撤回整理只需继续使用原 develop，原分支、源文件和发布产物不受影响。

## 分类和提交映射

| 新提交 | 类别 | 原始来源 |
|---|---|---|
| 61e60fe9 | 006 Dispatch 所有权/取消 | fa30790f、a6bd65e6 |
| 1e0405f7 | 008 OB 分组/URL/keepalive | 6f5f7928、dfae77f6、eb669065、751e4ef1；4c8ba18f 的 OB 配置测试 |
| c8cd15e0 | 009 路由域匹配/详细控制面 | 526589bb；4c8ba18f 的路由测试 |
| 942fb46c | 006 Hy2 manager 锁 | d4ce07cd |
| 715b2d70 | 007 Unix dial/redirect/SOCKS | 6c3e8fb7、5867a881、460516c5 |
| 0d2d65cf | 009 健康池加权调度 | 8fea74b9 |
| e15c0b36 | 008 HTTP 204/恢复迟滞 | 70355b3f、c22729d2 |
| 62b4f317 | 010 用户域名统计 | 48a99bad、eb7a9959、9cb1f966、1e0c6a30 |
| 883929ad | 007 多地址监听 | 9f82fd94 |
| 76e9dffa | 011 gofmt 门禁 | e7a21974 的 CI 文件 |
| 8263ef7b | 011 DNS 修复及隔离测试 | e7a21974 的 DNS、TLS/ECH、networktest、dnsfixture |
| 376ac43e | 011 XHTTP 生命周期/锁/缓冲 | e7a21974 的 splithttp |
| d47e1d76 | 011 VLESS 指针及 REALITY 本地测试 | e7a21974 的 VLESS outbound/scenarios |
| 47a806bb | 011 selector cache 并发 | e7a21974 的 outbound manager/test |
| c91f7eeb | 007 AddInbound 失败清理 | e7a21974 的 inbound manager 和 core/xray.go |
| 01a025d6 | 011 场景测试与资源清理 | e7a21974 的 common、blackhole、scenarios、UDP fixture |
| fd0b918c | 011 sing 依赖升级 | e7a21974 的 go.mod/go.sum |
| 5841e6be | 012 AnyTLS 协议/控制面/统计 | e7a21974 剩余协议、配置、注册、私网策略及测试 |

原 21 笔整理为 18 笔。原 4c8ba18f 横跨路由与 OB，按测试所属功能拆开；
重排时遇到 OB 测试 modify/delete 冲突，以原 751e4ef1 的完整测试内容解决，
不是删除测试。前九笔 tree 还单独与原 1e0c6a30 比较一致。

## 依赖边界

- OB 分组先于 weightedLeastPing；健康恢复后续独立改动。
- 用户域名统计、UDS 和多地址监听先于 AnyTLS 验收。
- DNS fixture 先于 VLESS REALITY 本地测试；sing 升级先于 AnyTLS。
- 通用 AddInbound 失败清理独立；ValidateStream 钩子仍随其首个使用者 AnyTLS。
- 通用启动失败的专项非 AnyTLS 测试仍是 V-009 缺口；没有为了拆 commit 伪造覆盖。
- 526589bb 中的旧 DoH 测试调整在后续 DNS fixture 提交覆盖，最终内容不变。
- proto 源定义与生成文件保持同笔；依赖文件与对应升级同笔。

## 验证方法与状态

Go `go1.26.1 darwin/arm64`。每笔执行 `go build ./...` 与对应模块定向测试；
日志和完整命令保存在本机忽略目录 `.cache/core-history-20260927/`。
阶段结果写入 `history-results.json`；最终回归结果另列，不把编译算成功能测试。

- PASS：原 21 笔累计内容与重建最终 tree 相同，`git diff --exit-code e7a21974 HEAD`。
- PASS：最终 `bash .github/check-gofmt.sh`。
- PASS：18 笔提交逐笔 `go build ./...` 与定向测试/门禁，共 36 项退出码为 0。
  路由条件测试实际名为 TestRoutingRule，发现首次过滤未命中后已补跑，日志
  `c8cd15e0-routing-test.log`；不将 no-tests-to-run 当作用户匹配验收。
  AnyTLS internal/engine 不使用 AnyTLS 测试名前缀，另跑全包通过，日志
  `final-engine-test.log`，最终 race 也覆盖该包。
- PASS：最终重点模块 `go test -race`，见 `final-race.log`；覆盖 transport、
  Hy2、OB、router、stats、dispatcher、proxyman、AnyTLS、XHTTP；不是整库 race。
- 首次完整回归：新 worktree 缺 resources/geosite.dat 和 geoip.dat，相关测试失败；
  已链接原 checkout 的同一份 DAT 资源，不修改测试以跳过该错误。
- `go vet ./...`：6 处既有报告，见 `final-vet.log`。原 HEAD 用 `go vet -a`
  强制重验对应包复现同样六处，见 `original-vet-forced.log`；不关闭 analyzer。
  具体为 VMess commands.go:33/67 不可达，router command.go:109 与 SS2022
  outbound.go:95 丢弃 cancel，VLESS inbound.go:585/586 unsafe.Pointer。
- PASS：补齐 DAT 后 `go test ./... -count=1 -timeout=15m` 退出码 0，
  全量日志 `final-test.log`；默认 opt-in 公网测试未启用，不代表公网验收通过。
- PASS：原清单 21 个提交/147 个路径集合完全一致；分类文档及关联入口
  46 份 Markdown、190 个本地链接目标检查通过；V-001 至 V-033 连续且唯一。
- 逐提交验证临时 worktree 已移除；重建 worktree 保留 DAT 文件级软链接
  （被 *.dat 忽略），工作树干净，可重新执行测试。

## 隔离双平台构建

现有 wrapper 要求 .git 为目录，不接受 linked worktree 的 .git 文件。
未为此改变 wrapper 或切换原 develop：从原本地仓库创建共享对象的临时 clone，
选择重建分支，通过已有环境变量运行标准 `xrayctl build core`：

```sh
XRAY_CORE_SRC="$PWD/.cache/core-history-20260927/build-source" \
XRAY_CORE_BRANCH=refactor/fork-history-20260927 \
XRAY_BUILD_DIR="$PWD/build/fork-history-20260927" \
  ./scripts/xrayctl.py build core
```

PASS：Darwin arm64/Linux amd64 均构建完成，Darwin 实际执行 version 为 5841e6b，
Linux 为静态 ELF x86-64，VCS revision 为重建 HEAD，vcs.modified=false。
新 Darwin 产物对现有 `xray/nodes/local/config-reality-raw.json` 执行
`run -test` 返回 Configuration OK；这是校验，不是 local deploy 或运行时切换。
产物只写入隔离目录，未覆盖默认 build/xray-*，未重启本地或远端服务。
xrayctl 末尾旧默认路径摘要不是隔离产物证据，以 wrapper 日志及上述实际文件为准。

## 后续切换边界

本阶段不移动 develop、不更新远端、不修改 Sidecar 的 Core pin。
Sidecar 当前通过 `replace ../xray-core` 和固定 hash 门禁使用原分支；
正式切换时需要同步 pin、重跑消费者检查、双平台构建和版本审计。
旧部署记录中的原 hash 是事实，不能批量替换。原备份在切换后也继续保留。

待补验收（尤其 V-025 已闭合桶 LRU/cursor 风险）不因 tree 相同自动闭环，
不在本阶段修复或声称全绿。完整矩阵见 006–011。
