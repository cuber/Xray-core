# Upstream CI Generation and Formatting Gates

The candidate's standard gofmt check passed, but the upstream CI also runs its
`infra/vformat` gofumpt checker and compares every non-gRPC protobuf generated
header to `core/config.pb.go`. Both checks must remain enabled.

The initial header comparison failed for eight locally regenerated files.
Their compiler stamp was v7.34.1 while the pinned upstream used v6.33.5.
The existing private tool archive supplies `libprotoc 33.5`; the Go generator is
`protoc-gen-go v1.36.11`. Regenerating from the eight actual proto sources changes
only eight compiler-version comment lines. No descriptor, field, interface or
wire number changes. Headers were not manually edited.

```sh
/Volumes/Linux/opensource/cuber/xray-config/.cache/anytls/tools/protoc/bin/protoc \
  --go_out=. --go_opt=paths=source_relative \
  proxy/freedom/config.proto proxy/anytls/config.proto \
  app/router/config.proto app/router/command/command.proto \
  app/stats/config.proto app/proxyman/config.proto \
  app/stats/command/command.proto app/observatory/burst/config.proto
```

Initial upstream formatter output is preserved in
`/tmp/core-upgrade-vformat-check.log`: 42 files need gofumpt formatting.
The local `.github/check-gofmt.sh` now also invokes the same read-only upstream
checker after its existing gofmt check. A build cannot pass the local format
gate and subsequently fail CI merely because it used the weaker formatter.

After the normal package partition passed (2688 test/subtest passes, zero
failures), the upstream formatter applied the 42 file adjustments. Review shows
blank lines, declaration grouping, multiline literals and redundant-parenthesis
removal; no algorithm or assertion changes. The extended local read-only format
gate now passes. A second full generation with the locked compiler produces
byte-identical SHA-256 values for all eight protobuf files, and the CI header
comparison passes. These generated-comment/formatting corrections do not repair
a protocol behavior or waive any runtime failure. Subsequent package race and
scenario gates will compile the formatted source.
