#!/usr/bin/env bash
# Summarizes a Go coverage profile, writes an HTML report next to it and
# fails if total coverage is below COVERAGE_MIN (default 80).
#
#   go test -coverpkg=./... -coverprofile=coverage.out ./...
#   ./scripts/coverage.sh coverage.out
set -euo pipefail

profile="${1:-coverage.out}"
min="${COVERAGE_MIN:-80}"
html="${profile%.*}.html"

go tool cover -html="$profile" -o "$html"
total="$(go tool cover -func="$profile" | awk '/^total:/ {sub("%", "", $3); print $3}')"

summary="### Test coverage: ${total}% (minimum ${min}%)"
echo "$summary"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    echo "$summary"
    echo
    echo '| Package | Avg. function coverage |'
    echo '|---|---|'
    go tool cover -func="$profile" | awk '
      /^total:/ { next }
      {
        split($1, parts, ":"); file = parts[1]
        n = split(file, segs, "/"); pkg = segs[1]
        for (i = 2; i < n; i++) pkg = pkg "/" segs[i]
        sub("%", "", $NF); sum[pkg] += $NF; cnt[pkg]++
      }
      END { for (p in sum) printf("| %s | %.1f%% |\n", p, sum[p] / cnt[p]) }' | sort
  } >> "$GITHUB_STEP_SUMMARY"
fi

echo "HTML report: $html"
if awk -v t="$total" -v m="$min" 'BEGIN { exit !(t + 0 < m + 0) }'; then
  echo "coverage ${total}% is below the minimum of ${min}%" >&2
  exit 1
fi
