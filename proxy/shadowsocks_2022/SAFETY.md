# Native SS2022 safety evidence

## Scope and provenance

Only `proxy/shadowsocks_2022/**` was edited for this task. No commit, release
build, deployment, old singbridge restoration, or unrelated spec port was done.

- Historical fork: `1fd51943d81ed297636581724f6f2ebeaa50f62a`.
- Selected upstream: `0888beb38a9ba45440557175e71ce21cf8ba3b13`.
- Shared candidate HEAD at execution: `682406c3d9924125fcbcecccdd742553b2733a73`.
- Candidate worktree is dirty, including other agents' unrelated ports.
- `safety-source.sha256` records every package Go source at first soak start.
- Manifest SHA-256: `31f74ce9ad266f74ffc4ab78c2ad431f63d91c5f4e3752f185bbc0f4f4b29dfd`.
- Restart manifest: `safety-source-r2.sha256`, SHA-256
  `d024858eea2ab9439292d70304721b53cee8a793f17dcabe9ccacbcdf8f7eed8`.
- Darwin: Go 1.27.0, darwin/arm64.
- Docker: Go 1.27.0 selected with `GOTOOLCHAIN=auto`, linux/arm64.
- Docker base: `golang:1.26.1`, image
  `sha256:2abfa7348a8006cb2d2374824e6e7de7ae12862edd87560ebb55ca2cbae777a8`.

Read contracts: spec010 U-001/U-002/U-003 and V-003/V-004/V-005, plus spec007
V-032's historical outbound connection-close regression. These results do not
claim the complete spec010 upgrade.

## Reproduced defects and fixes

Before fixing the native implementation:

1. `TestSafetyInvalidUDPAllocationWaves` failed on its first wave: 200 invalid
   AEAD packets retained 200 sessions and 200 replay windows.
2. `TestSafetyUDPContextIsolation` failed: parent identity changed to B, and A's
   previously saved dispatcher context reported B.
3. `TestSafetyUDPCloseDuringDispatch` failed after one second: Close waited on
   the session mutex held across Dispatch instead of cancelling the dispatch.

The candidate authenticates before publishing session/window/cipher state,
serializes replay admission, copies inbound metadata per session, and releases
the session mutex around dispatch. Close cancels pending work; a late returned
link is interrupted, not published. Pointer-conditional removal prevents an old
session from deleting a replacement. Replies bind to their original session,
not a lookup that could create or use a replacement session.

Additional regression protection covers deadline set/clear errors, complete data
returned with EOF, early-link cleanup, outbound context/timer cleanup, timer-driven
quiet expiry, dynamic duplicate-key rejection and concurrent UDP replay admission.

## Policy

- Native TCP already had a finite level-0 handshake deadline and deferred socket
  close. This work does not misattribute those upstream changes to this patch.
- The authentication budget is absolute. Zero handshake timeout retains native
  semantics: the read deadline is immediate, not disabled.
- After authentication, deadline clearing must succeed before dispatch. Normal
  activity/half-close timers use the authenticated user or relay destination's
  level. Tests make level-0 idle only 10 ms and level-7 idle 10 s, then transfer
  exact early and late bytes after three seconds.
- SIP022's native single-read initial-header rule is preserved. Short initial
  headers are rejected; the subsequent variable-header read remains inside the
  absolute handshake deadline. Real external-client fragmentation compatibility
  is not inferred from these fixtures.
- RemoveUser denies later UDP admission, removes that identity's cached UDP
  sessions, cancels pending dispatch and interrupts its links. B is unaffected.
  Already admitted/in-flight I/O may complete; instantaneous wire-level revocation
  is not promised. Already-authenticated TCP sessions keep their ordinary lifetime.
- Re-add with a new key creates a new identity. Old keys remain denied. Duplicate
  key aliases are rejected so one identity cannot overwrite another's key lookup.
- A session-ID collision across identities/relay destinations is rejected, never
  reused across users. Source roaming updates only the authenticated session's
  reply transport. Session close does not close a shared UDP transport.
- Single/multi-user UDP publishes state only after valid authentication and
  parsing. Relay can validate its EIH destination, not arbitrary downstream AEAD;
  relay forwarding preserves this protocol boundary.
- Quiet UDP sessions have timer-driven expiry, independent of future packets.
  Manager close prevents further publication. Response completion retires its
  own session; tests join response workers explicitly.

## Executed short gates

Commands run from the candidate root:

