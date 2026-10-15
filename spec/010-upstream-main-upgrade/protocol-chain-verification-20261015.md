# Protocol Chain Verification Handoff

## Initial Compile-Only Evidence

This section records preparation before the main agent granted the exclusive
network window. Subsequent Darwin results and failures are in
[the network ledger](protocol-chain-network-20261015.md); isolated Linux runtime
execution and final-source ownership/boundary verification are in
[the Linux ledger](protocol-chain-linux-20261015.md). The original statuses below
remain dated preparation evidence, not current overall acceptance status.

Candidate HEAD at preparation: `87de2fb885a39d33a5548de7b5e025d055857a32`.
The three new Go files are uncommitted; no runtime change, canonical-develop
change, commit, push, binary release build or deployment was performed.
Concurrent edits to `spec010/tests.md` belong to the main agent and are preserved.

| Gate | Result | Scope |
|---|---|---|
| Ordinary scenario executable compile-only | PASS | `go test -c`, no test executable run |
| Race scenario executable compile-only | PASS | `go test -race -c`, no test executable run |
| Config matrix | PASS | 80 subcases plus parent, JSON-to-Core Build only; no socket opened |
| `go vet ./testing/scenarios` | PASS | Static package vet, not runtime race acceptance |
| Strict upstream format check | PASS | Existing `bash .github/check-gofmt.sh`; read-only whole-tree check |
| All new socket-based tests | NOT RUN | Await main agent's single serial network window |

Parser JSON events are in `/tmp/core-protocol-chain-config-20261015.jsonl` on the
preparation host. The ordinary/race compile-only binaries are in
`/tmp/core-protocol-chain-scenarios.test` and
`/tmp/core-protocol-chain-scenarios-race.test`. These temporary artifacts are not
release artifacts or persistent evidence committed to Git.

Direct invocation of `go run mvdan.cc/gofumpt ...` failed before formatting
because its command-only dependency lacked a go.sum entry. No dependencies were
changed. The repository's pinned `infra/vformat/main.go` path, used by its strict
format check, succeeded. This tool invocation failure is not a network-test
failure or a reason to mutate go.mod/go.sum.

## Reproduce Non-Network Checks

Run in the isolated Core candidate, without setting the network opt-in:

```sh
go test -c ./testing/scenarios -o /tmp/core-protocol-chain-scenarios.test
go test -race -c ./testing/scenarios -o /tmp/core-protocol-chain-scenarios-race.test
go test ./testing/scenarios -run '^TestProtocolChainConfigMatrix$' -count=1 -timeout=2m -json > /tmp/core-protocol-chain-config-20261015.jsonl
go vet ./testing/scenarios
bash .github/check-gofmt.sh
```

## Serial Network Window

The main agent was notified after initial parser and compile-only preparation.
Do not run alongside AnyTLS diagnostic loops, Sidecar chain/resource stress,
Mihomo/sing-box stress, full scenarios or other socket-heavy acceptance. The
previous 16,319 TIME_WAIT incident is not to be repeated. Use distinct new log
filenames for reruns; retain failed logs. No fixture retries business requests.

Run the following commands one at a time. `-p=1` and `-parallel=1` limit this
invocation, not other concurrently running agents/processes. The main agent must
serialize them externally. Every successful network gate requires exit zero,
nonzero named subcase execution, and no skips/fail events in its JSON log.

```sh
XRAY_PROTOCOL_CHAIN_NETWORK=1 go test -race ./testing/scenarios -run '^TestProtocolChainTLSVerification$' -count=1 -p=1 -parallel=1 -timeout=3m -json > /tmp/core-protocol-chain-tls-race.jsonl
XRAY_PROTOCOL_CHAIN_NETWORK=1 go test -race ./testing/scenarios -run '^TestProtocolChainTwoHop$' -count=1 -p=1 -parallel=1 -timeout=10m -json > /tmp/core-protocol-chain-two-hop-race.jsonl
XRAY_PROTOCOL_CHAIN_NETWORK=1 go test -race ./testing/scenarios -run '^TestProtocolChainThreeHop$' -count=1 -p=1 -parallel=1 -timeout=15m -json > /tmp/core-protocol-chain-three-hop-race.jsonl
XRAY_PROTOCOL_CHAIN_NETWORK=1 go test -race ./testing/scenarios -run '^TestProtocolChainVLESSMux$' -count=1 -p=1 -parallel=1 -timeout=5m -json > /tmp/core-protocol-chain-mux-race.jsonl
XRAY_PROTOCOL_CHAIN_NETWORK=1 go test -race ./testing/scenarios -run '^TestProtocolChainOutage$' -count=1 -p=1 -parallel=1 -timeout=90m -json > /tmp/core-protocol-chain-outage-race.jsonl
XRAY_PROTOCOL_CHAIN_NETWORK=1 go test -race ./testing/scenarios -run '^TestProtocolChain(Cancellation|ActiveClose|ResourceRounds)$' -count=1 -p=1 -parallel=1 -timeout=15m -json > /tmp/core-protocol-chain-retirement-race.jsonl
```

Repeat without `-race` for ordinary runtime acceptance, with distinct logs. Do
not assume a future race pass establishes ordinary or Linux runtime acceptance.
The outage timeout is a theoretical 32-case x100s held-stream bound plus startup,
failed requests and safety margin; it is not a production retry/timeout change.
Stop at a failure for diagnosis; do not launch the remaining stress groups in
parallel to shorten the window.

Expected leaf subcases: TLS 9, double-hop 16, triple-hop 64, Mux 4, outage 32,
cancellation 16, active close 16, resource rounds 12 plus their explicit warmup.
Go parent PASS events are not extra protocol combinations. A guard SKIP or
platform-resource SKIP means the corresponding gate was not executed.

Complete test scope, parameters, receipt/stat/resource oracles and intentionally
uncovered fault/Mux phases are in [the matrix plan](protocol-chain-plan.md).
