# Spec011 补测与修复证据

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

2026-09-27；共享隔离 worktree：`/Volumes/Linux/opensource/cuber/xray-core-spec`。
Darwin arm64、Go 1.26.1；Linux arm64 为 Docker/OrbStack、golang:1.26.1。
本 agent 未 commit、checkout、rebase、部署，未修改 sibling 正式 Core 工作树。
本记录只认下列实际命中测试，不以整库其他 agent 的修改归功本项。

源码及测试已冻结。父流程已将同内容归入 fixup；冻结确认时 Core HEAD 为
`945d56d2149bf50b82071a5f3bcb5dfcabd2f98d`，`git status --short` 为空。
后续本 agent 仅更新文档，最终 clean-tree 全库/逐提交/双平台验证归父流程。

## 文件归属与实际修复

以下路径相对 Core worktree：

| 文件 | 本轮归属及行为 |
|---|---|
| `proxy/vmess/encoding/commands.go` | 复核并保留父流程删除的两处不可达代码；未知命令和认证错误语义不变 |
| `proxy/vmess/encoding/commands_regression_test.go` | nil/未知 Marshal 不写输出；全部 256 command ID 的未知类型/错误认证、短输入 |
| `proxy/shadowsocks_2022/outbound.go` | 保留父流程 timeout-only deferred cancel；新增成功 Dial 后 defer Close，修复早期 TCP payload 读取失败泄漏 |
| `proxy/shadowsocks_2022/outbound_regression_test.go` | TCP/UDP × normal/timeout-only 失败路径资源对照；修复前两个 TCP 子例真实 FAIL，修复后全部 PASS |
| `proxy/vless/inbound/inbound.go` | 复核并保留父流程 unsafe.Pointer + unsafe.Add，消除两处 vet unsafe-pointer 告警 |
| `testing/scenarios/vless_gc_test.go` | 同进程 Core，GC + 64KiB 固定载荷 + inner TLS1.3，重复建连关闭；覆盖 TLS/uTLS/REALITY、RAW XOR0/1/2、加密外层 TLS；坏 pin 必须失败 |
| `transport/internet/tls/config.go` | 用户追加授权；watcher 私有 proto 副本、atomic 发布不可变叶证书/CA 快照，OCSP copy-on-write；保持真正文件热重载，ticker 退出时 Stop |
| `transport/internet/tls/reload_test.go` | 真实磁盘证书轮换与四并发校验握手，坏证书保留最后有效版本、未知 SNI 拒绝、proto 不变；真实本地签名 OCSP 刷新与旧快照不变 |
| `app/router/command/command.go` | 只改取消所有权。Publish 异步持有 ctx，不能在 RPC return 时 defer cancel；WithTimeout + context.AfterFunc 在四秒期限结束取消，不提前丢投递 |
| `transport/internet/splithttp/dialer.go` | uploadWriter 的下游依赖由具体 pipe.Writer 收窄为 buf.Writer，Close 转发给 common.Close，便于立即释放 fake；释放前保存长度的修复原已存在 |
| `transport/internet/splithttp/regression_test.go` | fake 接收即释放 4096 字节，Write 必须返回 4096；共享 proto.Clone 对照、两个 manager 各 1000 次归一化 |
| `transport/internet/splithttp/wait_reader_test.go` | Set/Read/Close 交错 1000 轮；补等待唤醒、重复 Set、重复 Close 的关闭计数 |
| `proxy/dns/shared_slice_test.go` | 直接调用 handleIPQuery，A/AAAA 各 1000 次共用同一 slice，独立深复制对照；归一化私有 slice 原已存在 |
| `app/dns/trust_regression_test.go` | DoH/DoQ 私有根成功、未知根和错误 SAN 拒绝；DoH 两类失败各 100 次，FD 不持续增长，无 GC 掩盖回收 |
| `app/proxyman/outbound/cache_concurrent_test.go` | 四并发读者 + 1000 次 Add/Remove；Remove 完成 barrier 后新 Select 不含旧 handler |
| `testing/networktest/regression_test.go` | 拒绝 SOCKS 时真实目的端 accept=0；独立临时浅 clone 的 tracked fmt 负例、源文件 SHA256 不变、旧 baseline 对象确实不存在 |
| `.github/check-gofmt.sh` | 改用稳定 fork point b4f08981becb71eaa995fa98ed2098ade92566bb；不依赖重写前中间提交 1e0c6a30 |

