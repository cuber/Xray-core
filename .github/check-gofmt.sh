#!/usr/bin/env bash
# Check all Go files touched by this fork, including untracked files locally.
# The fork point survives history reconstruction; intermediate fork commits do not.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
BASE=b4f08981becb71eaa995fa98ed2098ade92566bb
git cat-file -e "$BASE^{commit}" || {
  echo "Missing stable fork baseline $BASE; fetch history through the fork point." >&2
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
