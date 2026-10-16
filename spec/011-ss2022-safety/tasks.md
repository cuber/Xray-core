# SS2022 Safety Tasks

## Documentation Split, 2026-10-16

These checkboxes track the ownership split only. They do not claim a fresh
runtime run or mark all Core007/Core010 historical tasks complete. Imported
candidate results and their limits belong to [validation.md](validation.md).

- [x] T001 [US-001/002/003] Define the existing SS-only scope and non-production
  status in `spec.md`; no dependency; verify against SAFETY.md and owning specs.
- [x] T002 [US-001] Map deadlines, clear, first-read and relay policy to
  FR-001..003 and V-001..003 in `tests.md`; depends on T001; retain native zero
  policy and upstream attribution.
- [x] T003 [US-002/003] Map dynamic identity, admission/replay, roaming and
  retirement to FR-004..007 and V-004..007; depends on T001; preserve the relay
  AEAD boundary and in-flight revocation limit.
- [x] T004 [US-003] Move current SS outbound Close/resource ownership to
  FR-008/V-008..009; depends on T001; retain Core007 V-032's original meaning.
- [x] T005 [US-001/002/003] Add `plan.md` and historical ID/evidence mapping in
  `validation.md`; depends on T002..004; no relabeling of experimental sources,
  manifests, dated reports or test names.
- [x] T006 [US-001/002/003] Revise Core007/Core010 current navigation/plan/task
  boundaries; depends on T005; leave Core010's frozen `tasks.md` and protocol
  plan unchanged, using `current-validation.md` for current navigation.
- [x] T007 [US-001/002/003] Check local links, coverage, whitespace and protected
  evidence bytes; depends on T006; record actual document-only validation in
  `validation.md` and finish the requirements checklist.

## Unclaimed Follow-Up Gates

These are not new implementation commitments or approval requests. They preserve
the acceptance boundary when consuming the existing candidate evidence.

- [ ] F001 [US-001/002/003] Any future runtime change must rerun its relevant
  focused/race/resource gates on recorded sources; a documentation commit is not
  an automatic retest of the existing implementation.
- [ ] F002 [US-001/002/003] Core010 reconciles independent clients, whole-Core,
  native Linux and paired public-chain/resource gates at matching revisions;
  this spec's local safety evidence does not complete Core010 T025/P12.
- [ ] F003 [US-003] Production adoption, credential migration, canary/24-48h and
  whole-server THP A/B remain outside this split and need their operational
  scope. No production acceptance is claimed by the candidate one-hour soak.
