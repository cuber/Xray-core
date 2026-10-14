# UDP Dispatch Retirement Race

Candidate-only verification, 2026-10-15; no production deployment.

Sidecar instance/chain churn exposed concurrent access to `connEntry.closed`:
timer/input retirement writes it while `Dispatcher.getInboundRay` reads it under
a different owner's mutex. The dispatcher mutex does not serialize retirement.

Use an atomic state for publication and compare-and-swap for retirement. Preserve
the existing duplicate-retirement panic and cancellation behavior; do not extend
timeouts, alter UDP dispatch selection, or add a shared global lock.

`TestDispatcherConcurrentEntryRetirement` runs 100 rounds of concurrent retirement
and acquisition. After joining both goroutines, it verifies exactly one cancel
and rejects reuse of the retired connection. Replacement deliberately fails, so
the test cannot silently accept a fresh working connection as the retired one.

Validation:

```sh
go test -race ./transport/internet/udp -count=10 -timeout=3m
```

The same test against the pre-fix plain-bool implementation reports a data race
(`/tmp/core-udp-retirement-before.log`). The fixed full package passes ten race
repetitions in 11.308s (`/tmp/core-udp-retirement-after.log`). The original real
Sidecar reproduction is recorded in `/tmp/sidecar-worker-churn-race.log`.
These temporary logs are supporting evidence; the tracked test is the repeatable
acceptance gate. Full Sidecar churn must also be rerun on the final candidate.
