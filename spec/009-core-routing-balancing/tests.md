# 路由与调度验证矩阵

V-015 至 V-018 的核心 harness 已实现并通过 race（2026-09-27）。
代码在 /Volumes/Linux/opensource/cuber/xray-core-spec，以下 core 路径相对此 worktree。
原有 weighted/leastLoad/condition/command/conf 测试随完整目标包运行。
实际 Sidecar/TUI 解码仍是跨组件剩余项，不能由 core wire 兼容替代。
详见 [执行证据](../005-core-fork-contracts/evidence-008-009.md)。

| ID / 原 FR | 已实现输入与断言 | 新测试入口 | 状态 |
|---|---|---|---|
| V-015 / 006 | 1.5:1:0.5；串行与 12x50 并发，600 次均为 300:200:100 | TestContractWeighted600 | 核心 PASS |
| V-016 / 006 | RTT/失败率/样本边界、fallback、重复/移除/恢复、字面 first-match、非正/非有限权重 | TestContractWeightedThresholdMatrix、TestContractWeightedFallbackAndRecovery、TestContractWeightedLiteralFirstMatch、TestContractWeightedJSONLiteralWeights | 核心 PASS |
| V-017 / 006,007 | 精确/domain/regexp 用户边界，leastLoad tolerance 前/等/后及 0 | TestContractUserMatchingEdges、TestContractLeastLoadToleranceBoundary | 核心 PASS |
| V-018 / 007,012 | 真实 TCP gRPC 静态/追加/删除/替换，深复制、并发和旧字段 wire | TestContractRealGRPCRuleLifecycle、TestContractRealGRPCConcurrentRules、TestContractRealGRPCConcurrentReplacement、TestContractListRuleLegacyWireFields | 核心 PASS；实际 Sidecar/TUI 未验证 |

## 实际执行

以下两条在 xray-core-spec 执行，均 exit 0；四包完整回归 277 个测试/子测试通过事件，
新增测试十次重复 1170 个通过事件，无失败或跳过。数字为 008/009 合计；
009 新增 11 个顶层测试。准确包耗时、失败复现、日志哈希见证据。

```sh
go test -json -race -count=1 -timeout=5m ./app/observatory/burst ./app/router ./app/router/command ./infra/conf > /tmp/core-spec-008-009-race.json
go test -json -race -count=10 -timeout=5m ./app/observatory/burst ./app/router ./app/router/command ./infra/conf -run '^TestContract' > /tmp/core-spec-008-009-repeat.json
```

以下逐项命令是对已通过 suite 的筛选复验，不声称分别另跑过每条命令。
按 [公共规程](../005-core-fork-contracts/test-procedure.md) 保存 JSON、退出码；
零测试命中不算通过。

## V-015：稳定权重比例

```sh
go test -json -race -count=1 -timeout=5m ./app/router -run '^Test(ContractWeighted600|WeightedLeastPingSmoothWeightedRoundRobin|WeightedLeastPingConcurrentSelectionPreservesRatio)$'
```

每个场景重建 strategy；fake Observatory 给 a/b/c 相同健康/RTT，权重 1.5/1/0.5。
通过公开 PickOutbound 串行 600 次，再用另一实例以 12 goroutine 各 50 次选择。
独立 mutex 计数，全部 join 后精确断言 a=300、b=200、c=100，总数 600；
日志记录权重和频数。不以实现自身的计算结果充当 oracle，也不检查字节比例。

## V-016：健康池、fallback 与权重

```sh
go test -json -race -count=1 -timeout=5m ./app/router ./infra/conf -run '^Test(ContractWeightedThresholdMatrix|ContractWeightedFallbackAndRecovery|ContractWeightedLiteralFirstMatch|ContractWeightedJSONLiteralWeights|WeightedLeastPing.*)$'
```