```sh
go test -race ./proxy/shadowsocks_2022 -count=1 -timeout=3m
go test -race ./proxy/shadowsocks_2022 -run 'TestSafetyUDP|TestOutbound' -count=10 -timeout=90s
XRAY_SS2022_SOAK=1 XRAY_SS2022_SOAK_DURATION=10s go test -race ./proxy/shadowsocks_2022 -count=1 -timeout=3m
go vet ./proxy/shadowsocks_2022
git diff --check -- proxy/shadowsocks_2022
```

Passed on Darwin, including the final full-package race invocation with the
10-second opt-in soak (68.462 s). UDP/outbound repeat-10 passed (5.526 s).
The frozen-source default package race invocation also passed (53.304 s), with
the opt-in hourly test skipped as intended.
One later vet attempt transiently failed importing standard-library reflect/sync
(`open : no such file or directory`); the identical retry passed. It was not
hidden with vet suppression or a code change.

Linux package race gate passed (52.054 s), using the container command below
without the soak environment variables and with
`go test -race ./proxy/shadowsocks_2022 -count=1 -timeout=5m`.
The hourly test is explicitly skipped in ordinary runs; it does not block the
main full suite unless opted in.

Coverage and resource oracles:

- 200 real silent TCP connections in 10 waves for EACH of single, multi and relay:
  server-side closure, Process completion, exactly one accepted-socket close,
  no dispatch, joined handlers, process FD/post-GC heap samples.
- Salt-only, incomplete fixed header, wrong key, slow variable-header trickle,
  zero handshake policy, deadline failures and data-with-EOF cases.
- Three-second successful sessions for all TCP variants, early and late exact
  payloads, finite-then-cleared deadline calls and authenticated-level idle policy.
- Production HandlerService over local gRPC: add/list/count/remove/re-add and
  duplicate rejection; A/B UDP admission, old-key rejection and B continuity.
- 32 concurrent duplicate UDP decodes: exactly one admission.
- 2,000 invalid single-user AEAD packets and 2,000 mixed multi-user invalid
  EIH/AEAD packets over repeated waves: zero retained sessions/windows.
- Ten concurrent A/B shared-transport/roaming UDP waves: exact per-user decoded
  payload/destination, stable dispatcher identity, zero retained sessions/windows,
  and explicit joins of process and reply goroutines.
- Real quiet timer expiry and injected cleanup tick without another new packet;
  closed manager/session cannot recreate link or cipher state.
- Restored native-adapted spec007 outbound-close TCP/UDP normal/TimeoutOnly test.

## Soak harness

`TestSS2022ResourceSoak` is opt-in with `XRAY_SS2022_SOAK=1`; duration defaults to
one hour and can be set with `XRAY_SS2022_SOAK_DURATION` (Go duration syntax).
Five warm-up waves are always run, even for a shorter requested duration.

Each wave creates ten real loopback TCP connections: valid, silent, partial,
wrong-key and immediate-close cases for each of two users. Valid sessions send
64 early bytes plus 65,536 subsequent bytes, verified exactly in both directions.
The dispatcher checks user-specific destinations and uses production
`dispatcher.SizeStatWriter` with registered production Stats Manager counters
named `user>>>alice/bob>>>traffic>>>uplink/downlink`. This is not a claim of
end-to-end Stats RPC or the full production routing pipeline.

Every wave joins fixture client/handler/echo workers, verifies accepted/client
socket ownership reaches zero, and checks native TCP copy stacks drain too.
Wave drain is bounded to five seconds; native sibling-copy drain has an additional
one-second bound. Counter totals must be exact, including no invalid-user bytes.

Samples include pre-GC HeapAlloc, two-GC retained HeapAlloc/objects, process FDs,
process goroutines and exact owned resources. Linux uses `/proc/self/fd` (excluding
the sampling directory FD); Darwin uses the AnyTLS-style RLIMIT/F_GETFD oracle,
not `/dev/fd` enumeration or a global FD skip. Samples stream to stdout so test
logging does not itself retain an hour of heap. After five warm-up waves, process
FD/goroutine diagnostics allow +2 above the observed warm maximum; retained heap
allows four times observed warm variance with a documented 1 MiB runtime floor.
Exact owned counts remain zero regardless of these diagnostic tolerances.

Darwin smoke evidence:

- 10 s / 10 waves: 100 connections, FDs 5 throughout and 4 after listener close;
  each user uplink/downlink 656,000/656,000; post-GC heap first/last
  1,075,288/1,120,328 bytes.
