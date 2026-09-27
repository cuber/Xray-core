# 回归基础设施与通用修复任务

- [x] T001 [US-001] 从 001 提取 FR-001 和 V-028 至 V-033，保留历史追溯。
- [x] T002 [US-001] 对照重建提交检查内容与依赖，执行原有定向测试。
- [x] T003 [US-001] 记录历史 tree 对比；新增授权修复与原无损重建阶段区分。
- [x] T004 [US-001] 为各编号提供准确命令、命中条件、独立断言及清理边界。
- [x] T005 [US-001] 实现并执行 V-028 至 V-033 补测：XHTTP、DNS、VLESS、cache、SS2022、代理与 fmt。
- [x] T006 [US-001] 关闭原六条 vet；复核父流程改动，Router publication 不提前 cancel；scoped vet 通过。
- [x] T007 [US-001] SS2022 TCP 早期失败泄漏先复现再修复；TCP/UDP normal/timeout-only 资源对照通过。
- [x] T008 [US-001] 稳定 fork-base 格式门禁、无旧对象的 clone 负例及源文件哈希验证通过。
- [x] T009 [US-001] VLESS 八情形三轮同进程 GC/race/checkptr/coverage；真实 TLS1.3 载荷与坏 pin 断言通过。
- [x] T010 [US-001] Docker network-none 整库和显式坏代理负向实验执行并记录，不把 FAIL 转成 skip。
- [x] T011 [US-001] 父流程在冻结归并后的 clean tree 完成最终全库验收：Darwin race、Linux 禁网整库均 exit0；历史 002/003 计数与 008 fixture FD 已修复。
- [x] T012 [US-001] 父流程整理单笔提交后，在隔离 clean clone 通过标准 wrapper 完成 Darwin/Linux 构建；已执行 Darwin 版本及 local 配置校验，未部署。
- [x] T013 [US-001] 用户追加 TLS 范围：真实热重载 race 修复、proto 私有状态、atomic 证书/CA 与 OCSP 不可变快照；reload/OCSP 各三轮 race PASS，移除 OneTimeLoading 后 VLESS 三轮 PASS。
- [x] T014 [US-001] 最终 focused race 54 项通过、scoped vet/格式门禁通过，源码及测试冻结交父流程；后续仅更新文档。

状态、文件归属、命令、原始 JSON 路径、退出码及残留见
[本轮证据](../001-core-fork-contracts/evidence-011.md)。
protobuf 真正重生成单独归 003/004/006，见
[独立证据](../001-core-fork-contracts/evidence-protobuf.md)。
