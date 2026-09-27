# Outbound Design Audit - 2026-09-27

Result: native outbound is implemented locally; full release acceptance is not
complete. See [execution evidence](outbound-implementation.md).
This audit distinguishes closed spec ambiguities from implementation
gates. No deployment is authorized by this document.

## Findings resolved in the contract

1. Earlier text required total-pool capacity/queuing despite reference clients and
   Xray mux not implementing that policy. Removed; retain native selection, test
   cleanup, and do not invent a cap or conflate mux MaxConnection with pool size.
2. Removal originally implied force-close, contrary to Core's registry removal and
   user-approved drain semantics. Specify optional retirement and preservation of
   active streams; full shutdown remains immediate. Other protocols remain unchanged.
3. Timer default semantics were ambiguous. Follow the existing engine's <=5s ->
   30s behavior, uint32 seconds and portable minIdleSession integer range. Add exact
   boundary tests; level/email are local metadata, password remains opaque.
4. sing-box's multiplex context helper clears metadata, not cancellation. Chained
   proxySettings and dialerProxy have different lifetimes. Tests must prove both;
   do not claim either universal Background or WithoutCancel is sufficient.
5. Existing external-client tests only establish inbound interoperability. New
   acceptance includes external servers and strict-mode dependency checks.
6. Existing OB HTTP tests replace tagged.Dialer and cannot prove actual dispatch,
   AnyTLS or remote UDS. B cases now require the full chain and timeout cleanup.
7. Task completion differs from protocol FIN: unread data and copy workers can
   remain. Physical-pool retirement and complete adapter teardown need separate
   evidence; do not count Process return as proof of zero workers.

## Implementation review gates

- Existing TestAnyTLSJSON rejects an empty outbound and labels all outbounds
  unsupported. Retain invalid empty-config rejection, change its obsolete message
  and add valid full-outbound tests when registration lands. Do not erase the test.
- Native ClientConfig validation alone cannot inspect sender TLS/mux settings.
  Add runtime handler-level validation equivalent to JSON before accepting gRPC
  configurations; rejected handlers must release owned resources.
- Decide the admission linearization point in implementation tests: admitted
  logical streams drain, incomplete new dialing may cancel, late completions never
  register. Verify with barriers before claiming retirement correctness.
- Dynamic sendThrough and request-dependent TLS must not reuse incompatible
  physical connections. Before enabling these combinations, prove correct routing
  identity or explicitly reject unsupported combinations; do not silently ignore.
- Reverse handlers also use outbound manager; optional lifecycle hooks must not
  introduce any protocol-name special case or change their deletion behavior.
- Core instance shutdown needs ownership of already removed draining handlers.
  Avoid permanent retained registries and callbacks under manager locks.

## Implementation boundary

ClientConfig, JSON registration, native validation, Process, UoT and optional
handler retirement are implemented. The pool uses handler-owned dialing contexts,
an admission barrier after authentication/SYN writes, and explicit copy-worker
completion. Canceled/failed records retire their connection before reuse. The
native adapter enables cancelable writes because dispatch-backed connections may
ignore socket deadlines; the cancellation watcher is joined before releasing the
write lock. Control frames have a bounded transport-close fallback. Session
reservation under the existing pool lock closes the take/open publication gap.
No global connection cap, active packing algorithm, or additional pool mutex was
introduced. Remaining OT/B/C evidence is not implied by these code changes.
