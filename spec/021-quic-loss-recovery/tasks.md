# Tasks: QUIC Loss Recovery And Redundancy

- [x] T001 spec, plan and tasks; index row in `spec/README.md`.
- [ ] T002 Spec022 done (fork, line `fork/2026-11-07/184d081eef3e` at the base, `replace`); the fork's spec-kit set up; the upstream suite passes on the
  unmodified branch.
- [ ] T003 Series 1–2: config fields, validation and R1; FR-001–FR-003 in the fork.
- [ ] T004 Series 3: copies (R2); FR-004 in the fork.
- [ ] T005 Series 4: loss counting moved out of Cubic, new counters; `ConnectionStats` additions; FR-009 in the fork.
- [ ] T006 Core: `replace` to the series head, proto fields 17–25, JSON parsing and ranges, `ApplyLossRecovery` in the
  Hy2 dialer and listener, masque and splithttp HTTP/3; FR-005.
- [ ] T007 Loss relay integration test; FR-006.
- [ ] T008 Hy2 per-handler accumulator, registry, `HandlerService.GetQuicStats`; the CP-7 contract; FR-007.
- [ ] T009 `xray api quicstats`; FR-008.
- [ ] T010 Full suite, race, format; `xrayctl build core` (`go version -m` names the replacement); `xrayctl gate plan` and
  its gate with default and spec024 parameters; spec001 inventory row for the forked dependency.

## Evidence

(none yet)