- 65 s / 64 waves: 640 connections; exact owned counts zero after each wave;
  FDs 5 and final 4; each user uplink/downlink 4,198,400/4,198,400;
  post-GC first/last 1,082,664/1,228,800 bytes, final 1,226,960 bytes.
- The native-copy-stack guard was added after the 65 s run and exercised in the
  final full-package race run with the 10 s soak. Source manifest is the final
  version used for the Linux hourly run.

## Linux hourly run

First attempt started 2026-10-14 23:09:27 CST (15:09:27 UTC) and failed after
70.99 s. Raw events are preserved in `soak-linux-20261014.jsonl`. The oracle saw
`sockets=0 workers=1`: fixture defer order released WaitGroup waiters before
decrementing the owned-worker count. This test-harness ordering defect was fixed
by publishing the decrement before Done. It is not treated as a passing hour or
as evidence of a native socket leak. The corrected Linux full-package race rerun
with a 90 s soak passed (142.275 s), and active-link RPC revocation plus the other
UDP/outbound regressions passed Linux race repeat-10 (2.205 s).

The restarted one-hour run PASSED, exit status 0, at 2026-10-15 00:16:50 CST
(2026-10-14 16:16:50 UTC). Test duration was 3,600.27 s; package duration was
3,601.304 s. The permanent [hourly summary](soak-linux-20261014-r2.summary.jsonl)
retains ALL 3,549 resource samples, final drain, test/package PASS events, the
original command, raw-log SHA-256 and full source manifest with its SHA-256.
The complete per-request log is archived locally at
`safety-artifacts/soak-linux-20261014-r2.jsonl` (ignored, not a Git artifact), SHA-256
`5c50b9c182271e178ea85d9c12b7456d439138b48aa410c0652b0f5437066f34`.
Its source is pinned by `safety-source-r2.sha256`; every package Go source hash
was verified unchanged after completion.

- 3,549 waves / 35,490 real connections; owned connections, fixture workers and
  native TCP copy goroutines reached zero after every wave.
- Process FD min/max: 7/7; final FD after listener close: 6.
- Process goroutines: 3 in the final sample and after final drain.
- Each of alice and bob: exactly 232,814,400 uplink bytes and 232,814,400 downlink
  bytes, with corresponding exact echo payload assertions.
- Pre-GC HeapAlloc min/max: 1,215,320 / 3,137,248 bytes.
- Post-GC HeapAlloc first/last: 1,027,256 / 1,414,208 bytes; observed min/max:
  1,027,256 / 1,423,608 bytes. Final post-listener-close heap: 1,412,368 bytes,
  4,336 objects. No resource-bound assertion or race report occurred.

```sh
docker run --rm --name xray-ss2022-safety-hour \
  -e GOTOOLCHAIN=auto -e GOPROXY=https://goproxy.cn,direct \
  -e XRAY_SS2022_SOAK=1 -e XRAY_SS2022_SOAK_DURATION=1h \
  -v /Volumes/Linux/opensource/cuber/xray-core-rebase-20261014:/src:ro \
  -v xray-upgrade-go-mod:/go/pkg/mod \
  -v xray-upgrade-go-build:/root/.cache/go-build -w /src \
  golang:1.26.1 go test -json -race ./proxy/shadowsocks_2022 \
  -run '^TestSS2022ResourceSoak$' -count=1 -timeout=70m
```

The actual invocation pipes generated output through `tee` with shell pipefail
enabled, preserving Docker/test exit status. Its original output path is retained
in the summary's command; the raw file was subsequently moved to the ignored
archive without changing its bytes. The small failed-first-attempt log remains
in Git-visible evidence so its harness failure is not hidden.

Permanent evidence index (all paths relative to this report):

- [Hourly resource samples and provenance](soak-linux-20261014-r2.summary.jsonl)
- [Frozen source manifest](safety-source-r2.sha256)
- [First-attempt source manifest](safety-source.sha256) and
  [first-attempt failure log](soak-linux-20261014.jsonl)
- [Darwin FD baseline](safety-baseline-20261014.json) and
  [Linux FD baseline](safety-baseline-linux-20261014.json)
- [Darwin independent/core interop](safety-interop-20261014.json) and
  [Linux core interop](safety-interop-linux-20261014.json)
