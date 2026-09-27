# 回归基础设施与通用修复实施计划

1. 保留 001 的稳定 V 编号，读取共享隔离 worktree 的父流程改动和当前 dirty 状态。
2. 复核父流程 VMess/SS2022/VLESS/Router 四处修复；避免 Publish 异步上下文过早取消。
3. 在各所属包补直接回归：缓冲释放、共享 proto/slice、信任与 FD、缓存 barrier、
   SS2022 TCP/UDP 资源。VLESS 用同进程 Core，使 GC/race/checkptr 真正覆盖运行时。
4. 用确定性负例先证实再修复：SS2022 早期 TCP 泄漏；坏代理禁止直连；
   临时 tracked fmt 文件不被门禁修改，clone 内无旧 baseline 对象。
5. 运行 focused race、VLESS 三轮 checkptr/coverage、SS2022 协议场景、Router publication、
   scoped vet、gofmt。原六条 vet 已修复，不作为剩余任务。
6. 用户追加 TLS 专属范围：复现真实热重载 race，私有配置与不可变 atomic 证书发布；
   真实 reload/并发握手/OCSP 快照回归，移除 VLESS 的 OneTimeLoading 规避。
7. Docker network-none 预置 Go/依赖/DAT，整库完成情况、失败与既有 skip 如实记录。
   不修改宿主防火墙，不扩展到其他 agent 的运行时文件。
8. 源码/测试冻结后，父流程接收文件及证据，在干净可追溯源码上执行最终全库及双平台构建；
   本 agent 不 commit、checkout、rebase 或 deploy。

## 已执行与剩余

实现及定向验证见 [@@SPECMAP0@@](../001-core-fork-contracts/@@SPECMAP1@@)。
六条 vet、V-028 至 V-032 定向验收、V-033 代理/格式负例已通过。
历史禁网整库暴露的范围外监听计数与 AnyTLS fixture FD 失败已由所属流程修复；
最后一次整合运行按要求停止，最终全库由父流程执行，不因定向成功宣布全绿。
TLS 热重载 race 已按追加授权真实修复，reload/OCSP 各三轮 race 通过，
VLESS 已去掉 OneTimeLoading 并三轮复验通过。AnyTLS FD 为父流程已修的 spec008 fixture splice 池问题，
不归为未修代理泄漏；最终整合 Linux 快照结果单列在证据中。
linked-worktree 不被现有 release wrapper 接受，正式构建留给父流程，未绕过保护。

历史提交映射及备份见 [001 重建记录](../001-core-fork-contracts/reconstruction.md)。
本轮用户明确授权了窄范围修复，故新增差异单独归属，不再适用旧阶段最终树零差异要求。
protobuf 版本生成由父流程处理，归 003/004/006，不改其 CI 门禁或伪造 header。
