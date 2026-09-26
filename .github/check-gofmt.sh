#!/usr/bin/env bash
# Check all Go files touched since this gate was introduced, including untracked
# files locally. Untouched upstream formatting debt is deliberately excluded.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
BASE=1e0c6a30f2a33080b4ce55121e2e414a09f99bec
git cat-file -e "$BASE^{commit}" || {
  echo "Missing format baseline; fetch full repository history." >&2
  exit 1
}
files=$(mktemp)
trap 'rm -f "$files"' EXIT
git diff --name-only -z --diff-filter=ACMR "$BASE" -- '*.go' > "$files"
git ls-files --others --exclude-standard -z -- '*.go' >> "$files"
failed=0
while IFS= read -r -d '' file; do
  [ -f "$file" ] || continue
  if ! output=$(gofmt -l "$file"); then
    failed=1
  elif [ -n "$output" ]; then
    printf 'Run gofmt -w %q\n' "$file" >&2
    failed=1
  fi
done < "$files"
exit "$failed"
