# Regression Tests

Default protocol regressions use owned loopback DNS/TLS/HTTP2/QUIC fixtures,
including ECH acceptance, REALITY and sniffing. TLS certificates are verified
against fixture-specific roots. Dependencies and existing DAT assets must already
be available locally; this is not a promise of a hermetic build environment.

```sh
go test ./... -count=1 -timeout=15m
go test -race ./... -count=1 -timeout=15m
go vet ./...
bash .github/check-gofmt.sh
```

Public integration tests are explicitly named `TestPublic*` and opt in. They
remain failures when enabled but unreachable; they never silently bypass a proxy.
They run serially because Core's system dialer is process-global.

```sh
XRAY_TEST_NETWORK=1 XRAY_TEST_PROXY=socks5://127.0.0.1:1080 \
  go test ./app/dns ./transport/internet/tls \
  -run 'TestPublic(TCP|DOH|ECH)' -count=1 -timeout=2m
```

- `XRAY_TEST_PROXY`: SOCKS5 URL, optionally with username/password; used by
  public TCP/DoH/ECH tests and, by default, public UDP/DoQ tests.
- `XRAY_TEST_UDP_PROXY`: optional separate SOCKS5 endpoint with UDP ASSOCIATE.
- `XRAY_TEST_ECH_DNS`: public ECH resolver; defaults to Cloudflare HTTPS DoH.
- Without proxy variables, enabled public tests use the native network. The
  system-resolver integration test always uses the OS resolver, not SOCKS.
- Public dual-stack checks use `cloudflare.com`; the local fixture separately
  asserts exact A/AAAA responses, cache hits, negative replies and timeout paths.

To include public DoQ and the OS resolver, run `-run TestPublic`. A TCP-only
SOCKS server is insufficient for DoQ. Surge on the verification Mac rejects
UDP ASSOCIATE (reply 7); native UDP and local Xray UDP also timed out. Public
DoQ is therefore **not verified** on that network, despite local DoQ passing.

Do not replace failing assertions with skips, disable certificate verification,
or use `-vet=off` to obtain a green result.

## Strict AnyTLS Outbound Gates

`anytls_outbound_acceptance.py` checks required test names, nonzero execution,
package success and absence of skips. It preserves Go JSON events, stderr,
revision/dirty state, and external binary SHA-256/version metadata outside this
worktree. Groups may run separately on suitable hosts; `address` requires real
IPv6, source-alias and interface-binding capabilities (no silent strict skip).
`soak` always runs ten minutes; `idle` runs the real default timer. This runner
does not replace the full OT/B/C coverage audit or authorize deployment.
`chains` requires all 16 relay/entrance/independent-server combinations, using
the same external executable metadata and no-skip checks as `external`.

The runner requires POSIX process groups. Each command starts in a separate
session; timeout, interrupt and exceptional exit perform bounded TERM/KILL
cleanup. A command that reports success but leaves its group alive fails
acceptance. Helpers must not detach with setsid. `--wall-timeout` defaults to
900 seconds per group, including compilation, independently of Go's 12-minute
package timeout. Failure/interruption is preserved in report.json.

Groups pass an absolute `ANYTLS_EVIDENCE_DIR` to Go fixtures. Supported Core
fixtures and independent client/server helpers write redacted configuration
snapshots there (0600 files), mapped to test names in JSONL output. These are
diagnostic structures, not runnable configurations: passwords, IDs and private
keys are removed. Runtime gRPC mutations are not all captured; test source and
events remain necessary to reconstruct their sequence. Do not use production
credentials with this test harness.

The `processes` group requires nine native paths: direct and SOCKS/VLESS/AnyTLS/
Hy2 via both `proxySettings` and `dialerProxy`. Entry, relay and remote Core run
in separate race-instrumented processes. Tests require exact TCP/UDP replies,
independent relay and remote outage failure without bypass, first-request recovery after each restart,
joined child exits and final once-only backend receipts. It does not substitute
for the combined observer/balancer/Sidecar acceptance topology.

```sh
python3 testing/anytls_outbound_acceptance_test.py
ANYTLS_SINGBOX=/absolute/path/sing-box ANYTLS_MIHOMO=/absolute/path/mihomo \
  python3 testing/anytls_outbound_acceptance.py --group external --group idle \
  --output /absolute/path/outside-core/new-acceptance-run
```

Output directories must be new. Keep evidence paths private: logs use test
credentials and generated loopback configs, not production credentials.
