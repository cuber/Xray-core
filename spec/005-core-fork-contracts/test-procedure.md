# 独立复验规程

适用 006–011 的 V-001 至 V-033。先读本页，再执行各 tests.md 的编号步骤。
本页提供复验步骤；实际执行结果与源码版本见 [最终验收](acceptance.md)，
不能只因步骤存在就声明测试通过。

## 1. 固定环境

- Core 测试 cwd 为本仓库根目录；先记录实际分支和 HEAD，不自动切换原 develop。
  Historical: 原 e7a21974 和等价重建 5841e6be 是旧基线；历史补测源为
  xray-core-spec，文档迁移前归并为 xray-core-spec-final 的
  refactor/spec-final-20260927。当前文档归属分支见 spec/README.md。
- 记录 `git rev-parse HEAD`、`git status --short`、`go version`、`go env GOOS GOARCH`。
  脏代码参与测试必须记录 diff，不能与 clean release 混称。
- Go/module 缓存和 DAT 已就绪。resources/geosite.dat、resources/geoip.dat
  使用 xray-config/xray/dat 的真实二进制文件，不接受 LFS pointer；记录 SHA-256。
  以已有 resources 链接为准，不覆盖文件或自动下载不同版本。
  新建 worktree 不继承被忽略的 DAT 文件，必须重新核查这两个路径可读；
  最终复验曾因遗漏此步骤失败，补齐同一资源后重跑，见最终验收。
- Sidecar 测试在 sibling xray-sidecar 执行，记录它的 HEAD、go.mod 的 replace
  和 Core pin。若要验证重建分支，使用隔离副本显式设置依赖并记录差异，
  不偷偷切换正式 sibling 或绕过 check-xray-core。
- 默认不启用 XRAY_TEST_NETWORK、XRAY_TEST_PROXY、XRAY_TEST_UDP_PROXY。
  公网验收仅按 V-033 单独执行。隔离测试不改 Surge、系统代理、DNS、正式服务。
- 数据/进程 fixture 使用 t.TempDir、127.0.0.1:0、独立 UDS 和固定测试身份。
  不占用正式 8080/9302；UDS 路径尽量短，避免 macOS sockaddr 路径长度限制。

## 2. 运行与证据

各用例中的命令是**已有测试**，在标明的仓库执行。`-run` 是 Go 正则，
先以同一包和正则运行 `go test -list '<pattern>' <packages>`，确认每个预期
顶层测试名实际存在。包内其他测试未被选中不等于通过。

将命令的 JSON stdout、stderr、退出码分别保存至 xray-config 的忽略目录
`.cache/core-spec-validation/<run-id>/<case-id>/`，不提交原始敏感日志。
以下以 V-001 为例，先在 shell 设置绝对 EVIDENCE 路径并创建目录：

```sh
go test -json -race -count=1 -timeout=5m ./transport \
  -run '^TestNewDispatchConn(DoesNotCloseWhenRunnerExits|CloseUnblocksRunnerRead)$' \
  > "$EVIDENCE/stdout.jsonl" 2> "$EVIDENCE/stderr.log"
rc=$?
printf '%s\n' "$rc" > "$EVIDENCE/exit-code.txt"
```

不要用管道后的 tee 退出码替代 go test 退出码。检查 JSON 中每个预期测试有
Action=run 和 Action=pass，包最终 pass，exit-code=0；skip、未命中、
编译失败、race 报告均不能计入该用例 PASS。stderr 也必须检查。
shell 若启用了 errexit，使用 if/else 捕获失败，避免丢失退出码文件。

每个 case 记录：源码版本、完整命令、选中测试列表、PASS/FAIL/SKIP/BLOCKED、
实际断言、资源前后快照、日志位置。**已有部分通过**和**完整场景通过**分栏记录。
新增 harness 尚未编写的部分标 NOT IMPLEMENTED，不编造可执行命令。

## 3. Fixture 与清理

- 并发测试使用 barrier/channel 协调顺序，不用偶然的 sleep 当因果保证。
  子任务须有 deadline；超时输出 goroutine 栈、阶段和资源计数，然后失败。
- 时间测试使用现有可注入 clock；不能修改系统时间，也不用真实等待 24h。
- 每个 listener、连接、Core/Sidecar 实例、HTTP transport 都注册 t.Cleanup。
  先取消任务，再关闭连接/listener，等待 goroutine/子进程退出，最后移除临时目录。
- 进程实验只终止自己启动并记录 PID 的进程；禁止 pkill xray/sidecar。
  停止后原端口能重新 bind、原 socket 无活跃 listener；遗留进程算验收失败。
- FD/线程/heap 先在预热后记录基线，再做至少 3 轮同负载对照。自己的资源计数
  必须归零；全进程 goroutine/heap 会有后台噪声，报告趋势而非武断要求绝对零。
- TLS fixture 使用专用根和 SAN，开启校验；不能 allowInsecure 换取通过。

## 4. 状态与退出标准

文档补全仅说明试验可描述；新增测试实现后须先证明故障注入能触发失败，再验证修复。
不得修改预期来迎合当前缺陷。V-025 已由用户确认采用有界尽力缓存：淘汰直接
删除明细、不搬 other，早/晚采集差异可接受；必须验证已返回副本不被修改、
已消费 cursor 不重复计数。V-022 回拨处理须写入 spec010 并有明确测试。

先运行逐项命令，再执行 005/tests.md 的完整回归和格式门禁。当前已知六处 vet
报告见 reconstruction.md，单独保留，不能用 -vet=off 声称整套门禁全绿。
构建/部署仍遵循 xrayctl；本轮已授权补测及必要修复，没有新增部署授权。
