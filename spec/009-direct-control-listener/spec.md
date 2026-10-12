# Direct Control Listener

Status: Implemented locally. Authorized 2026-10-12: Core owns its control UDS directly,
not a dokodemo inbound/traffic route. Consumer/deployment scope is separate.

Extend the existing `api.listen` string without new protobuf fields:

```json
{"api":{"tag":"api","listen":"/run/xray/api.sock,0600","services":["HandlerService","StatsService","RoutingService","ObservatoryService"]}}
```

- Absolute POSIX paths select filesystem Unix stream sockets. Optional octal
  permissions follow the existing Core listener syntax; default is 0600.
  Existing host:port TCP direct listeners and omitted-listen routed API remain.
- No synthetic API inbound, destination port, dispatcher or routing rule is
  needed for direct mode. HandlerService lists only actual traffic inbounds.
- Reuse Core's UDS permission/lock/close infrastructure. The new exclusive path
  acquires its lock nonblocking, never waits indefinitely on a live instance.
  Under the lock reclaim only a filesystem socket whose dial returns
  ECONNREFUSED. Do not unlink an active socket, ordinary file or symlink;
  uncertain errors/timeouts fail closed. Private parent directories remain
  operator/service-owned; do not create them implicitly or weaken permissions.
- Close releases the listener and lock and permits immediate rebinding.
  Permission/bind/start failure must not affect another live server.
- Ordinary listener/dokodemo/route semantics and legacy lock mode are unchanged.
  API socket permissions are the local access boundary, not gRPC authentication.
- No AnyTLS, traffic routing, statistics or health algorithm changes.

See [plan](plan.md), [tests](tests.md), [tasks](tasks.md).
