# Verification

`app/commander/listen_test.go`:

- TestDirectAPIUnixLifecycle: default/explicit 0600, real gRPC health, close,
  removed socket/lock and immediate restart at the same path.
- TestDirectAPIUnixOccupiedFailsFast: two real instances, error within one
  second, first instance's RPC remains healthy; no takeover.
- TestDirectAPIUnixStaleAndInvalidPaths: persistent closed socket recovery,
  regular-file preservation, invalid permissions/relative path rejection,
  an active listener outside the Core lock is not replaced.
- TestDirectAPITCPUnchanged: existing TCP binding, RPC, occupied-port refusal
  and listener release/rebind.

Commands (Core repository root):

```sh
go test -race -count=5 -timeout=2m ./app/commander ./transport/internet
go vet ./app/commander ./transport/internet
go test -race ./...
```

Actual Darwin results on 2026-10-12: five-round focused race passes (Commander
1.668s, internet 2.091s); focused vet and full `go test -race ./...` pass.
The latter includes AnyTLS 574.450s and scenarios 288.004s. Sidecar's direct-Core
focused race passes five rounds (6.706s), full race passes (32.805s).
Default opt-in external-client/public-network tests are not enabled by this run.
POSIX UDS fixtures skip on Windows; TCP test remains enabled. Supported release
builds must include Darwin and Linux; cross-building is not runtime Linux proof.
Legacy routed API is covered by the existing proxyman/scenario command suite.

Sibling Sidecar real-Core fixture uses direct `api.listen`, no API inbound/rule,
and checks Handler/Stats/Routing/Observatory/certificates/domain traffic, actual
SS2 data flow, hot user-key change, fail-closed lookup and reconnection after
Core restart. Deployment/build records belong to xray-config; do not infer fleet rollout.
