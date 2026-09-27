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
