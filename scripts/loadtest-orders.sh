#!/usr/bin/env bash
# Starts the demo store in load mode, waits for its ready line, runs
# loadtest/orders.js with k6 (Docker, host network) and stops the demo.
set -euo pipefail

bin="$1"
port="${STORE_PORT:-18191}"
log="$(mktemp)"
"$bin" -load -addr "127.0.0.1:${port}" >"$log" 2>&1 &
demo=$!
trap 'kill "$demo" 2>/dev/null || true; wait "$demo" 2>/dev/null || true; rm -f "$log"' EXIT

for _ in $(seq 1 180); do
  grep -q "sfdemo: ready" "$log" && break
  if ! kill -0 "$demo" 2>/dev/null; then cat "$log"; exit 1; fi
  sleep 1
done
grep -q "sfdemo: ready" "$log" || { cat "$log"; echo "sfdemo did not start"; exit 1; }

docker run --rm -i --network host \
  -e STORE_PORT="$port" -e RATE="${RATE:-50}" -e DURATION="${DURATION:-60s}" \
  grafana/k6:1.8.2 run - < loadtest/orders.js
