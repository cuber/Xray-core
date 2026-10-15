# Reverse Portal Drain Publication

## Failure

The complete scenario race gate at `8e246952` failed after 800.457s in
`TestReverseProxyLongRunning`. The race-instrumented Core child reported a read
of `PortalWorker.draining` from `StaticMuxPicker.PickAvailable` concurrent with
a write from `PortalWorker.heartbeat`. The parent's child-race gate correctly
failed even though application exchanges were succeeding. Raw evidence:
`/tmp/core-upgrade-final-8e24-scenarios-race.jsonl`.

The picker's mutex protects the worker list and simultaneous selectors; it
does not synchronize the separately scheduled heartbeat. This is a real runtime
race, not a reason to skip reverse tests, remove child instrumentation or widen
timeouts.

## Narrow Repair

Use `atomic.Bool` for the shared drain flag and atomic loads/stores at all four
access sites. Keep the existing heartbeat ownership for control writer/counter,
the picker mutex for its list, the 256-connection drain threshold and the
two-pass selection policy. No new global lock, routing change or pool policy
is introduced. This does not claim an exhaustive reverse subsystem audit.

## Regression Oracle

`app/reverse/portal_drain_test.go` uses actual Mux workers and 257 real dispatch
allocations, not unsafe field mutation. Four picker goroutines race the real
heartbeat. It verifies:

- Before drain, the first least-loaded worker is selected.
- Heartbeat emits exactly one `Control_DRAIN` and closes control output once.
- After publication, a fresh worker is preferred; a repeated drained heartbeat
  neither emits nor closes again.
- If only a draining worker remains, the original second-pass fallback still
  selects it. The test does not silently turn drain into an unavailable worker.

Reader goroutines are joined; fixture clients, pipes and ActivityTimers are
closed. No network listener, fixed port or external service is required.

## Executed Results

Before changing runtime, the new regression failed with the same heartbeat-write
versus picker-read race (exit 1, 0.610s):
`/tmp/core-reverse-drain-negative.log`.

After the atomic change:

```sh
go test -race -count=50 -timeout=2m ./app/reverse
go test -json -race -count=3 -timeout=3m ./testing/scenarios -run '^TestReverseProxy'
```

The package race x50 passed in 1.876s (`/tmp/core-reverse-drain-fixed.log`).
The immediate count=3 scenario run failed in 63.764s because its second
4096-connection case exhausted Darwin's ephemeral ports (`can't assign requested
address`); it reported no data race. That failed run is retained in
`/tmp/core-reverse-scenarios-fixed.jsonl`, not counted as acceptance. No host TCP
setting or test timeout was changed. After TIME_WAIT settled to 139, an isolated
count=1 run passed both complete reverse scenarios in 22.018s:
`/tmp/core-reverse-scenarios-fixed-single.jsonl`. The full scenario rerun is
recorded separately in spec010's final ledger; it is not inferred from scoped
success.
