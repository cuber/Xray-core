# Multiple inbound listen addresses

An inbound accepts either the existing single-address string or an array of
explicit IP addresses:

```json
{
  "tag": "ss2-in",
  "listen": ["10.77.55.1", "43.248.8.104"],
  "port": 2053,
  "protocol": "shadowsocks"
}
```

The example shows listener fields only; supply the protocol's normal settings.
All addresses share the same protocol instance, users, inbound tag and counters.
Each address is bound on every configured port and supported network. If a
listener fails to start, all listeners started by that handler are closed.

Existing strings, including wildcard IPs, localhost and Unix sockets, retain
their existing behavior. Omitting listen still binds the default wildcard.
Arrays require ports and unique, non-wildcard IP addresses. Empty arrays,
null entries, domain names, Unix sockets and TUN inbounds are rejected.
Single-address arrays are supported.

The protobuf receiver keeps the legacy listen field (2) and adds
listen_addresses (7). The fields are mutually exclusive. Multiple-address
configs require a core supporting the new field; do not send them to older
cores, which do not interpret it.
