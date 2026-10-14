# Plan

1. Extend Commander direct listen using absolute path detection and the existing
   Core Unix socket permission syntax; retain TCP and routed fallbacks.
2. Add an exclusive/nonblocking option to the existing Unix listener lifecycle,
   scoped to direct control listeners. Reject non-socket paths and reclaim only
   positively refused stale sockets while holding the lock.
3. Test real gRPC, lifecycle/rebinding, conflicts, stale recovery, permissions,
   ordinary files and TCP compatibility. Run existing listener/route/command
   regressions and the whole Core race suite.
4. Commit before approved Darwin/Linux builds. Sidecar independently pins this
   Core commit and tests all services, runtime keys, hot changes and restart.
5. Config-owned serial canaries remove API inbound/rule only after compatible
   Core binary deployment; rollback requires routed config for old binaries.
   Portal control endpoint metadata is Sidecar-owned, not a fake traffic inbound.
