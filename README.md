# iftahonline

A multi-tenant e-commerce SaaS for Egypt and the Gulf: merchants build and
run their own stores. It is a Go modular monolith on Linux. Read
[CLAUDE.md](CLAUDE.md) (project rules) and [docs/adr](docs/adr) before
changing anything.

## Quick start

Requirements: Linux (amd64/arm64), Go (go.mod downloads the pinned
toolchain), Docker, and Node 20.19+ (22 LTS in CI).

```sh
make tools          # pinned golangci-lint, sqlc, gitleaks, trivy -> bin/; npm deps
make dev            # compose deps, migrate, run api (:18080) and storefront (:18081)
make ci             # the full pipeline; must pass before any task is "done"
```

The dashboard dev server runs with `cd web/dashboard && npm run dev`
(http://localhost:5173) and proxies `/v1` to the API.

The local service ports are offset to avoid clashes:

| Service | Address |
|---------|---------|
| PostgreSQL | localhost:15432 (db `iftah`) |
| Redis | localhost:16379 |
| Meilisearch | localhost:17700 |
| S3 (SeaweedFS) | localhost:18333, bucket `iftah-media` |
| Mailpit | SMTP localhost:11025, UI http://localhost:18025 |

## Layout

```
cmd/platform           one binary: api | storefront | worker | migrate | version
internal/modules       business modules (see docs/modules/TEMPLATE.md)
internal/platform      shared building blocks used by 2+ modules:
                       money, phone, ulid, tenant, event, errs, problem,
                       safehttp, retry, config, logging, otel, postgres, redisx
internal/server        HTTP wiring: router, middleware, probes, graceful shutdown
api/openapi.yaml       the API contract (edit first, then `make gen`)
db/migrations          goose SQL migrations (platform baseline + River)
db/queries             sqlc queries
web/dashboard          React + Vite + strict TypeScript, generated API client
mobile                 Flutter merchant app (later phase)
deploy                 docker-compose, Dockerfile, systemd units (deploy/README.md)
loadtest               k6 scripts
docs/adr               architecture decision records
```

## Quality gates (`make ci`)

| Gate | What it checks |
|------|----------------|
| Generated code | Generated code (oapi-codegen, sqlc, templ, TS client) is up to date. |
| Formatting and lint | gofmt and goimports; golangci-lint with staticcheck, gosec, errcheck, revive, dupl, depguard and others; go vet. |
| Tests | `go test -race` on real PostgreSQL 16 and Redis (testcontainers). |
| Coverage | Per-package coverage of at least 80% for domain/app and core platform packages. |
| Fuzzing | Every parser of external input (money, phone, ULID, URL). |
| Security scans | govulncheck; gitleaks over the full history. |
| API contract | oasdiff blocks breaking changes in /v1. |
| SQL | `sqlc vet` with every query prepared by a migrated PostgreSQL. |
| Architecture gates | `make gates` proves that a cross-module internal import fails to compile, a 40-line duplicate fails dupl, and a domain package importing net/http fails depguard. |
| systemd units | The units are valid and have an exposure score of at most 2.0. |
| Dashboard | ESLint (strict type-checked), tsc, vitest, and a Playwright smoke test against the real API binary. |
