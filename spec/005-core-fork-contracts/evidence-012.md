# AnyTLS 重建分支复验

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

2026-09-27，在 `../xray-core-spec` 执行；初始重排 HEAD `a603b44e`，
有其他 spec 的未提交补测/修复，故不是最终 clean release 验证。
本报告不覆盖原 003 的完整十分钟压测，不替代其历史验收记录。

## 模块 race

```sh
go test -race ./proxy/anytls/... -count=1 -timeout=5m
```

退出码 0；AnyTLS 包 47.750s，internal/engine 2.885s。
此命令未设置独立客户端环境变量，不能将其 skip 记为互通成功。

## 独立客户端

```sh
ANYTLS_SINGBOX=/Volumes/Linux/opensource/cuber/xray-config/.cache/anytls/tools/sing-box-1.14.2-darwin-arm64/sing-box \
ANYTLS_MIHOMO=/Volumes/Linux/opensource/cuber/xray-config/.cache/anytls/tools/mihomo \
go test -race -json ./proxy/anytls -run '^TestExternalClients$' -count=1 -timeout=5m
```

退出码 0；JSON 中 `TestExternalClients/SINGBOX`、`/MIHOMO` 及父测试
均有 pass 事件，无零命中或 skip。测试使用临时本地 Core、CA 和 loopback
监听，不更改 Surge/正式客户端配置，不部署服务器。
证据：`.cache/core-spec-acceptance/anytls-clients.jsonl`、
`anytls-clients.stderr`。覆盖由该测试定义的 AnyTLS、Hy2/SS-2022
回归及用户路由，不将短测当作十分钟压力测试。

最终合并后的整体回归和双平台构建见 [最终验收](acceptance.md)。

## 最终重复运行与测试夹具修复

最终第一次复验的 Mihomo 出现 EOF，保留失败日志 `anytls-clients-final.jsonl`。
Mihomo `hub/executor/executor.go` 的 `ApplyConfig` 先 `updateListeners`，
后 `tunnel.OnRunning`；`tunnel/tunnel.go` 在 Running 前关闭外部请求。
测试原先只等 SOCKS 监听，不能证明客户端已经可以转发。

增加独立 loopback DIRECT 回显作为有期限的就绪检查，不经过 AnyTLS；
正式十次 AnyTLS 请求仍无重试。随后百轮发现一次 TCP 空闲但 UDP 占用的端口冲突，
保留 `anytls-clients-ready.jsonl`；fixture 改成检查同端口 TCP/UDP 都可用。
端口交接仍有操作系统级竞争窗口，不声称释放后能够绝对保留。

最终相同独立客户端命令使用 `-count=100`，exit0，两个客户端各100轮，
无 skip；见 `anytls-clients-final-ready.jsonl`。两项均为测试夹具修复，
归入 spec012 的单笔提交，不改变 AnyTLS 服务端实现。

## Linux FD 断言修复

Linux `golang:1.26.1`、`--network none`、预置模块缓存、独立可写源码副本：
`go test -v ./proxy/anytls -run '^TestAnyTLSRepeatedConnectionsAndListenerClose$'
-count=3 -timeout=2m`。

修复前 exit 1，三轮 before=13、after=26/24/26，五秒内未降到断言阈值。
定位到测试回显服务的 `io.Copy(TCPConn,TCPConn)` 触发 Linux Go splice 管道池，
缓存 FD 被错误计入代理生命周期。将 fixture 源包装为只有 io.Reader 的对象，
使回显不使用 splice；不改变 Core 代理实现，不放宽 FD 阈值、不调用 GC 掩盖资源。

同一命令修复后 exit 0，三轮 before=13、after=12/12/13，总测试时间0.944s。
变化归 spec012 的 `proxy/anytls/integration_test.go`。该结果为局部 Linux复验，
不代表全部 Linux 测试或全部资源场景已通过。
