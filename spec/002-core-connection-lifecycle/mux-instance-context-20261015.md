# Mux Instance Ownership And XUDP Publication

Status: Implementing on `rebase/main-20261014`; no adoption or deployment.
These are protocol-independent lifecycle repairs discovered by spec010's
independent Sidecar/SS2/VLESS acceptance, not changes to AnyTLS pooling.

## Defects And Narrow Repairs

1. `DialingWorkerFactory.Create` used `context.Background()` for its shared
   connection. A VLESS TLS outbound with TCP Mux and a UDS `dialerProxy` therefore
   lost its Core owner. The actual resolver reported no outbound manager, zero
   relay/target bytes and EOF. Mux disabled succeeds through the identical chain.
   Both TCP Mux and XUDP factories now receive `core.ToBackgroundDetachedContext`
   at Handler construction. Create preserves its supplied owner values but
   detaches cancellation/deadlines; it still owns its own cancel function.
   A nil context keeps the legacy standalone factory behavior. No global manager,
   request-cancellation coupling, dialerProxy bypass or extra retry is introduced.
2. The XUDP janitor and session retirement read/write status under the existing
   `XUDPManager` mutex, but `handleStatusNew` published `Active` without that lock.
   A real native-client race run lasting beyond the minute janitor interval
   reports those exact read/write stacks. Active publication now uses the same
   existing mutex. No new lock, retention-window change, timer acceleration or
   disabling of XUDP is needed.

Before-fix reproduction and exact wire counters are retained by the consumer in
`../xray-sidecar-rebase-20261014/spec/006-upstream-main-upgrade/`:
`vless-matrix-owner-20261015.md` and `vless-matrix-interop-20261015.md`.
These are relative to the repository parent, not a new authoritative Sidecar
contract inside Core. Consumer acceptance must be rerun against the repaired
source; earlier standalone/direct Mux passes do not waive either failure.

## Provider Regressions

- `common/mux/client_context_test.go`: actual worker goroutine observes each
  supplied owner, a fresh Mux destination, no inherited canceled deadline, and
  its own cancellation after Process returns. Nil-context compatibility remains.
- `app/proxyman/outbound/mux_context_test.go`: two simultaneously live Core
  owners with identical outbound tags produce TCP and XUDP factories resolving
  only their own instance-local outbound managers, including canceled constructor
  contexts. No socket fixtures are used for these two tests.
- `common/mux/xudp_state_test.go`: 512 actual `handleStatusNew` activations run
  against concurrent guarded manager scanning. Each fully published session is
  Active, then actual session-manager Close retains the unchanged minute expiry.
  Explicit cleanup releases the input and observes its response worker exit.
  Run with `-race`; ordinary success alone cannot detect the original defect.

## Required Consumer And Regression Gates

1. Sidecar minimal owner reproduction: Mux off requires two real VLESS transport
   connections for two independent HTTPS requests; Mux on requires one, with
   verified TLS and identical complete payloads through the UDS prefix.
2. Full Sidecar mode/via/Mux, UDP/XUDP, wrong identity/CA/SNI, cancellation and
   replacement suites; two-owner chains and missing-tag denial, no global fallback.
3. Complete independent sing-box/Mihomo interop matrix, ordinary and race. The
   race run must include the minute-scale cleanup boundary, not stop before it.
4. Existing Mux, proxyman outbound/retirement, dialerProxy and protocol scenario
   regression; strict checkptr where supported, official Darwin/Linux build.
5. Native amd64 Akile chain matrix on isolated processes, followed by unchanged
   original-service and hash fences. No previous ELF is combined-source evidence.

The new owner tests passed race count=10. The 512-session XUDP regression passed
race count=1. A first owner-test setup attempt lacked TCP transport registration;
it failed before worker creation and was corrected by an explicit test import.
One invocation also inherited the Homebrew GOROOT; the explicit cached Go1.27
GOROOT is required. Neither infrastructure failure is relabelled as runtime PASS.
Complete consumer/native/build gates remain pending until recorded separately.
