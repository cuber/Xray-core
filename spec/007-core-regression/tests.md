# 回归基础设施与通用修复验证矩阵

继承 001 稳定 V 编号。2026-09-27 在 `../xray-core-spec` 共享隔离 worktree
完成本轮补测；[执行证据](../001-core-fork-contracts/evidence-011.md) 为准确命令、
JSON 路径、文件归属和失败边界的权威记录。不是只存在文档或零测试命中。

| ID / 原 FR | 本轮实现与独立断言 | 实际状态 |
|---|---|---|
| V-028 / 006 | 1000 次 WaitReadCloser Set/Read/Close；等待唤醒、重复 Set/Close；下游立即释放 4096 字节，Write 返回4096 | focused race PASS |
| V-029 / 006 | 两 manager 共享配置，各1000次读取；默认/显式范围、最小 chunk 归一化；proto.Clone + proto.Equal 不变性 | race PASS，scoped vet PASS |
| V-030 / 006 | A/AAAA 并发同一 slice 各1000次；DoH/DoQ 私有根成功、未知根/错SAN失败；DoH 失败各100次 FD | race PASS，无关闭证书校验 |
| V-031 / 006,008 | 同进程 TLS/uTLS/REALITY、RAW XOR0/1/2、加密外层TLS；inner TLS1.3 64KiB固定载荷，反复GC与关闭；错误 pin | 八情形三轮 race/checkptr PASS，有覆盖计数 |
| V-032 / 006,008 | 四读者并发1000轮Add/Remove；删除完成后新Select无旧缓存；SS2022 TCP/UDP与timeout-only资源对照 | race/协议 PASS；新修早期TCP连接泄漏 |
| V-033 / 007,008 | SOCKS成功/拒绝、目标accept=0；浅clone无旧baseline，tracked fmt负例哈希不变；Linux network-none整库 | 代理/格式PASS；父流程整库验收见 001/acceptance.md |

## 复验命令

cwd 为 Core worktree。所有命令应保留 `-json` 输出和退出码；完整实际执行命令见证据。

### V-028 / V-029

```sh
go test -json -race -count=1 -timeout=5m ./transport/internet/splithttp \
  -run '^Test(WaitReadCloser|UploadWriter|Normalization)'
```

fake 下游复制实际接收字节后立即 ReleaseMulti，不能只检查返回 n。
WaitReadCloser 用读者 barrier 与关闭计数，所有成功测试的 goroutine join。
manager 无外部资源的 fake conn；proto 对照为独立 Clone，不是同一指针别名。

### V-030

```sh
go test -json -race -count=1 -timeout=5m ./app/dns \
  -run '^Test(LocalDNSTransports|LocalNameServerFixture|DNSTrustAndFailedHandshakeFDs)$'
go test -json -race -count=1 -timeout=5m ./proxy/dns \
  -run '^TestSharedSliceNormalization$'
```

共享 slice 测试直接覆盖 handleIPQuery，不仅测 helper。
FD 数值见证据；server/HTTP transport/QUIC listener/cache timer 都关闭，
不以 GC 清理隐藏错误，不把单纯连接拒绝误认为证书验证成功。

### V-031

```sh
go test -json -race -gcflags=all=-d=checkptr=2 \
  -coverpkg=./proxy/vless/inbound,./proxy/vless/outbound \
  -coverprofile=/tmp/spec011-vless-final.cover ./testing/scenarios \
  -run '^TestVlessVisionGCMatrix$' -count=3 -timeout=5m
```

成功模式每轮三条连接、每条65536字节；inner TLS1.3 触发 Vision direct-copy，
在 echo 期间执行 GC，停止 GC 后 join，关闭 Core 与监听。
覆盖证据必须包括 inbound CommonConn/TLS/REALITY 与 outbound CommonConn/TLS/uTLS/REALITY；
不能用外层 `go test -race` 包裹未插桩的 Core 子进程冒充通过。
初次发现的 TLS 热重载 race 已在追加授权后真正修复，OneTimeLoading 规避已删除。
按上述命令重新三轮复验的输出为 spec011-vless-reload-final.json/cover，全部通过。
REALITY 等待真实目标探测，不注入成功缓存。

追加热重载验证：

```sh
go test -json -race ./transport/internet/tls \
  -run '^Test(CertificateHotReloadConcurrentHandshake|OCSPRefreshPreservesPublishedSnapshot)$' \
  -count=3 -timeout=1m
```

修复前真实 DATA RACE 和 source proto 变更断言 FAIL，修复后两测试各三轮 PASS。
真实文件证书轮换、四并发且验证信任的握手、坏文件保留有效版本、未知 SNI 拒绝，
以及本地签名 OCSP 响应刷新/旧握手快照不变都有独立断言；不靠禁用热重载通过。

### V-032

```sh
go test -json -race -count=1 -timeout=5m ./app/proxyman/outbound \
  -run '^TestTagsCache(Invalidated|ConcurrentInvalidation)?$'
go test -json -race -count=1 -timeout=5m ./proxy/shadowsocks_2022 \
  -run '^TestOutboundFailureClosesConnection$'
go test -json -race -count=1 -timeout=5m ./testing/scenarios \
  -run '^TestShadowsocks2022(Tcp|UdpAES128|UdpAES256|UdpChacha)$'
```

缓存测试区分在途快照与 Remove barrier 后新查询，不误报合法在途结果。
SS2022 TCP 三算法及 UDP 三算法互通通过；场景 Core 子进程自身并未由外层 -race 插桩。
单包资源负例已插桩，修复前 TCP normal/timeout-only 均漏 Close，修复后 TCP/UDP 全过。

### V-033

```sh
go test -json -race -count=1 -timeout=5m ./testing/networktest \
  -run '^Test(ProxyRouting|BadProxyNoDirectFallback|FormatGateRejectsWithoutModification)$'
bash .github/check-gofmt.sh
```

格式负例在独立临时浅 clone 的已跟踪 Go 文件制造语法有效的 fmt 错误；
只取 tip 与稳定 fork point，确认旧 1e0c6a30 不存在；门禁非零且文件SHA256不变。
无需手改当前 worktree 负例；临时 clone 自动清理，没有 commit/checkout。

Linux 禁外网使用 `docker run --network none`，依赖和 DAT 预置，源码复制为独立可写快照；
准确挂载参数、完整运行结果、既有 skip 名称见证据。历史范围外失败已由所属流程修复，
子任务最后一次整合运行按要求中止；父流程已独立完成全库验收，
具体源码版本和范围见 [最终验收](../001-core-fork-contracts/acceptance.md)。
显式启用 TestPublicDOHNameServer 配坏代理，
实际 FAIL/exit1、无skip，是预期负向证据而非公网成功。
不改变宿主防火墙，不声称公网 DoQ/UDP ASSOCIATE 已验证。

## 六条 Vet 与发布边界

父流程的 VMess不可达×2、SS2022 lostcancel、VLESS unsafe-pointer×2 保留并补验证；
Router lostcancel 改为异步期限所有权，而非 RPC 返回时取消。六条已关闭，
scoped vet、现有 TestServiceTestRoute 20次均通过，command测试文件归其他agent。

release wrapper 在本 linked worktree 拒绝 .git 文件，未执行构建；父流程最终 clean tree
双平台 build 和整库/race 全绿仍需单独验收。本轮没有生产操作、commit 或重写历史。
