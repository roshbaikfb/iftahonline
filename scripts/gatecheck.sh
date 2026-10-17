#!/usr/bin/env bash
# Proves the architecture and duplication gates actually fire.
# See testdata/gates/README.md.
set -euo pipefail

ROOT="$PWD"
LINT="${GOLANGCI_LINT:-$ROOT/bin/golangci-lint}"
FIXTURES="$ROOT/testdata/gates"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() { echo "gatecheck: FAIL: $*" >&2; exit 1; }

# prepare <scenario|""> -> prints module dir
prepare() {
  local dir="$WORK/${1:-base}"
  mkdir -p "$dir"
  cp -R "$FIXTURES/base/." "$dir/"
  if [[ -n "$1" ]]; then cp -R "$FIXTURES/$1/." "$dir/"; fi
  cp "$ROOT/.golangci.yml" "$dir/.golangci.yml"
  echo "$dir"
}

lint() { (cd "$1" && GOFLAGS=-mod=mod "$LINT" run --config .golangci.yml ./... 2>&1); }

# 0. Control: the clean fixture must build and lint clean, so the failures
#    below are caused by the scenario and nothing else.
dir="$(prepare "")"
(cd "$dir" && go build ./...) || fail "control fixture does not build"
out="$(lint "$dir")" || fail "control fixture does not lint clean:
$out"
echo "gatecheck: ok   control fixture builds and lints clean"

# 1. Cross-module import of internal/ must not compile.
dir="$(prepare internal_import)"
if out="$(cd "$dir" && go build ./... 2>&1)"; then
  fail "cross-module internal import compiled"
fi
grep -q "use of internal package .* not allowed" <<<"$out" || fail "unexpected build error: $out"
echo "gatecheck: ok   cross-module internal import fails to compile"

# 2. A 40-line duplicated block must fail dupl.
dir="$(prepare dupl)"
if out="$(lint "$dir")"; then fail "duplicated block passed lint"; fi
grep -q "(dupl)" <<<"$out" || fail "lint failed, but not on dupl: $out"
echo "gatecheck: ok   40-line duplicated block fails dupl"

# 3. domain importing net/http must fail depguard.
dir="$(prepare depguard)"
if out="$(lint "$dir")"; then fail "domain importing net/http passed lint"; fi
grep -q "(depguard)" <<<"$out" || fail "lint failed, but not on depguard: $out"
echo "gatecheck: ok   domain importing net/http fails depguard"
