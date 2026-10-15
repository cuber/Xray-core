# Local Candidate Verification

## Scope and Source

This is the isolated fixed-main candidate, not adopted develop or a production
release. The fixed upstream base is
`0888beb38a9ba45440557175e71ce21cf8ba3b13`.
The last runtime change is `d2cda3ef` (direct Hy2 loopback source binding), after
`aa6ca3ed` (reverse drain publication). Later evidence-only commits do not alter
the tested source. Official artifact
identities and paired operational checks are maintained by Config's
`docs/audits/2026-10-15-main-upgrade-verification.md` on the matched candidate.

Counts below are test/subtest PASS events, excluding package PASS events. Commands
used `-count=1` and Go 1.27.0 unless stated otherwise. Socket-heavy gates ran
serially. Earlier failed runs remain in the owning specifications.

**Subsequent cross-component repair:** after Sidecar normal/race both passed
1020 test/subtest events, the extended 1000-chain run failed at batch36's
Hy2-to-SOCKS request with a 4s timeout and no ingress bytes on either remote
fixture. `/tmp/sidecar-final-aa6c-churn-1000.log` contains stacks and counters.
An independent repeated handshake captured a concrete Darwin wildcard/specific
UDP same-port collision; a deterministic real-wire regression fails with the
original source and passes with explicit loopback source binding. The unchanged
1000-chain gate then passes (22.081s), as do 1000 fresh handshakes. The original
churn log lacks its client local port, so common-cause attribution remains an
explicit inference, not a retrospectively observed port value. Evidence and
the narrow helper's preservation boundaries are in
[spec002](../002-core-connection-lifecycle/hy2-loopback-source-20261015.md).
The final changed-leaf and paired artifact checks follow below; a waiting cleanup
lock alone was not the root cause and no timeout/retry was relaxed.

## Darwin Gates

| Partition | Source | PASS / FAIL / SKIP | Evidence |
| --- | --- | --- | --- |
| All packages except scenarios/AnyTLS, normal | `098703aa` | 2688 / 0 / 24 | `/tmp/core-upgrade-final-0987-packages-normal.jsonl` |
| Same partition, race | `8e246952` | 2698 / 0 / 22 | `/tmp/core-upgrade-final-8e24-packages-race.jsonl` |
| Complete scenarios, normal | `8e246952` | 90 / 0 / 0, 281.215s | `/tmp/core-upgrade-final-8e24-scenarios-normal.jsonl` |
| Complete scenarios, race | `aa6ca3ed` | 90 / 0 / 0, 1053.085s | `/tmp/core-upgrade-final-aa6c-scenarios-race.jsonl` |
| AnyTLS adapter and engine, normal | `f4466f6f` | 609 / 0 / 6 | `/tmp/core-upgrade-final-f446-anytls-normal.jsonl` |
| AnyTLS adapter and engine, race | `098703aa` | 609 / 0 / 6 | `/tmp/core-upgrade-final-0987-anytls-race.jsonl` |

Between f4466f6f and 098703aa only two non-AnyTLS tests/evidence were added.
8e246952 applies strict upstream formatting and protobuf compiler-stamp comments;
it changes no runtime algorithm or descriptor. aa6ca3ed changes only reverse
drain publication, its tests and owning documentation. Its app/reverse package
race x50 passes (1.876s); both real reverse scenarios pass in 22.018s, followed
by the complete race scenario pass above. This is partitioned verification with
explicit source boundaries, not a claim that one `go test ./...` invocation
executed every platform/opt-in test on the final documentation commit.

The remaining-package race run enables the eight independent SS first-read
cases. The normal partition leaves that explicit native-client opt-in disabled;
its separate native race invocation passes. Ordinary skips include thirteen
public DNS/ECH cases, separately executed hourly SS soak, credentialed cloud
storage tests and the Darwin-inapplicable system-CA subprocess. A race-only
child negative control adds one normal-run skip. Skips are not passes.

