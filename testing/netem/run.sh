#!/bin/sh
# Core spec021 FR-010: the Hy2 loss tests on the kernel's netem. They cross-compile for linux/amd64 (the fleet's
# architecture) and run in a container with --network none, whose only interface is lo; XRAY_NETEM=1 makes every
# missing precondition a failure. The run fails unless every netem test ran and passed, none skipped.
#
#   testing/netem/run.sh [extra test flags, e.g. -test.count=3]
#
# Needs docker (OrbStack on the verification Mac). XRAY_NETEM_MIRROR picks an Alpine mirror for the first image
# build, for example mirrors.aliyun.com.
set -eu
cd "$(dirname "$0")/../.."
image=xray-netem:alpine3.23
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

if ! docker image inspect "$image" >/dev/null 2>&1; then
	docker build --platform linux/amd64 --build-arg MIRROR="${XRAY_NETEM_MIRROR:-}" -t "$image" testing/netem >&2
fi
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o "$out/hysteria.test" ./transport/internet/hysteria
tests=$(grep -ho '^func TestNetem[A-Za-z0-9_]*' transport/internet/hysteria/*_test.go | sed 's/^func //' | sort)
[ -n "$tests" ] || { echo "FATAL: no TestNetem tests found" >&2; exit 1; }

status=0
docker run --rm --pull=never --platform linux/amd64 --network none --cap-add NET_ADMIN \
	-e XRAY_NETEM=1 -v "$out:/t" "$image" \
	/t/hysteria.test -test.run '^TestNetem' -test.v -test.count=1 -test.timeout=30m "$@" >"$out/log" 2>&1 || status=$?
grep -E '^ *(=== RUN|--- |netem_test.go)|^(PASS|FAIL|ok)' "$out/log" || true

for t in $tests; do
	grep -q "^--- PASS: $t " "$out/log" || { echo "FATAL: $t did not pass" >&2; status=1; }
done
if grep -q -- '--- SKIP' "$out/log"; then
	echo "FATAL: a netem test was skipped" >&2
	status=1
fi
[ "$status" -eq 0 ] && echo "netem: $(echo "$tests" | wc -l | tr -d ' ') tests passed"
exit "$status"
