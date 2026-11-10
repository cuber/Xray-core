# Tasks: Connection Control

- [x] T001 spec, plan and tasks; index row in `spec/README.md`; xray-config spec034 for the gate, the consumers and
  the rollout.
- [x] T002 Registry kinds, accept-time binding for Hy2, the per-connection dispatcher and carrier marks; FR-001–FR-003.
- [x] T003 `QuicStatsSet` census and claim, `QuicCloser`; SS-2022 user sessions; the handler's report and close;
  FR-005–FR-007.
- [x] T004 `GetConnections` and `CloseConnections` with their contract; FR-004, FR-008.
- [x] T005 `xray api conns` and `connclose`; FR-009.
- [ ] T006 Full suite, race, format; `xrayctl build core`; the gate (xray-config spec034 `connclose-check`, with
  `singmux-check` A/B, `migration hy2-check` and `relay-check`); FR-010.
- [ ] T007 CP-8 consumers in their own repositories: Sidecar spec022 (`/xray-debug` `connections`, the tokened close
  endpoint), Auth and Portal under xray-config spec034.

## Evidence

- 2026-11-10 T001: specified from the code at `1f4d2ff7` and approved by the user the same day (the design is
  xray-config draft 008, promoted into this spec and xray-config spec034). Binding sites: `worker.go` (`callback`,
  `accept`), `hysteria/hub.go` (`AuthHTTP` sets the QUIC connection's user; `StreamDispatcher` hands streams with
  their user to the worker), `shadowsocks_2022/replay.go` (sessions bound at authentication).
