# Protobuf 生成门禁复验

> Historical evidence: original execution paths, cwd, hashes and results below
> are preserved from before the Core documentation migration. They do not
> identify the current branch; see [path conventions](../README.md#paths-and-historical-evidence).

2026-09-27，`../xray-core-spec` 的 `.github/workflows/test.yml` 要求
全部非 gRPC `*.pb.go` 前四行与 `core/config.pb.go` 一致。
发现四份旧生成文件使用 protoc v7.34.1，基准为 v6.33.5。

使用已有本地 protoc（`libprotoc 33.5`）和 `protoc-gen-go v1.36.11`
从源文件真实重新生成，没有手改生成头：

```sh
/Volumes/Linux/opensource/cuber/xray-config/.cache/anytls/tools/protoc/bin/protoc \
  --go_out=. --go_opt=paths=source_relative \
  app/proxyman/config.proto app/observatory/burst/config.proto \
  app/stats/command/command.proto app/stats/config.proto
```

结果仅四个生成头版本行改变，没有 descriptor/字段/接口变化。
全部非 gRPC pb.go 头比较通过；不改 CI 的版本一致性要求。
归属：proxyman 随 spec007、burst 随 spec008、stats 随 spec010。
最终逐笔提交和 clean release 门禁需再次执行，不能以此替代运行时测试。
