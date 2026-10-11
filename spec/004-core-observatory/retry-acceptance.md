# Probe Retry Acceptance

Local-only increment authorized on 2026-10-11, implemented across midnight on
2026-10-12. Protocol dialer retries and DNS remain unchanged. Main probe timeout
is distinct from optional connectivity diagnosis. Later user authorization permits
deployment only to Shlii and opt-in HKT checks; its rollout is recorded in
xray-config node notes, not inferred from this local acceptance.

## Reproduction Commands

```bash
go test -race ./common/retry ./app/observatory/burst ./infra/conf -run 'TestProbe|TestRetryProbe|TestObservatoryRetry' -count=10
go test -race ./common/retry ./app/observatory/... ./app/router ./infra/conf
go test -json ./... -count=1 -timeout=15m
go vet ./...
```

After a clean local commit, from xray-config:

```bash
./scripts/xrayctl.py build core
build/xray-darwin version
file build/xray-linux-amd64
```

Use a `.json` fixture enabling retry and HTTP/2 for `run -test`; validate the
existing Shlii config too. Sidecar spec003 owns the real SOCKS wire fixture.

## Test Matrix

| Tests | Required assertion |
|---|---|
| `TestProbePolicyNormalize`, `TestObservatoryRetryConfiguration` | Disabled default, 0..3 cap, duration/default validation, HEAD/GET gate, proto round-trip and group isolation |
| `TestProbeRetryableErrors` | Typed EOF/refused/reset/DNS eligibility; terminal certificate/auth/HTTP/ASN/parser errors and mixed-provider failures |
| `TestProbeAttemptsAndBackoff`, `TestProbeBackoffJitterAndCap` | At most retries+1 calls, no wait after final failure, exponential progression/jitter/cap and duration overflow |
| `TestProbeDeadlineAndCancellation` | One absolute deadline, cancelable waits, insufficient budget and canceled success cannot be published |
| `TestRetryProbeHTTPProtocolsAndReuse` | Actual H1/H2 negotiation, same query, total RTT includes wait, recovered requests reuse transport |
| `TestRetryProbeSingleHealthSampleAndRecovery` | Four logical checks/12 attempts produce four samples; one failure then three distinct successful checks |
| `TestRetryProbeTerminalHTTPAndTLS`, `TestRetryProbeTerminalHTTPTruncatedBody` | Non-204 and invalid certificates do not retry, even if a GET error response has a truncated body |
| `TestRetryProbeTotalBudgetAndStop`, `TestRetryProbeStopDuringBackoff` | No deadline reset, active requests released, scheduler joins canceled waits and publishes no canceled sample |
| `TestRetryProbeHTTP2ConcurrentPoolIsolation` | Warm H2 pool, eight concurrent probes and one injected EOF retain one healthy connection |

## Executed Evidence

- Focused retry/config/helper tests repeated ten times with race passed, including
  H2 concurrent cleanup and the truncated non-204 response boundary.
- Full Observatory/router/config race and `go vet ./...` passed.
- Full `go test -json ./... -count=1 -timeout=15m` completed: 86 packages
  passed, 1652 test/subtest pass events, 46 opt-in skips. One existing scenario,
  `TestVMessGCMMuxUDP`, timed out waiting for a loopback UDP reply; do not call
  that run all green. It does not configure Observatory, and no VMess/Mux code
  or existing protocol retryer changed in this increment.
- Isolated `go test ./testing/scenarios -run '^TestVMessGCMMuxUDP$' -count=5
  -timeout=3m` passed all five repetitions (46.858s).
- Full `go test -json ./testing/scenarios -count=1 -timeout=10m` rerun passed.
  This is a clean package rerun, not a claim that the initial full run was green.
- `xrayctl build core` produced Darwin/arm64 and Linux/amd64 artifacts with
  `vcs.modified=false`; the Darwin version executed successfully. A complete
  retry+H2 `.json` fixture and the Shlii retry config both printed Configuration OK.
  Repeat the official build after the evidence-only commit amendment so the
  final binaries carry the exact final approved revision.

Loopback harnesses do not prove production SOCKS stability. An opaque EOF can
represent a permanent remote fault; bounded retries are opt-in, not a repair for
the provider backend. Application attempt caps do not count HTTP transport's
internal retries or existing lower-level dialer attempts.
