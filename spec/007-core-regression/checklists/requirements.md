# 回归基础设施与通用修复检查清单

- [x] 明确共享隔离 worktree、文件归属、用户授权与不提交/不部署约束。
- [x] 保留 V-028 至 V-033，区分原有实现、补充测试与本轮实际运行时修复。
- [x] 原六条 vet 已修复并经 scoped vet 复验；Router 不提前取消异步 publication。
- [x] XHTTP 4096 字节立即释放、共享 proto、1000 轮并发与重复 Set 验证。
- [x] DNS 共享 slice、可信/未知根/错误 SAN、失败连接 FD 验证。
- [x] VLESS 同进程 GC/race/checkptr 与覆盖分支证明；错误 pin 真实失败。
- [x] cache Remove barrier 与 SS2022 TCP/UDP 资源、协议对照验证。
- [x] 稳定 fork-base gofmt；fresh clone 无旧对象、tracked 负例、源 SHA256 不变。
- [x] Linux 禁网及显式坏代理公网负例实际执行，FAIL/既有 skip 如实列明。
- [x] TLS 热重载 race 已实际修复；真实文件、并发校验握手、OCSP 快照三轮 race PASS，无 OneTimeLoading 规避。
- [x] 历史监听统计/AnyTLS 整库失败、后续所属 agent 修复及 release wrapper 限制分别记录；AnyTLS 为已修 fixture splice 池问题，不误称代理泄漏。
- [x] 源码及测试已冻结；最终 focused race 54 项通过，停止的 Linux 整合运行不计为通过，最终验证交父流程。
- [x] 禁网整库在最终整合 tree 全部通过；显式跳过不计为公网验证。
- [x] 干净源码双平台构建和父流程单笔提交完成；未部署。

[证据与准确命令](../../001-core-fork-contracts/@@SPECMAP0@@)。