路由 command 测试由其他 agent 所有，本轮未编辑其测试文件。
VLESS outbound 运行时未修改，覆盖证明读取分支已命中。
`testing/scenarios/vless_test.go` 最终无 diff。
父流程的 protobuf 真正重生成属于 007/008/010，见
[独立 protobuf 证据](evidence-protobuf.md)，不计入 011。

## 本地命令与结果

除构建 wrapper 外，cwd 均为上述 Core worktree；JSON 原始输出保存于本机 `/tmp/spec011-*.json`。

### Focused Race：PASS，exit 0

冻结前最终合并定向验证（包含 TLS 两项和 blocked/repeated Set）：

```sh
go test -json -race ./app/dns ./proxy/dns ./transport/internet/splithttp \
  ./proxy/vmess/encoding ./proxy/shadowsocks_2022 ./app/proxyman/outbound \
  ./testing/networktest ./transport/internet/tls \
  -run '^Test(DNSTrustAndFailedHandshakeFDs|LocalDNSTransports|LocalNameServerFixture|SharedSliceNormalization|UploadWriterReleasedLength|NormalizationSharedConfig|WaitReadCloserLifecycle|WaitReadCloserConcurrentSetClose|WaitReadCloserBlockedAndRepeatedSet|UnsupportedCommands|OutboundFailureClosesConnection|TagsCacheConcurrentInvalidation|TagsCacheInvalidated|ProxyRouting|BadProxyNoDirectFallback|FormatGateRejectsWithoutModification|CertificateHotReloadConcurrentHandshake|OCSPRefreshPreservesPublishedSnapshot)$' \
  -count=1 -timeout=5m > /tmp/spec011-freeze-focused.json
```

exit 0，54 个 test/subtest PASS，0 FAIL、0 skip。下列为此前分阶段记录：

```sh
go test -json -race ./app/dns ./proxy/dns ./transport/internet/splithttp \
  ./proxy/vmess/encoding ./proxy/shadowsocks_2022 ./app/proxyman/outbound \
  ./testing/networktest \
  -run 'Test(DNSTrustAndFailedHandshakeFDs|LocalDNSTransports|LocalNameServerFixture|SharedSliceNormalization|UploadWriterReleasedLength|NormalizationSharedConfig|WaitReadCloserLifecycle|WaitReadCloserConcurrentSetClose|UnsupportedCommands|OutboundFailureClosesConnection|TagsCacheConcurrentInvalidation|TagsCacheInvalidated|ProxyRouting|BadProxyNoDirectFallback|FormatGateRejectsWithoutModification)$' \
  -count=1 -timeout=5m > /tmp/spec011-focused-final.json
go test -json -race ./transport/internet/splithttp \
  -run '^Test(WaitReadCloser|UploadWriter|Normalization)' -count=1 -timeout=5m \
  > /tmp/spec011-xhttp-final.json
```

前一命令 51 个 test/subtest PASS，无 skip；后一命令包含随后补齐的 blocked/repeated Set 场景。
DoH FD 记录：trusted 8->9；unknown-root 100 次 9->7；wrong-san 100 次 8->8。
允许服务器最后一个连接关闭的短暂延迟，最大容差 2；没有调用 runtime.GC 清理泄漏。

### VLESS：PASS，exit 0

```sh
go test -json -race -gcflags=all=-d=checkptr=2 \
  -coverpkg=./proxy/vless/inbound,./proxy/vless/outbound \
  -coverprofile=/tmp/spec011-vless-final.cover ./testing/scenarios \
  -run '^TestVlessVisionGCMatrix$' -count=3 -timeout=5m \
  > /tmp/spec011-vless-matrix-final.json
```

八个子例三轮，含父测试共 27 PASS，无 skip。每轮成功模式各三条连接、每条 65536 字节；
inner TLS1.3 会触发 Vision direct-copy，XOR/外层加密不能被错误穿透。
覆盖计数证实 inbound CommonConn/TLS/REALITY 和 outbound CommonConn/TLS/uTLS/REALITY
分支、unsafe.Add 取 input/rawInput 均命中（不是子进程未插桩的假覆盖）。

初次矩阵发现 `transport/internet/tls/config.go:90/253` 热重载写读 race；
用户随后明确扩展 TLS 为 011 专属范围，现已真正修复，**不是遗留风险**。
临时使用过的 OneTimeLoading 已从 VLESS fixture 移除，未保留该规避方式。
原始失败栈：`/tmp/spec011-vless-checkptr.json`。
REALITY fixture 只等待真实异步目标探测完成，不注入缓存、不关闭证书校验。

TLS 修复后，用同一三轮 coverage/checkptr 命令复跑 VLESS，将输出改为
`/tmp/spec011-vless-reload-final.json`、coverage 改为
`/tmp/spec011-vless-reload-final.cover`，exit 0、27 PASS，无 skip。

