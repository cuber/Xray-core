# Control API Audit (2026-10-14)

Compared fork `1fd51943` with the integration candidate. Spec005 owns routing
control, not HandlerService inventory. No runtime API port was necessary.

- `app/proxyman/command/command.proto`, `command.pb.go` and
  `command_grpc.pb.go` are identical to the fork. `command.go` differs only in
  a local variable declaration (`result :=` versus `var result =`).
- ListInbounds (including tag-only mode), GetInboundUsers (all/by email),
  GetInboundUsersCount and ListOutbounds remain available. Receiver/proxy and
  sender/proxy TypedMessages remain serialized in the existing response fields.
- TLS Certificate fields remain bytes certificate=1/key=2 and paths
  certificate_path=5/key_path=6. The JSON builder moved upstream from
  `infra/conf/transport_internet.go` to `infra/conf/transport_security.go`;
  TLSCertConfig.Build still loads bytes and retains both paths.
- Upstream TLS policy changes are retained: no resurrection of allow_insecure
  or ech_force_query; use_system_ca=23 remains. No TLS source/proto edits made.
- SenderConfig field 3 remains reserved. Its removed ProxySettings is not an
  inventory API omission and must not be restored to satisfy old consumers.
  Consumers must use the new chain configuration deliberately.
- Sidecar call sites inspected: xray_debug.go, usersync_accounts.go,
  egress_core_proxy.go and certificate_collector.go. Certificate collection uses
  ListInbounds -> ReceiverConfig -> StreamConfig -> TLS Config -> certificate
  bytes/path, not a separate missing certificate RPC.

## Executed Evidence

`go test -race -count=3 -timeout=5m ./app/proxyman/command` passed (1.548s).
New `TestControlInventoryRealGRPCWire` uses a real loopback TCP gRPC server,
production handlerServer and generated client, with controlled manager/handler
fixtures. It asserts all/by-email user account/email/level, count, full and
tag-only inventory, nested TLS runtime byte/path round-trip, sender dialerProxy,
raw certificate wire field numbers, and reserved Sender field 3.

This is wire/API evidence, not a real Sidecar process test, live TLS handshake,
certificate reload test or full Core runtime inventory test. Certificate fixture
bytes deliberately test opaque serialization, not PEM validation. Existing TLS
reload and Sidecar interoperability suites remain separate integration gates.
No commits, deployment, canonical checkout edits or shared TLS edits.
