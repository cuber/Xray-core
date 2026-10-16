# 分叉行为契约索引

001 只负责完整性与重建追溯；各类别维护唯一契约，旧 K 编号保留。

## K1

[连接生命周期](../002-core-connection-lifecycle/spec.md) · [验证](../002-core-connection-lifecycle/tests.md)

## K2

[监听与 UDS](../003-core-listeners-uds/spec.md) · [验证](../003-core-listeners-uds/tests.md)

## K3

[Observatory](../004-core-observatory/spec.md) · [验证](../004-core-observatory/tests.md)

## K4

[路由与调度](../005-core-routing-balancing/spec.md) · [验证](../005-core-routing-balancing/tests.md)

## K5

[用户域名流量](../006-user-domain-traffic/spec.md) · [验证](../006-user-domain-traffic/tests.md)

Core 仅负责采样与 RPC；消费者的聚合、LRU 预算与 snapshot 属于
[Sidecar007](../../../xray-sidecar-rebase-20261014/spec/007-domain-traffic-aggregation/spec.md)。

## K6

[回归基础设施与通用修复](../007-core-regression/spec.md) · [验证](../007-core-regression/tests.md)

SS2022 的长期协议安全与资源契约归 [Core011](../011-ss2022-safety/spec.md)，
上游变更清单和集成发布归 [Core010](../010-upstream-main-upgrade/spec.md)。