AnyTLS's six opt-ins, source-alias platform boundary, native client identities
and two strict ten-minute soaks are recorded in
[its owning report](../../proxy/anytls/UPGRADE-20261014.md#serial-final-verification-2026-10-15).
Both soaks retire all tracked workers/connections/readers/HTTP handlers.

## Linux Execution

The final runtime checkpoint aa6ca3ed also passes ten complete packages under
Linux/arm64 Go 1.27.0, race enabled, 283 PASS events, zero failures and three
explicit opt-in skips. Log: `/tmp/core-upgrade-final-aa6c-linux-runtime.jsonl`.

```sh
go test -json -race -p 1 -count=1 -timeout=5m ./app/reverse ./common/signal \
  ./common/buf ./transport ./transport/internet ./transport/internet/hysteria \
  ./app/proxyman/inbound ./app/commander ./proxy/shadowsocks_2022 ./core
```

Execution used an owned read-only, network-none Docker container, the existing
cached Go 1.27.0 toolchain explicitly (the bootstrap image tag is 1.26.1), a
read-only module/source mount and separate writable build cache/tmpfs. The
container was removed automatically. This executes Linux listeners, syscall,
worker retirement, accepted TCP/UDS, SS resource/identity and Hy2 tests; it is
not merely cross-compilation, nor a full Linux whole-tree/native-amd64 claim.
The three skips are socket-mark's unavailable `CAP_NET_ADMIN`, the independently
executed native-client first-read gate, and separately recorded hourly SS soak.
The independent source-alias race test and system-only CA evidence remain
separately recorded in the owning reports.

## Last Changed-Leaf Gate

d2cda3ef changes only the direct Hy2 literal-loopback source choice and its tests.
It preserves explicit source, dialerProxy, ownership and cancellation. The full
Hy2 directory race passes (13.191s, plus BBR/Brutal), as do source/context and
deterministic wire controls, 1000 fresh handshakes, and unchanged 1000-chain race.
These results do not erase the earlier churn failure or claim it captured the
same local port. Runtime diagnostics were removed before acceptance.

The affected AnyTLS chain/process/address selection passes race with explicit
native clients: **121 PASS events, no failures, two helper/diagnostic skips**, main
adapter 270.099s. Log: `/tmp/core-final-loopback-anytls-affected.jsonl`.
Command: `go test -json -race -count=1 -timeout=10m -run
'Chain|Process|Hysteria|HY2|Address' -skip '/source-alias$' ./proxy/anytls/...`.
The two skips are the explicit chain-fault diagnostic and subprocess-helper entry;
the parent process tests actually execute. No new full ten-minute AnyTLS soak or
whole scenario run is inferred from this focused follow-up. Earlier frozen
partition results and the last leaf's reruns together define this acceptance.

The final Hy2 directory also passes Linux/arm64 race execution: 36 PASS events,
three packages, zero failures/skips, main Hy2 12.863s. Log:
`/tmp/core-final-d2cd-linux-hy2-r2.jsonl`. The first invocation's main Hy2 package
passed, but BBR setup failed because the read-only cache lacked testify; that
invocation remains failed. Required cached testify/yaml artifacts were copied
from the existing host module cache, then the same read-only/network-none gate
was rerun. No source, test deadline, network access or container privilege was
relaxed. The Darwin-only deterministic collision fixture is intentionally not
compiled on Linux.

## Independent Contracts

- U001-003: native SS deadlines, identity/revocation, bounded resources, real
  old/upstream/candidate and independent clients: `proxy/shadowsocks_2022/SAFETY.md`.
  The independent first-read gate rejects deliberately short initial reads as
  required by SIP022; no runtime ReadFull workaround was introduced.
- U004/U009: accepted TCP/UDS workers, UDP dispatcher and Hy2 listener retirement
  have baseline-failing regressions in spec002. Final cross-protocol Sidecar
  churn is a separate paired gate, not inferred from package passes.
- U005/U008: explicit Freedom final rules and AnyTLS private-protection, 3s OB,
  retry total deadlines, transport identity and health hysteresis are exercised
  by the full owning packages and AnyTLS chain/process suites.
- U006/U007: exact stats/domain counters and wire/control tests pass; the old
  Sidecar 501ed69 actually adds/removes native SS users through candidate gRPC
  while preserving admin identity/traffic. Old executable read-only queries
  against the final official binary are tracked separately by Sidecar spec006.
- U010/U012: coherent router reload and weak matcher lifetime/resource gates
  are in `evidence/router-u012-20261014/README.md`. The added combined independent
  DAT oracle passes race x10 (70 events, 3.071s), with first-rule IDs, attributes,
  duplicates and IP/domain conjunction. No production RSS savings are inferred.
- U011: API symlink/socket ownership and Linux system-only CA subprocess tests
  pass in their owning spec009/spec007 reports, without disabling trust checks.

## Explicit Boundaries

Windows runtime validation is deferred by the user on 2026-10-15 because there
is no Windows deployment scenario. Cross-compilation passed; actual readiness,
EOF/reset/cancel/close execution remains a [TODO](../007-core-regression/tasks.md#deferred-platform-todo).

Public AdGuard DoQ paths failed both directly and through available proxies;
the diagnostic concludes they are unavailable from this host. Those opt-in
failures remain failed in spec007's Public DoQ Diagnosis, not relabeled as PASS
because local trusted fixtures pass. No public-network or production-path
reachability guarantee follows from offline acceptance.

The existing Linux hourly SS soak and dual-platform geodata/resource evidence
are scoped candidate measurements. No controlled whole-server old/new THP A/B,
24-48h production canary, native Linux/amd64 hardware soak or Windows execution
is claimed. No host TCP tuning, active proxy profile or running Xray service
was changed. Adoption, migration application and deployment remain separate.
