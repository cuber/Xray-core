# Outbound Local Acceptance Index

Date: 2026-09-28. Scope: local implementation, tests and Darwin/Linux builds.
No push, deployment, production pin update or local service restart is included.
This index reconciles requirements; chronological failures and their resolutions
remain in [the audit](outbound-current-audit.md) and
[implementation evidence](outbound-implementation.md).

Status: local implementation, functional acceptance, final whole-suite regression
and approved dual-platform build passed. All names below refer to real tests.

## Requirement Mapping

Names in the AnyTLS column omit the common `TestAnyTLSOutbound` prefix except
where a full `TestClient...` or `TestAnyTLSExternal...` name is given.

| Contract | Primary executable evidence |
|---|---|
| OT-01 | NativeValidationMatrix, JSONIdleIntervalBoundaries, ControlPlaneValidation; infra/conf AnyTLS tables |
| OT-02 | RoutingStatsAndChains; TestAnyTLSExternalServers, TestAnyTLSExternalClientsThroughNativeOutbound, TestAnyTLSExternalControlPairs; strict external/chains runner |
| OT-03 | TestClientSequentialReuseAndContextIsolation, TestClientConcurrentStreams, TestClientPeerIdleExpiryFreshRequest; ChainLayerCounts, TCPRelayLayerCounts, Hy2RelayLayerCounts; external physical-reuse oracle |
| OT-04 | CanceledUserThenReusedPool, ChainedReplacementDrains, ScheduledBalancerUDSIsolation |
| OT-05 | engine TestClientIdleEvictionAndMinimum, TestClientDefaultIdleTimer, reserved/control-frame pool tests; strict idle report |
| OT-06 | TestClientUDPPacketBoundaries; UDPInvalidSizeAndNoDirectBypass, UDPResponseBoundaries, UDPUserDomainAccounting, AddressIPv6; strict Linux address report |
| OT-07 | FailureRecovery, RefusedDestinationFreshRecovery, ChainFaultReceipts, ChainAdmissionFaultFreshRecovery; TestClientMalformedResponseDoesNotReplay, TestClientDeliveredWriteCancellationAndFreshRecovery |
| OT-08 | ControlPlaneValidation, RetirementDuringTLS, RetirementDuringAuthWrite, RetirementAdmissionBarriers, RetirementFINReturnAndClose, ShutdownOwnsDrainingNativePool; independent core rejected-construction tests |
| OT-09 | RoutingStatsAndChains, UDPUserDomainAccounting, UDPReuseExactWire, ChainLayerCounts |
| OT-10 | StatisticsSwitches; sibling Sidecar TestAnyTLSOutboundLiveControlHTTPDisplay and TestAnyTLSOutboundLiveDomainAggregation with candidate Core modfile |
| OT-11 | B/C mapping below, plus existing router/observatory regression suites |
| OT-12 | NativeFaultResourceRounds; all retirement barriers; TestClientLateDialCannotPublishAfterRetirement; engine wire/read/write/FIN tests; strict ten-minute soak report |
| OT-13 | Whole-Core race report, strict external clients/servers, independent shared-code regressions in outbound-impact.md |
| OT-14 | Read-only gofmt gate, go vet, approved xrayctl build core, Darwin version/config check and both artifact VCS identities |

## OB And Chain Mapping

| Contract | Primary executable evidence |
|---|---|
| B-01/02 | ObservatoryUDS: status/method/redirect/query/group cases |
| B-03 | ProbeLoopbackTrap in isolated Linux namespace; Sidecar controlled-provider egress integration |
| B-04 | OBResourceCounts: separate HTTP/logical/physical counts |
| B-05 | HealthThroughControlAPI: windows 1/2/20, startup/recovery/staleness |
| B-06 | ProbeTimeoutCancelsDial/TLS/AuthWrite, ProbeTimeoutPreservesBusiness, ProbeAdmissionPreservesSharedPoolBusiness |
| B-07 | ObserverStopRestart, ConnectivitySuppression, ScheduledBalancerUDSIsolation |
| B-08 | ObservedBalancers and ScheduledBalancerUDSIsolation |
| C-01 | RoutingStatsAndChains, SeparateProcesses, strict external chains, no-bypass packet tests |
| C-02 | AddressDNSLocality, AddressIPv6, AddressStaticBinding, ChainedSocketBinding; TLS/SNI failure matrix |
| C-03 | ChainLayerCounts, TCPRelayLayerCounts, Hy2RelayLayerCounts; distinct physical/logical/backend counts |
| C-04 | ChainedReplacementDrains, native retirement overlap tests |
| C-05 | ChainFaultReceipts, SeparateProcesses, ChainAdmissionFaultFreshRecovery, ChainPrecedence, validation/cycle checks |
| C-06 | UDPInvalidSizeAndNoDirectBypass, UDPResponseBoundaries, UDPUserDomainAccounting; exact TCP chain receipts |
| C-07 | ChainProbeTimeoutIsolation, ScheduledBalancerUDSIsolation |

## Evidence Boundaries

Final follow-up strict reports (candidate worktree atop 23c1449c):
`../xray-config/.cache/anytls/strict-final-local/report.json` passes external and
chains groups; `../xray-config/.cache/anytls/strict-final-processes/report.json`
passes the separate-process group (194.427s), including child cleanup. These
reports include the redirect cancellation repair, not just the earlier runtime.

Final Darwin whole-Core command `go test -race ./... -count=1 -timeout=15m`
exited 0, including every new test and the redirect repair: AnyTLS 513.683s,
engine 8.847s, scenarios 283.811s, DNS 23.920s and transport/internet 2.454s.
Log: `../xray-config/.cache/anytls/full-race-final-local-darwin.log`.
Source/test files were frozen during this run; only acceptance documentation
changed. Read-only formatting, diff checks and `go vet ./...` also passed.

The approved `../xray-config/scripts/xrayctl.py build core` built Darwin/arm64
and Linux/amd64 at clean `e5fae1c7`; the Darwin candidate executed `version` and
`run -test` with `.cache/anytls/outbound-validation.json` (`Configuration OK`).
VCS metadata matches the commit and reports `vcs.modified=false`. This final
documentation amendment is rebuilt through the same wrapper before delivery;
the resulting exact revision and both SHA-256 values are retained in
`../xray-config/.cache/anytls/final-local-build.log`. No source/test changes occur
between the passed whole-suite run and that final documentation-only amendment.

- Normal developer suites can skip unavailable external binaries, optional soak
  and privileged platform fixtures. Separate strict reports provide those gates;
  a normal suite pass alone is not strict acceptance.
- Older strict process, address, idle and soak results retain their recorded
  revisions. They are not relabeled as executions of the final amended commit.
- Linux runtime evidence is arm64 in isolated containers. Linux amd64 is a build
  artifact, not a claim of runtime tests on that architecture.
- Core pipe semantics can discard empty UDP application writes before the packet
  adapter. Zero-byte rejection is an adapter contract, not a new whole-pipeline
  empty-datagram API. TCP upload EOF does not promise remote TCP half-close.
- Sidecar integration uses a candidate Core modfile; its production dependency
  pin and any deployment remain separate release work.
- Shared redirect cancellation preserves detachment from the original request;
  explicit connection Close cancels only the connection-owned relay context.
