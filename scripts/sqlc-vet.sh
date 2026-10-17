#!/usr/bin/env bash
# Runs `sqlc vet` against a throwaway PostgreSQL migrated with the real
# migrations, so every query is prepared (sqlc/db-prepare) as app_user.
set -euo pipefail

SQLC="${SQLC:-$PWD/bin/sqlc}"
PLATFORM="${PLATFORM_BIN:-$PWD/bin/platform}"
IMAGE=postgres:16-alpine
name="iftah-sqlc-vet-$$"

cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker run -d --name "$name" -e POSTGRES_PASSWORD=postgres -p 127.0.0.1::5432 \
  -v "$PWD/deploy/compose/initdb:/docker-entrypoint-initdb.d:ro" "$IMAGE" >/dev/null
port="$(docker port "$name" 5432/tcp | head -1 | sed 's/.*://')"

for _ in $(seq 1 60); do
  if docker exec "$name" pg_isready -U postgres -d iftah -h 127.0.0.1 >/dev/null 2>&1; then break; fi
  sleep 0.5
done
docker exec "$name" pg_isready -U postgres -d iftah -h 127.0.0.1 >/dev/null

MIGRATE_DATABASE_URL="postgres://app_owner:app_owner@127.0.0.1:$port/iftah?sslmode=disable" \
  "$PLATFORM" migrate up >/dev/null
SQLC_VET_DATABASE_URL="postgres://app_user:app_user@127.0.0.1:$port/iftah?sslmode=disable" "$SQLC" vet
echo "sqlc-vet: all queries prepared and passed vet rules"
