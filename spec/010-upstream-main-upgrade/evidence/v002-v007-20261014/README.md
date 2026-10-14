# V-002 QUIC and V-007 Buffered Writer

Executed 2026-10-14, Go 1.27.0, Darwin/arm64, in the isolated candidate
`xray-core-rebase-20261014`. Observed HEAD was
`682406c3d9924125fcbcecccdd742553b2733a73`; other agents were editing separate
files, so this is evidence for the recorded sources, not a clean release build.
No commits, deployment, canonical changes, runtime rewrites, or readv edits.

## Source Review

- QUIC already contains the sample-length guard from upstream
  `8f15190c230fe6975c9b18e31316c6bc494f3863`.
- BufferedWriter already continues after partial `ErrBufferFull`, upstream
  `6ce8dc53e79842af71f0b4a360cb65d9eaa1e8f7`.
- `git diff --exit-code 0888beb3 -- common/protocol/quic/sniff.go common/buf/writer.go`
  exited 0. Both runtime files remain identical to the selected upstream base.
- Source SHA-256: sniff.go
  `19d1a9af76b96b98348d0c77ac6f256e4102a1ba940e9505c592d56504cf9e04`;
  writer.go `c4bbc5d026ade18877a89d9c7dcc7f28e7ce9a025df3b847686388e702c39b5c`.

## Permanent Tests and Oracles

[QUIC tests](../../../../common/protocol/quic/sniff_regression_test.go):
192 malformed Initials cover sample lengths 0..15, packet-number lengths 1..4,
and draft29/v1/v2. Every malformed sample must return an error and nil header.
Twelve valid packets use standard-library QUIC TLS ClientHello generation and
independent AES-GCM/header protection, asserting `owned.example.test` and `quic`.
Empty input, declared packet lengths 0..4, and CRYPTO offsets 32767, 65535,
65536 and 2^62-1 are rejected. Existing capture, fragmentation, padding and
coalescing tests remain enabled. No external network is used.

[Writer tests](../../../../common/buf/writer_regression_test.go):
initial occupancy 0, 1, half capacity, capacity-1; writes of zero, remaining-1,
remaining, remaining+1 and 2*capacity+17. Independent patterned bytes verify
the complete output and counts through both io.Writer and consuming/releasing
MultiBuffer sinks. Seven-byte short writes with nil error are retried to exact
completion. ErrShortWrite/ErrClosedPipe at delivered counts 0, 1, capacity-1,
capacity verify exact prefixes and returned accepted counts. Explicit flush,
SetBuffered(false), and direct unbuffered short writes are covered.

On a terminal buffered flush error, n counts bytes accepted into the buffer,
not bytes delivered downstream. The implementation discards the failed buffer;
these tests do not claim automatic recovery or lossless retry after that error.
They assert no further implicit retry/duplicate delivery on subsequent Flush.

## Executed Commands

From candidate root, all commands below exited 0:

```sh
go test -json -race -count=1 -timeout=5m ./common/buf ./common/protocol/quic
go test -json -race -count=10 -timeout=5m ./common/buf ./common/protocol/quic -run '^Test(BufferedWriter|QUIC)'
go test ./common/protocol/quic -run '^$' -fuzz '^FuzzSniffQUICBounded$' -fuzztime=60s -parallel=4
go vet ./common/buf ./common/protocol/quic
gofmt -l common/protocol/quic/sniff_regression_test.go common/buf/writer_regression_test.go
```

- [Full race JSON](full-race.json.gz): 608 test/subtest passes, 0 failures,
  0 skips; buf 1.721s, QUIC 2.003s. Includes all 40 persisted fuzz entries.
- [Repeated race JSON](repeated-race.json.gz): 2740 test/subtest passes,
  0 failures, 0 skips; buf 1.552s, QUIC 1.817s.
- [Fuzz log](core-v002-fuzz60.log): 60.300s, 3,565,230 executions,
  205 initial seeds and 40 newly interesting entries, no crash.
  All 40 discovered inputs are retained in
  [testdata/fuzz/FuzzSniffQUICBounded](../../../../common/protocol/quic/testdata/fuzz/FuzzSniffQUICBounded).
  Input length is capped at 65,535 bytes; each input is cloned because the
  sniffer decrypts in place. This is not a per-input allocation benchmark.
- Scoped formatting and diff checks passed. No test exclusions were used.

## Negative Baseline

Exact runtime files from `1fd51943d81ed297636581724f6f2ebeaa50f62a` were supplied
through a Go `-overlay` mapping; candidate source files were never replaced:

```text
common/protocol/quic/sniff.go       -> git show 1fd51943:common/protocol/quic/sniff.go
common/protocol/quic/qtls_go118.go   -> git show 1fd51943:common/protocol/quic/qtls_go118.go
common/buf/writer.go                -> git show 1fd51943:common/buf/writer.go
```

The overlay JSON has `{"Replace":{"<absolute candidate path>":"<temporary source path>"}}`.
With those files materialized under `/tmp/core-v002-v007-baseline`, executed:

```sh
go test -overlay /tmp/core-v002-v007-baseline/overlay.json ./common/protocol/quic -run '^TestQUICTruncatedProtectionSamples/version=1/pn=1/sample=0$' -count=1
go test -overlay /tmp/core-v002-v007-baseline/overlay.json ./common/buf -run '^TestBufferedWriterPartialCapacityBoundaries$' -count=1
```

Both exited 1, intentionally:

- [QUIC baseline](core-v002-baseline.log): slice-bounds panic at old sniff.go:149
  for v1/pn1/sample0 (21-byte datagram).
- [Writer baseline](core-v007-baseline.log): 16 failing overflow subcases;
  e.g. occupancy=1/write=8192 accepts 8191 then incorrectly returns ErrBufferFull.

This isolates old implementations under candidate dependencies and the current
toolchain; it is not a historical full-binary build or old-tree acceptance run.
The same tests pass without the overlay.

## Integrity and Remaining Scope

Verify logs, corpus, and test sources from candidate root with
`shasum -a 256 -c spec/010-upstream-main-upgrade/evidence/v002-v007-20261014/SHA256SUMS`.
Compressed JSON preserves complete test events, not only summaries.

Linux Docker validation is available but not executed in this worker increment.
No new end-to-end SS routeOnly sniffing or user/inbound/outbound transfer-counter
test was added; those broader spec010 integration gates remain with main.
The shared buf package run includes existing readv tests, but does not claim
ownership or an independent review of main's readv changes.