### 真实 TLS 热重载：已修复，PASS

修复前执行 `go test -json -race ./transport/internet/tls -run
'^TestCertificateHotReloadConcurrentHandshake$' -count=1 -timeout=30s`，
exit 1；`/tmp/spec011-tls-reload-before.json` 包含实际 DATA RACE 和
“reload mutated shared protobuf configuration”断言失败。
叶证书切换不能只给 slice 加锁：握手在 GetCertificate 返回后仍持有旧指针，
因此 OCSP 字段也必须 copy-on-write；CA reload 同样发布私有不可变 proto 快照。
公共 BuildCertificates 保留签名，返回稳定快照；真实 TLS 配置回调读取更新后的 atomic slot。

```sh
go test -json -race ./transport/internet/tls \
  -run '^Test(CertificateHotReloadConcurrentHandshake|OCSPRefreshPreservesPublishedSnapshot)$' \
  -count=3 -timeout=1m > /tmp/spec011-tls-hot-reload-final.json
go test -json -race ./transport/internet/tls -count=1 -timeout=5m \
  > /tmp/spec011-tls-package-final.json
```

两命令 exit 0。热重载/OCSP 两测试各三轮，无 skip；整包测试包含原有 ECH/pin/CA，
显式公网 TestPublicECHDial 按默认开关 skip，不将其列为公网成功。
测试使用真实文件 reload/真实本地 OCSP 请求和签名响应，不关闭证书校验；
源配置 proto.Clone 对照不变，返回给旧握手的 Certificate/OCSPStaple 不变。
新增 OCSP 专项是在整包命令之后补入，另经三轮 race 验证。

### SS2022 与 Router Publication：PASS，exit 0

```sh
go test -json -race ./testing/scenarios \
  -run '^TestShadowsocks2022(Tcp|UdpAES128|UdpAES256|UdpChacha)$' \
  -count=1 -timeout=5m > /tmp/spec011-ss2022-scenarios.json
go test -json -race ./app/router/command -run '^TestServiceTestRoute$' \
  -count=20 -timeout=5m > /tmp/spec011-router-publish-final.json
```

SS2022 TCP 三算法及 UDP 三算法全过。旧场景由子进程运行，外层 -race 不代表子进程被 race 插桩；
资源失败路径单测本身已在 focused race 中插桩。
Router 20 次现有 gRPC publication 测试通过；取消所有权修复不提前取消排队上下文。
曾误用 `^TestTestRoute$` 命中零测试，**不计为通过证据**。

### Vet 与格式门禁：PASS，exit 0

```sh
go vet ./app/dns/... ./proxy/dns ./transport/internet/splithttp \
  ./proxy/vless/... ./proxy/vmess/encoding ./proxy/shadowsocks_2022 \
  ./testing/networktest ./testing/scenarios ./app/proxyman/outbound ./app/router/command \
  ./transport/internet/tls
bash .github/check-gofmt.sh
git diff --check
go test -json -race ./testing/networktest \
  -run '^TestFormatGateRejectsWithoutModification$' -count=1 \
  > /tmp/spec011-fmt-fresh.json
```

原六条告警（VMess 不可达代码 ×2、SS2022 lostcancel、VLESS unsafe pointer ×2、
Router lostcancel）已关闭，**不是待修**。这里是本 agent 范围 vet，不冒充整库 vet。
稳定 base 扫描初次仅 `app/observatory/burst/healthping.go` 未格式化，已通知父流程协调；
本 agent 未修改该文件，随后完整格式门禁 exit 0。
临时浅 clone 只取 tip 与稳定 fork point，确认旧 1e0c6a30 不存在；
语法有效、已加入临时 index 的负例使 gate 非零，文件哈希保持不变。

## Linux 禁外网验证

镜像准备 `docker pull golang:1.26.1` exit 0；
digest `sha256:cd78d88e00afadbedd272f977d375a6247455f3a4b1178f8ae8bbcb201743a8a`。
模块缓存预置、GOPROXY/GOSUMDB 禁用、network none，仅隔离容器受影响。

初次命令使用共享只读源和 XRAY_LOCATION_ASSET，全程执行结束 exit 1：

