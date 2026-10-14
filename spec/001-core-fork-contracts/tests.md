# 验证矩阵与执行方法

逐项操作前必读 [独立复验规程](test-procedure.md)：版本、DAT、测试命中检查、
证据记录、资源回收和通过判定。各类别 tests.md 包含独立的逐项步骤，
已有可运行命令与待实现 fixture 明确分开。

完整矩阵已按类别拆分，V-001 至 V-033 保留编号：

- [连接生命周期：V-001 至 V-004](../002-core-connection-lifecycle/tests.md)
- [监听与 UDS：V-005 至 V-009](../003-core-listeners-uds/tests.md)
- [Observatory：V-010 至 V-014](../004-core-observatory/tests.md)
- [路由与调度：V-015 至 V-018](../005-core-routing-balancing/tests.md)
- [用户域名流量：V-019 至 V-027](../006-user-domain-traffic/tests.md)
- [回归基础设施与通用修复：V-028 至 V-033](../007-core-regression/tests.md)

## 分层命令（参考清单，实际执行见 reconstruction.md）

从本 Core 仓库根目录执行，保留日志及退出码，禁止只截取成功行：

```sh
bash .github/check-gofmt.sh
go vet ./...
go test ./transport/... ./app/proxyman/... ./common/singbridge/... ./proxy/freedom ./proxy/socks ./infra/conf -count=1 -timeout=10m
go test ./app/observatory/... ./app/router/... ./app/stats/... ./app/dispatcher/... -count=1 -timeout=10m
go test -race ./transport/... ./app/proxyman/... ./app/observatory/... ./app/router/... ./app/stats/... ./app/dispatcher/... -count=1 -timeout=15m
go test ./... -count=1 -timeout=15m
go test -race ./... -count=1 -timeout=15m
```

Sidecar 使用其当前锁定的 Core 依赖执行 `go test -race ./...`；
联调前核对 go.mod 的 replace/pin、proto 描述符和实际 Core 二进制 revision。
不能假定本仓库 HEAD 自动就是 Sidecar 编译使用的版本。

公网测试按 Core testing/README.md 显式开启，本次历史重建不执行公网验收。
双平台构建仍走 xray-config 的 `./scripts/xrayctl.py build core` 和
`./scripts/xrayctl.py sidecar build`。本次 Core 隔离双平台构建见 reconstruction.md，
没有重编译或部署 Sidecar。

## 隔离端到端验收

1. 未来测试 harness 在临时目录生成 Core/Sidecar 配置、测试证书及 UDS，
   使用系统分配空闲端口；先运行各自 -test，再启动，不替换正式服务。
2. 两个认证用户连接本地回显服务，显式域名目标与 IP-only 各一组；
   原始 RPC 获取桶，对照固定载荷，再对照 Sidecar 未截断汇总。
3. 用受控 HTTP 端点改变 OB 返回码，逐次验证不健康/恢复和选择次数；
   不以真实公网 RTT 作为可重复 oracle。
4. 先重启 Core，再重启 Sidecar；分别核对 boot/cursor/快照与字节，不清空
   快照来规避恢复问题。强制预算淘汰与 V-025 另测。
5. 停止全部临时进程，确认端口和 UDS 无活跃监听，清理 fixture 文件；
   记录失败阶段，保持线上 Xray、Surge、Sidecar 不变。

上面 harness 是验收步骤，不是声称已存在的新 xrayctl 子命令。
新增测试缺口完成后才可填写 PASS。008 的 AnyTLS 互通、撤销、资源上限测试
仍必须独立执行，不能被本矩阵替代。
