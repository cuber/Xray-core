# Direct Control Listener Requirements Review

This is a contract/test mapping review, not a new execution report.

- [x] POSIX absolute UDS syntax, default/explicit permissions and existing TCP/routed modes defined.
- [x] Direct API does not create synthetic traffic inbounds or routing rules.
- [x] Active socket/foreign listener, regular file and symlink protection specified; uncertain errors fail closed.
- [x] Nonblocking exclusive ownership, stale socket recovery and close/rebind have concrete tests.
- [x] `TestDirectAPIUnixLifecycle`, `OccupiedFailsFast`, `StaleAndInvalidPaths` and `TestDirectAPITCPUnchanged` are mapped in [tests](../tests.md) (short names there share the `TestDirectAPIUnix` prefix).
- [x] Shared listener regressions owned by Core003; Sidecar consumer and deployment tracked independently.
- [x] Dated Darwin results remain dated; Windows skip and cross-build/runtime distinctions documented.

Read [spec](../spec.md), [plan](../plan.md), [tests](../tests.md) and [tasks](../tasks.md)
before changing listener ownership. This checklist does not rerun the historical test suite.
