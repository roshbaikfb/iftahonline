#!/usr/bin/env bash
# Fails on breaking changes to api/openapi.yaml relative to the base branch
# (CLAUDE.md rule 11: no breaking changes inside /v1).
set -euo pipefail

SPEC=api/openapi.yaml
BASE_REF="${OPENAPI_BASE_REF:-origin/main}"

if ! git rev-parse --verify --quiet "$BASE_REF" >/dev/null; then
  if git rev-parse --verify --quiet main >/dev/null && [[ "$(git rev-parse --abbrev-ref HEAD)" != main ]]; then
    BASE_REF=main
  else
    echo "openapi-diff: no base ($BASE_REF) to compare against; skipping"
    exit 0
  fi
fi
if ! git cat-file -e "$BASE_REF:$SPEC" 2>/dev/null; then
  echo "openapi-diff: $SPEC does not exist on $BASE_REF yet; skipping"
  exit 0
fi

base="$(mktemp --suffix=.yaml)"
trap 'rm -f "$base"' EXIT
git show "$BASE_REF:$SPEC" >"$base"
go tool oasdiff breaking "$base" "$SPEC" --fail-on ERR
echo "openapi-diff: no breaking changes vs $BASE_REF"
