#!/usr/bin/env bash
# Verifies the systemd units: syntax via `systemd-analyze verify` and
# hardening via `systemd-analyze security --offline`, failing if any unit's
# exposure score exceeds MAX_EXPOSURE.
set -euo pipefail

MAX_EXPOSURE="${MAX_EXPOSURE:-2.0}"
if ! command -v systemd-analyze >/dev/null; then
  echo "systemd-verify: systemd-analyze not available; skipping"
  exit 0
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
# verify needs ExecStart to exist; point units at a stand-in binary.
install -m 0755 /bin/true "$work/iftahonline"
for unit in deploy/systemd/*.service; do
  sed "s#/usr/local/bin/iftahonline#$work/iftahonline#g" "$unit" >"$work/$(basename "$unit")"
done

(cd "$work" && SYSTEMD_LOG_LEVEL=err systemd-analyze verify --man=no ./*.service)
echo "systemd-verify: unit files are valid"

fail=0
for unit in "$work"/*.service; do
  score="$(systemd-analyze security --offline=yes --no-pager "$unit" 2>/dev/null | grep 'Overall exposure level' | grep -oE '[0-9]+\.[0-9]+' | tail -1)"
  printf 'systemd-verify: %-36s exposure %s\n' "$(basename "$unit")" "$score"
  if awk -v s="$score" -v m="$MAX_EXPOSURE" 'BEGIN { exit !(s > m) }'; then
    echo "systemd-verify: $(basename "$unit") exposure $score > $MAX_EXPOSURE" >&2
    fail=1
  fi
done
exit "$fail"
