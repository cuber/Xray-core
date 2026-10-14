# Spec 提交系列最终验收

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

## 固定输入

当前编号/提交：AnyTLS 已由 003 迁至 008，七笔顺序为002–008，最终 HEAD
`cea5d5a14e6b36de6177786fd16433c8635ae06e`。相对下列已验收 c1e701d3，
只修改 `proxy/anytls/README.md` 中的规格路径和提交说明，代码/测试未变；
本次验证重命名、链接与非文档 tree 等价，不虚报重跑全量或重新构建。
下文保留实际执行时的源码/产物 hash。

2026-09-27，Go1.26.1 darwin/arm64。Core 验收时分支
`refactor/spec-final-20260927`，HEAD `c1e701d3ef9e30fa7c25d95326202c66b2d8931e`，
tree `1d2252528bdaafd4c3351163b4ca75fd3712aed0`。
初次全量 race 在归并前 `945d56d2` 执行；最终仅追加 AnyTLS 测试夹具修复，
最终 HEAD 另行执行全量回归，不将不同代码树混记。
原 develop e7a21974 保持干净，备份 branch/tag 保留，未 force-push 或部署。

DAT 输入 SHA256：

- geosite.dat：35ed26a24cafa1256bd7261414224b7bcef5c944cea7760e172b030a8b266450
- geoip.dat：1cba1f0982cf62502fa079c66047c3d0c608196da5b3305671e68f60e917a482

## 逐笔验证

独立 detached worktree `../xray-core-spec-check` 逐笔 checkout，运行
`go build ./...` 和各 spec 对应模块测试。完整命令、退出码、通过/跳过事件数
保存在 `.cache/core-spec-acceptance/series-results.json`，对应原始日志按 hash 命名。
七笔全部完成，14 项检查 exit0，无零命中；临时 detached worktree 已移除：

| Spec | 测试/子测试通过事件 | 跳过事件 |
|---|---:|---:|
| 002 | 37 | 0 |
| 003 | 117 | 0 |
| 004 | 163 | 0 |
| 005 | 210 | 0 |
| 006 | 50 | 0 |
| 007 | 139 | 13 |
| 008 | 88 | 2 |

跳过主要是显式公网及未设置独立客户端变量；独立客户端另有单独启用证据，
不把通过事件数当作独立顶层测试数量。
最终 `go vet ./...`、`bash .github/check-gofmt.sh` 均 exit0。

## 全量与消费者

- Core Darwin：`go test -race -json ./... -count=1 -timeout=15m`，exit0。
  87 个有测试包、1080 个测试/子测试通过事件，15 个显式跳过测试，零失败。
- Core Linux：golang:1.26.1 容器、`--network none`、GOPROXY/GOSUMDB=off，
  独立可写源码副本、标准 DAT 路径，`go test -json -p 4 ./... -count=1 -timeout=15m`，exit0。
  Linux arm64：88 个有测试包、1081 个测试/子测试通过事件，16 个跳过，零失败。
  Linux amd64 为构建验证，未冒充该架构运行时测试。
  最终 c1e701d3 同一命令也 exit0，仍为88包、1081个测试/子测试通过、16个跳过，
  证据 `core-linux-current.jsonl`。
- Sidecar `4fad2a9`：标准 `xrayctl sidecar build` 通过完整测试，执行 Darwin
  版本后构建 Linux，Darwin 对 la.akile.lite 受管配置 `-test` 通过。
  使用正式旧 Core pin，证明升级 Sidecar 不要求先升级 Core。
- Sidecar 新 Core 消费者：临时 modfile 指向 xray-core-spec，完整 race exit0；
  459 个测试/子测试通过事件、7 个跳过；日志 `sidecar-consumer-final.jsonl`。
  没有改正式 pin。后续 `78ec5c4` 加入真实路由 RPC 消费者测试，
  [Sidecar/TUI 各十次 race 验证](evidence-009-consumers.md) 通过，不用普通单测替代。
  最终 Sidecar `78ec5c4` 在旧 pin 和新 Core modfile 下均重跑完整 race，
  各460个测试/子测试通过、7个显式跳过、零失败；新 Core modfile 下 vet exit0。
  标准 wrapper 重建 Darwin/Linux 并执行 Darwin 配置检查，均通过。
- TUI：临时 modfile 指向同一隔离 Core，`go test ./... -count=1` exit0，
  ui、xrayapi 两包通过，cmd 无测试；未部署本地 Xray。
  添加真实 RPC 测试后，正式依赖全包测试和新 Core 全包 race 也均 exit0。
  TUI 测试独立提交 `eaab884`，Sidecar 对应测试独立提交 `78ec5c4`，
  两者均归 spec005；详细证据注明测试时的未提交源码状态。
- AnyTLS 本地独立客户端与 Linux FD 复验见 [008 证据](evidence-012.md)。
- Protobuf 使用匹配工具真实重生成并校验全部生成头，见 [生成证据](evidence-protobuf.md)。

Sidecar 默认旧 Core pin 下 `go vet ./...` 仍报告旧 Core 的三处问题：
VMess 两处 unreachable code、SS2022 一处取消函数丢弃；它们已在新 Core
spec007 修复，但正式 pin 尚未切换。此项不标全绿，也不为消除报告越权切换依赖。

最终 worktree 首轮 Darwin 全量复验 exit1：缺少忽略目录 resources 下的两个
DAT 文件，导致 router、geodata、infra/conf 三包共四项测试失败，
日志 `core-final-head-race.jsonl` 保留。已链接到上述相同 SHA256 的本地资源，
不修改代码或跳过测试；修正后重新执行全量 race。
最终 c1e701d3 的 `go test -race -json ./... -count=1 -timeout=15m` exit0，
87包、1080个测试/子测试通过事件、15个显式跳过、零失败，
证据 `core-final-assets-race.jsonl` / `.stderr`。新 Core 的全量 race、
Linux 禁网全量、vet、格式门禁及逐笔构建测试均已完成。

文档最终检查：51份 Markdown、163个本地链接目标无缺失，`git diff --check` 通过。

原始最终日志均在 `.cache/core-spec-acceptance/`，包含 stderr；不提交含运行细节的原始日志。
默认 opt-in 公网测试及外部客户端环境未启用的 skip 不计为公网验收；
子进程未 race 插桩的场景不外推为子进程 race-free。

## 本轮实际修复

- LRU 直接丢弃明细，保留旧匿名汇总兼容；不把域名缓存当完整账本。
- Core 时钟回拨、cursor 溢出，以及 sniff 带数据+错误时的载荷重放。
- Sidecar schema1/2 快照验证，拒绝重复/不一致结构且不污染已有状态。
- OB 停止取消正在调度的探测，避免取消后继续发布样本。
- 加权调度按字面权重处理非正值/非有限值，不影响 leastLoad。
- SS2022 失败路径连接关闭、取消所有权、VLESS 指针与 VMess 不可达代码。
- TLS 热重载/OCSP 发布不可变快照，避免共享配置和握手读写竞态。
- 格式门禁依赖稳定分叉点，重建后新 clone 不依赖旧中间提交。

各项负向复现、精确断言和限制分别见 evidence-006-007、008-009、010-core、
010-sidecar、011；证据文件名保留历史编号，最终状态不能用局部测试替代全库结果。