```sh
docker run --rm --network none \
  -v /Volumes/Linux/opensource/cuber:/Volumes/Linux/opensource/cuber:ro \
  -v /Users/cube/Dev/go/pkg/mod:/go/pkg/mod:ro \
  -w /Volumes/Linux/opensource/cuber/xray-core-spec \
  -e GOPROXY=off -e GOSUMDB=off -e GOTOOLCHAIN=local \
  -e XRAY_LOCATION_ASSET=/Volumes/Linux/opensource/cuber/xray-config/xray/dat \
  golang:1.26.1 go test -json -p 4 ./... -count=1 -timeout=15m \
  > /tmp/spec011-linux-offline.json
```

失败：app/stats 两个 LRU 断言；AnyTLS FD 13->22；platform 的默认资产路径假定被 env 干扰；
cert.TestGenerate 试图写 ca.key 被只读源拒绝。不是网络下载失败，也不标 PASS。
共享源在其他 agent 修改期间，不将此次当作不可变 release tree 验收。

纠正容器测试环境，复制独立可写快照，DAT 挂到标准位置，不设置资产 env：

```sh
docker run --rm --network none \
  -v /Volumes/Linux/opensource/cuber:/Volumes/Linux/opensource/cuber:ro \
  -v /Users/cube/Dev/go/pkg/mod:/go/pkg/mod:ro \
  -v /Volumes/Linux/opensource/cuber/xray-config/xray/dat:/usr/local/share/xray:ro \
  -e GOPROXY=off -e GOSUMDB=off -e GOTOOLCHAIN=local golang:1.26.1 bash -c '
cp -a /Volumes/Linux/opensource/cuber/xray-core-spec /work
cd /work
exec go test -json -p 4 ./... -count=1 -timeout=15m' \
  > /tmp/spec011-linux-offline-final.json
```

该快照完整执行结束 exit 1，完整失败清单：
TestMultiListenTCPUDP/127.0.0.1/tcp 的 uplink=5/downlink=0（预期各5）；
TestAnyTLSRepeatedConnectionsAndListenerClose 的 FD 13->22。均在本 agent 编辑范围外。
后续归属：父流程确认 006/007 计数时序已修、Linux race 三轮通过并 fixup；
这是所属 agent 的验证，不冒充本 agent 复跑。父流程已定位 AnyTLS 为 echo fixture 的
io.Copy(TCP,TCP) 触发进程级 splice 管道池，**不是未修代理泄漏**。
父流程改为隐藏 fixture ReaderFrom/WriterTo 优化，Linux network-none 三轮
13->12/12/13 PASS；归 spec012，见 [012 证据](evidence-012.md)。
未把失败改成 skip。默认确有显式公网跳过，另有已有 TestSockOptMark、
TestExternalClients、TestVLESSAfterAnyTLSRemoval 跳过，不能声称整库零 skip。

容器尚在运行时，使用同一 network-none 容器执行显式启用的坏代理公网测试：

```sh
docker exec 56147ee21069 env XRAY_TEST_NETWORK=1 \
  XRAY_TEST_PROXY=socks5://127.0.0.1:1 GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
  go test -C /work -json ./app/dns -run '^TestPublicDOHNameServer$' \
  -count=1 -timeout=30s > /tmp/spec011-linux-public-badproxy.json
```

**预期负向 FAIL，实际 exit 1，TestPublicDOHNameServer 命中且未 skip**。
正向 SOCKS 路径由本地 TestProxyRouting 验证；真正公网成功及公网 DoQ 未验证。

TLS 实际修复与父流程相关修复后再次执行同一可写快照整库命令，
输出 `/tmp/spec011-linux-offline-integrated.json`；按用户不再运行慢全库的要求，
本 agent 执行 `docker stop ca3ea290a4eb`（exit 0），原运行会话结束 exit 2。
停止前没有记录 fail 事件，但整库未完成，**不能记为 PASS**，也没有仍待收尾的会话。
该快照包括 TLS 文件热重载测试；随后新增 OCSP 专项另有上述三轮 race 证据。

## 构建与未闭环

从 config cwd 执行：

```sh
XRAY_CORE_SRC=/Volumes/Linux/opensource/cuber/xray-core-spec \
  python3 scripts/xrayctl.py build core
```

exit 1：wrapper 用 `-d "$SRC/.git"` 判定，拒绝 linked worktree 的 .git 文件；
尚未进入 build 或 checkout。没有绕过 clean-tree guard，也没有修改 wrapper。
父流程需在整理提交后执行批准的 Darwin/Linux 构建，当前不能声称 release build 已验证。

未闭环：父流程冻结归并后最终 clean-tree 禁网整库验收；正式 release 双平台构建；
真实公网可达性/UDP ASSOCIATE。TLS 热重载 race 已修复，旧六条 vet 已修复，
AnyTLS fixture 修复归父流程 spec012，不再误列为代理泄漏。
