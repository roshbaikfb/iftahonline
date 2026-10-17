#!/usr/bin/env bash
# Runs every Go fuzz target for FUZZTIME (default 10s) each. Seed corpora
# and checked-in testdata/fuzz cases also run in plain `go test`.
set -euo pipefail

FUZZTIME="${FUZZTIME:-10s}"
# New "interesting" inputs are minimized for up to 60s by default, during
# which no fuzzing happens; keep the budget on fuzzing.
FUZZMINIMIZETIME="${FUZZMINIMIZETIME:-1s}"
found=0
while IFS=: read -r file _ match; do
  name="$(sed -E 's/^func (Fuzz[A-Za-z0-9_]*)\(.*/\1/' <<<"$match")"
  pkg="./$(dirname "$file")"
  echo "fuzz: $pkg $name ($FUZZTIME)"
  go test -run='^$' -fuzz="^${name}\$" -fuzztime="$FUZZTIME" -fuzzminimizetime="$FUZZMINIMIZETIME" "$pkg"
  found=$((found + 1))
done < <(grep -rn --include='*_test.go' -E '^func Fuzz[A-Za-z0-9_]*\(' --exclude-dir=node_modules --exclude-dir=testdata . | sed 's#^\./##' | sort)
echo "fuzz: $found targets passed"