- [Darwin duplicate-key behavior](safety-duplicates-20261014.json) and
  [Linux duplicate-key behavior](safety-duplicates-linux-20261014.json)
- [Executable fixture harness](safety_interop.py)

## Actual old / upstream / candidate FD baseline

`safety_interop.py --mode baseline` ran isolated Darwin/arm64 executables built
from the clean exact old fork, an extracted pristine selected-upstream tree, and
the dirty candidate. Executables and extracted sources are ignored under
`safety-artifacts/`; no installed executable/service was replaced or restarted.
The approved existing local executable reports baf3672, not 1fd51943, and was
therefore NOT used as the exact-old baseline. Old/candidate test builds preserve
VCS metadata; the pristine extracted test build uses `-buildvcs=false` and is
pinned to the selected source commit. These are not production release builds.
Versions and SHA-256s are recorded in `safety-baseline-20261014.json`.

Each binary ran single-user, multi-user and relay configurations with the same
one-second handshake policy. For each configuration, ten waves opened twenty
silent real TCP peers and left them open for 1.5 seconds before observing EOF
and counting server-process numeric FDs with Darwin lsof. Peers not closed by
the server stayed open across waves, so client-side cleanup cannot fake success.

| Binary | Each configuration's baseline FD | Final-wave FD | Silent peers still open |
| --- | ---: | ---: | ---: |
| Exact old 1fd51943 | 8 | 208 | 200 |
| Pristine upstream 0888beb3 | 8 | 8 | 0 |
| Frozen candidate | 8 | 8 | 0 |

All three old configurations grew exactly twenty FDs each wave. Only after the
harness closed the retained peers did old FDs return to eight (sampled after
three seconds). This reproduces unbounded silent-peer retention beyond policy,
not an assertion that old sockets remain leaked after peer close. Upstream and
candidate remained at eight after every wave and after final drain. In total,
the executable comparison used 1,800 silent connections across nine runs.

Attribution: selected upstream already fixes this historical TCP behavior via
native handshake timeout/close ownership; this task preserves and measures that
behavior. The new patch's independently reproduced fixes concern UDP admission,
identity, dispatch cancellation and resource lifetime. The one-hour run is
candidate-only evidence, not a one-hour old/new comparative experiment.

The identical nine-run comparison also passed on Docker Linux/arm64, using
`/proc/<server-pid>/fd`. All counts in the table above matched exactly, including
return to eight after peer cleanup. Raw results and binary hashes are in
`safety-baseline-linux-20261014.json`. This adds another 1,800 silent connections.
Linux isolated old used Go 1.26.1; upstream/candidate used auto-selected Go 1.27.0.
All three Darwin executables used Go 1.27.0. Linux fixture executables were built
with `-buildvcs=false` from read-only mounted sources into package-local ignored
artifacts; they are test-only, not release/Portal version-audit artifacts.

## Executable interoperability

`safety_interop.py --mode interop` passed all 20 cases on Darwin/arm64. Raw
structured results and exact executable hashes are in
`safety-interop-20261014.json`.

- Exact old client -> candidate server: five cases passed.
- Candidate client -> exact old server: five cases passed.
- sing-box 1.14.2 (af6e64c3) -> candidate server: five cases passed.
- Mihomo Meta v1.19.31 -> candidate server: five cases passed.

Each five-case set covers AES-128 and AES-256 single/multi-user, plus ChaCha
single-user. Each case verifies 131,136 TCP bytes in EACH direction, including
131,072 bytes sent after three seconds, beyond the server handshake budget.
Each also verifies twelve SOCKS-associated UDP packets alternating two local
destinations, with exact payload/address/port and 8,832 bytes each direction.
Only loopback echo targets were used. These are exact payload counts, not a
claim about production user-stat RPCs or every independent-client feature.

Linux/arm64 also passed the ten old/candidate bidirectional cases with the same
byte/packet assertions. Results are in `safety-interop-linux-20261014.json`.
Independent sing-box/Mihomo execution was Darwin-only.

## Duplicate-key migration blocker

`safety_interop.py --mode duplicates` uses synthetic keys and the reported
`admin` / `la.akile.lite` labels, not production secrets. It tests both orders,
distinct keys while retaining BOTH users, and actual HandlerService removal.
Results and access-log identities are in `safety-duplicates-20261014.json`.
The complete twelve-case executable duplicate-key suite passed on Linux too,
with identical behavior (`safety-duplicates-linux-20261014.json`).