固定健康基准 a，逐行只改变 b 的资格；先断言完整池，再独立统计 60 次选择：
maxRTT=250ms 对 249/250/251；best=100ms、delta=50ms 对 149/150/151；
tolerance=.2 对失败 3/4/5 of 20 以及 tolerance=0；minSamples=5 对 4/5/6。
maxRTT 等值排除，相对等值保留，失败率等值保留。Burst Average 故意与 Delay 不同，
确认 Burst 使用 Average；无 Burst 则走 Delay，并覆盖无 Burst 时样本不足。
存在健康节点时排除 dead/无观测项。

缺失、错误、错误类型、全 dead 观测时回全部有限正权重候选；断言去重、60 次分布、
节点移除后的累计状态删除、恢复分布和空池返回空。
JSON 输入验证 -1/0 拒绝，0.5/1/1.5 原值保留。原始 protobuf 运行时测试
0、负、NaN、正负 Inf 排除，普通/正则首匹配、无效正则继续和默认权重均有断言；
带数值后缀 tag 不会复活被排除权重。

实际修复：原共享 WeightManager 把非正权重自动转换为数值/默认值，导致错误回池。
weightedLeastPing 改为字面 first-match 权重并保留加锁缓存，非有限值也不入池；
首匹配权重无效不穿透到后续项。共享 WeightManager 与 leastLoad 自动权重未改。

## V-017：用户匹配与 leastLoad

```sh
go test -json -race -count=1 -timeout=5m ./app/router -run '^Test(ContractUserMatchingEdges|ContractLeastLoadToleranceBoundary|RoutingRule|LeastLoadToleranceFiltersFailureRate)$'
```

逐条构建 matcher，精确 admin@la.att 大小写敏感；domain:la.att 匹配 u@la.att、
u@LA.ATT、u@gmail.com@la.att、@la.att，不匹配 u@x.la.att、无 @、尾部空后缀。
域规则大小写不敏感；空 domain: 规则被忽略。非法 regexp:[ 忽略而非拒绝配置，
空 regexp: 仍是精确字面值；有效正则的大小写按原语义。
每行记录规则、输入和 bool。leastLoad 单独验证失败 3/4/5 of 20 在 .2 和 0 门槛下的结果。
这些是补测与现有行为澄清，不包含 user matcher 或 leastLoad 运行时修改。

## V-018：真实路由控制面 RPC

```sh
go test -json -race -count=1 -timeout=5m ./app/router/command -run '^Test(ContractRealGRPCRuleLifecycle|ContractRealGRPCConcurrentRules|ContractRealGRPCConcurrentReplacement|ContractListRuleLegacyWireFields|ServiceListRuleReturnsDetailedRules.*)$'
```

真实 loopback TCP gRPC server/client（不是只调 service）验证两条静态规则的
user/domain/IP/network/inbound/target 与顺序；追加第三条、移除第二条后逐次比对。
重复 tag 追加拒绝且列表不变；shouldAppend=false 替换整表，不是按 tag 覆盖单条。
不存在 tag 的 Remove 幂等成功，空 tag 拒绝，删除最后一条得到空列表。

修改 RPC 响应及 direct service 响应的嵌套 user/domain/IP 字节/target 后重查，
确认不是只靠网络序列化实现的伪深复制验证。旧 wire 解码器读取字段 1/2，
跳过详细规则字段 3，验证 tag/ruleTag 兼容。

并发一：4 worker 各 100 轮 Add/List/修改响应/Remove/List，检查本人新增规则、
删除不残留、静态顺序和快照一致性。并发二：100 次整表顺序切换，
4 reader 各 100 次 List，只能读到完整旧表或新表，不能出现混合/部分表。
全部 join，关闭连接/server/router；不触碰 011 拥有的 command.go。

## 剩余验证边界

实际 Sidecar/TUI 客户端解码未执行，仍需父进程协调；core wire 测试不代替它。
RPC 控制面并发 PASS 不外推为所有 PickRoute 数据面并发路径已验证。
全仓测试与 Darwin/Linux 发布构建未声明 PASS，父进程统一隔离 clone wrapper 构建，
本 worker 不再尝试。新增权重修复导致当前 tree 有意区别于历史重建基线；
审阅归并未完成，不代表已提交或已部署。
