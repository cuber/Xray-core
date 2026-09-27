# 回归基础设施与通用修复

- Feature ID: 011-core-regression
- Status: Implemented（定向、全库离线回归及隔离双平台构建完成；未部署）
- Authorized: 2026-09-27，用户授权在共享隔离 worktree 补齐 V-028 至 V-033、窄范围运行时修复及格式 baseline 修复；主流程统一提交，不部署。
- Source: 原 Core e7a21974，相对 b4f08981；历史清单见 [005](../005-core-fork-contracts/inventory.md)。
- Scope: DNS/XHTTP/VLESS、SS2022 资源、VMess vet、Router publication 取消所有权、缓存并发、TLS 证书热重载、离线 fixtures、代理与格式门禁。
- 原 005 FR-010 至 FR-012 保留稳定追溯；实际差异、命令和退出码见 [本轮证据](../005-core-fork-contracts/evidence-011.md)。

## US-001 — 分类维护与独立验证（P1）

Given 受控测试输入与可追溯源码，When 执行补测及必要修复，
Then 真实断言字节、关闭、信任、并发与失败语义，分开记录定向成功和整库残留；
不以文件存在、零测试命中、关闭校验或新增 skip 代替验收。

## FR-001 — 行为契约

- DNS fixture 信任根只影响私有测试构造器；DoH/DoQ 错误 SAN 和未知根必须拒绝。
  DoH 失败握手回收连接，DNS outbound 归一化复制共享 slice，不修改缓存。
- XHTTP WaitReadCloser 的等待由 Set/Close 唤醒，重复/迟到 Set 释放资源，
  重复 Close 不重复关闭。uploadWriter 下游立即释放缓冲时仍返回原始交付长度。
- RangeConfig/XmuxConfig 指针化不得复制 proto 锁、改变默认值或修改共享输入。
- VLESS unsafe.Pointer + unsafe.Add 保持可达性；同进程 GC/race/checkptr 证明
  TLS、uTLS、REALITY 和加密 RAW 分支。XOR/额外 TLS 层不能错误穿透，错误 pin 不能成功。
- TLS 热重载保留真实文件与 OCSP 刷新；watcher 不修改共享 proto，
  新叶证书/CA 通过 atomic 快照发布，OCSP copy-on-write，不改写进行中握手持有的对象。
  无效文件保留最后有效叶证书，SNI 拒绝和客户端证书校验保持生效。
- outbound cache 在 Remove 完成后的新查询不得返回旧 tag；并发在途快照允许旧结果。
  SS2022 TCP/UDP 与 timeout-only 路径均需资源关闭，早期 TCP 失败不能泄漏 Dial 结果。
- Router TestRoute 的 stats.Publish 异步保留 context；RPC 返回不能取消排队消息。
  publication 保留四秒超时，取消由该期限拥有。不得用立即 defer cancel 消除 vet。
- VMess 无受支持 response command，拒绝未知命令且不写输出，保留短输入与认证失败错误。
- 公网 TestPublic* 仅显式开启；失败代理不直连，开启但不可达应 FAIL。
  TCP-only SOCKS 不证明 DoQ 可用；OS resolver 不走 SOCKS。
- gofmt 门禁只读，以稳定 fork point
  b4f08981becb71eaa995fa98ed2098ade92566bb 覆盖本 fork 改动。
  fresh clone 不依赖重写前中间 commit，负例必须是 tracked 文件且哈希不变。

## SC-001 — 验收状态

V-028 至 V-032 定向 race/checkptr/资源及协议测试通过；原六条 vet 已关闭。
V-033 的代理、fresh-clone 格式负例与真实禁网实验已实现并执行。历史快照的
006/007 计数与 012 fixture FD 失败已由所属流程修复，最后一次整合运行按要求中止，
最终全库验收交给父流程，不将未完成运行记为通过。TLS 热重载 race 已实际修复；
真实 reload/OCSP 三轮 race 和移除 OneTimeLoading 后的 VLESS 矩阵通过。
正式 clean-tree Darwin/Linux 构建仍由父流程整理提交后执行。

历史无损重建与本轮用户授权新增修复是两个阶段；不再要求新增修复后的树等于旧 HEAD。
新增文件由父流程归入 spec011 单笔提交；protobuf 修复另归 007/008/010，见
[protobuf 证据](../005-core-fork-contracts/evidence-protobuf.md)。不修改生产状态或远端。

## 导航

[技术计划](plan.md) · [任务](tasks.md) · [测试](tests.md) · [检查清单](checklists/requirements.md)
