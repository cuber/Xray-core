# HTTP/2 Increment Acceptance

Date: 2026-10-11. Source: the local `develop` checkout, based on `7d74e1b0`.
This increment belongs to FR-002 / V-015, not the AnyTLS implementation.

## Contract And Review

- JSON `pingConfig.http2` is a boolean, default false, for both legacy and
  grouped probes. Protobuf adds field 9 without renumbering existing fields.
- The existing tagged transport remains the dialing path. The option only
  enables Go TLS ALPN negotiation; HTTP/1.1 fallback and plaintext HTTP remain
  supported. It does not enable h2c or implicitly enable HTTP Keep-Alive.
- Request method, URL/query overrides, certificate verification, 204-only
  success, no redirects, cancellation, health recovery and per-round transport
  ownership are unchanged. No dependency or production node config changed.
- Real TLS fixtures observe HTTP protocol, peer addresses, accepted/closed
  connections and server cancellation. Configuration tests cover independent
  groups and protobuf round trips. Fixtures restore the tagged dialer and do
  not claim to cover production dispatch or remote SOCKS backend stability.
- A warm connection also carries eight concurrent, barrier-held HTTP/2 streams
  on one peer/connection. It does not assert a single speculative cold-start dial.

## Executed Gates

Commands below ran from the Core repository and returned exit 0:

```sh
go test -race ./app/observatory/burst ./infra/conf \
  -run 'Test(HTTP2|ContractBurstHTTP2Config|ContractBurstJSONIndependentSettings|NewHTTPClientKeepAlive)' \
  -count=10 -timeout=5m
go test -race ./app/observatory/... ./app/router/... ./infra/conf -count=1 -timeout=5m
go test -json ./... -count=1 -timeout=15m
go vet ./app/observatory/... ./app/router/... ./infra/conf
bash .github/check-gofmt.sh
git diff --check
```

The full-suite JSON reports 87 passing packages, 1591 passing test/subtest
events, no failures and 46 skipped test/subtest events. Packages with no tests
are not counted as executed tests. Opt-in public network, platform-specific,
diagnostic, external-client and soak tests were not enabled; their skips are
not asserted as passes. The HTTP/2 fixtures and relevant regression/race gates
did execute. Full JSON and stderr are in the ignored configuration build tree:
`../xray-config/build/ob-http2-20261011/core-tests.{jsonl,stderr}`.

The full-suite counts above precede the added concurrent-stream fixture. That
fixture is subsequently included in the repeated race and package regression
commands above; no runtime source changed after the full-suite run.

## Build Boundary

The clean-tree wrapper ran from the configuration repository after the local
source commit and returned exit 0:

```sh
./scripts/xrayctl.py build core
build/xray-darwin run -test -config build/ob-http2-20261011/http2-config.json
XRAY_LOCATION_ASSET=xray/dat build/xray-darwin run -test \
  -config xray/nodes/hk.shlii.tr/config-reality-raw.json
go version -m build/xray-darwin build/xray-linux-amd64
```

The Darwin/arm64 executable was executed, Linux/amd64 is a statically linked
ELF, both configs printed `Configuration OK`, and both binaries retain
`vcs.revision`, `vcs.time`, `vcs.modified=false`. The ignored minimal fixture
enables HTTP/2 and Keep-Alive; the existing Shlii config leaves both disabled.
The final documentation amendment is followed by the same wrapper/checks so
the artifacts match the final commit. No push, server deployment, local
LaunchAgent replacement or change to Sidecar's Core pin is performed.

Shlii's Sidecar egress failures are a separate investigation. Adding a Core
HTTP/2 switch is not evidence that those failures have been fixed.
