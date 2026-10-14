# Activity Timer Ownership

The AnyTLS v4 normal soak passed 601.09s; the matching race soak did not finish
teardown. Its stack identifies `ActivityTimer.SetTimeout -> Periodic.Start ->
ActivityTimer.check -> finish -> mu.Lock`: SetTimeout already holds that mutex.
The business copy goroutine is stuck, so the AnyTLS service and Core Close wait
forever. This is a shared timer defect, not evidence for widening probe timeouts.
Log: `/tmp/xray-anytls-fin-v4-race-20261015.jsonl` (terminated after the failed
bounded teardown; not a passing package).

Periodic.Start executes immediately. An old timer callback can consume the
shared activity token after the replacement writes it and before Start reads
it. The new task then tries to finish while SetTimeout holds the timer lock.
An old callback can also expire the replacement directly.

The fix keeps the existing Periodic implementation and coalesced activity
semantics. Its synchronous initial call only arms the task. Subsequent ticks
acquire the timer mutex, verify the current task identity and only then consume
activity or mark the timer consumed. Retired callbacks cannot affect a new task.
Timeout callbacks run outside the mutex; consumed publication guarantees one
callback. No detached goroutine, retry, timeout relaxation or global timer
cleanup is added.

`TestActivityTimerStaleCheckDoesNotExpireReplacement` captures an actual old
Periodic callback, replaces the timeout and then executes the retired callback.
Before the fix it fails immediately with `retired check expired the replacement
timeout`. After the fix it preserves the replacement and explicit final expiry
invokes the callback once. The virtual-time activity test checks interval reset,
activity extension, idle expiry and terminal non-restart.

Executed on Darwin/arm64, Go 1.27.0:

- New tests under race, count=100: PASS, 1.490s.
- Entire common/signal and common/task race tests: PASS, 13.660s / 13.748s.

Final AnyTLS teardown/soak and broader protocol regression must be repeated
against this change and the separate accepted-connection retirement repair.
This scoped result does not close that integration gate.