| Behavior | Old fork | Pristine upstream | Candidate |
| --- | --- | --- | --- |
| Same key, admin then peer | Authenticates as peer | Authenticates as peer | Explicit config rejection |
| Same key, peer then admin | Authenticates as admin | Authenticates as admin | Explicit config rejection |
| Distinct keys, both users retained | Both identities work | Both identities work | Both identities work |
| Remove admin from same-key pair | Peer still works | Peer also loses authentication | Pair cannot be admitted |
| Remove admin from distinct-key pair | Not separately tested | Not separately tested | Peer still works |

Duplicate rejection is NOT required to fix the TCP FD problem, nor mandated by
the SS2022 protocol. It is an intentional compatibility break supporting this
candidate's independent per-user admission, accounting, routing and revocation
contract. The protocol carries the key-derived identity, not the configured
email: identical keys cannot identify two independent users. Simply removing
the check restores silent overwrite and upstream's cross-user removal failure.

Recommended explicit migration: retain both users, inventory all callers of the
shared credential, assign a distinct credential to one identity and update its
callers in a coordinated rollout. Keeping the existing shared key assigned to
the currently effective `la.akile.lite` identity and rotating admin is one option,
not an authorized credential change. Validate both identities, destinations,
stats and removal isolation before cutover. The inspected configuration routes
both labels to direct today; that does not make their authentication identities
independent, or prove all callers have been inventoried.

If zero credential change is mandatory, a separately reviewed compatibility
mode must model this as ONE shared principal, explicitly define deterministic
selection and group revocation, and avoid claiming independent alias identity.
That requires more than suppressing duplicate validation, changes the security
contract and needs new lifecycle/race/resource tests. It has NOT been implemented.
No production credential/user was changed or deleted. The real-config migration
blocker remains until an explicit migration or compatibility policy is approved.

On 2026-10-15 the user requested continuing with fixes. The isolated Config
candidate now assigns a distinct admin key and updates ASZ's matching management
caller. The node-user key, Hy2 identities and all production files remain
unchanged. A real local SS wire/control test verifies both users' route markers,
exact isolated counters, and removal of admin while the node's existing and new
connections survive. Config's 45/45 runtime parser gate and 519-test suite pass
at the recorded checkpoint. Thus this specific candidate migration blocker is
resolved, not bypassed by relaxing key uniqueness; coordinated production
deployment is still required. Evidence is owned by Config's
`docs/audits/2026-10-14-ss2022-policy-handoff.md` and
`test/routing/test_la_ss2_identity.py` on the matching upgrade branch.

## Remaining acceptance boundaries

One-hour candidate resource acceptance and short old/upstream/candidate baseline
comparisons are established for the tested workloads. This is not an assertion
of leak absence for every possible workload. Package Go sources remain identical to the restart manifest;
the executable harness and evidence documents do not alter the running test.

Not established by this task: Linux amd64 execution,
production canary/24-48 hour behavior, all explicit half-close-policy transition
cases, or the complete spec010/whole-Core race gate. Unrelated ports and the
reported REALITY issue belong to other agents. No production resources were used.

### Subsequent Local Acceptance (2026-10-15)

The complete SS2022 package executed successfully as a linux/amd64 static test
binary in Docker under the host's arm64-to-amd64 translation support. This is
actual Linux executable testing, not native amd64 hardware or race instrumentation.
The opt-in one-hour soak was not repeated in that package invocation. Log:
`/tmp/core-upgrade-ss2022-linux-amd64.log` (the prior one-hour Linux/arm64 race
soak remains independent evidence).

`TestTransportTCPHalfClosePolicy` adds explicit uplink-EOF, downlink-EOF and
idle-policy coverage of the shared SS transport. Each half-close still delivers
exact bytes in the remaining direction, then requires that direction's shorter
policy to terminate the connection. Owned readers retire after caller shutdown.
Darwin race count=10 passed (8.646s). A temporary read-only Go overlay replacing
both half-close timeout selections with ConnectionIdle failed BOTH half-close
cases as intended (6.502s); no production source was mutated by this control.
Negative log: `/tmp/core-halfclose-mutation-negative.log`.
The same three cases passed count=20 on translated Linux/amd64 as well:
`/tmp/core-halfclose-linux-amd64.log`. Full SS2022 and inbound packages then
passed Darwin race count=3 (158.823s / 14.371s), including these new cases:
`/tmp/core-ss-inbound-resume-race.log`.
