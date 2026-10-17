# Module template

Every business module (catalog, orders, payments, …) follows this layout
(ADR-0001). Copy it when you create a module, and add
`docs/modules/<name>.md` describing the module's purpose, its public API,
the events it emits and consumes, and the tables it owns.

```
internal/modules/<name>/
  api.go            the ONLY exported surface: interfaces + DTOs + event types
  module.go         New(deps...) *Module — explicit wiring, no globals/init()
  internal/
    domain/         entities, value objects, rules. stdlib + internal/platform only
    app/            use cases: orchestrate domain + store, emit events/jobs
    store/          sqlc queries (db/queries/<name>/) and repository adapters
    httpapi/        oapi-codegen strict handlers for the module's OpenAPI tag
```

## Rules that the toolchain enforces

| Rule | Enforced by |
|------|-------------|
| No imports of another module's `internal/` | Go compiler (`internal/` rule); `make gates` proves it |
| `domain/` imports no `net/http`, `database/sql`, pgx, redis, river | depguard (`.golangci.yml`); `make gates` |
| `internal/platform` never imports modules | depguard |
| No duplicated blocks (100+ tokens) | dupl; `make gates` |
| Coverage ≥ 80% for `domain` and `app` | `make coverage` (covercheck) |
| SQL only through sqlc, every query prepared by PostgreSQL | `make sqlc-vet` |
| Contract-first API, no breaking changes in /v1 | `make gen-check`, `make openapi-diff` |

## Checklist for a new module

1. Add the module's tag and paths to `api/openapi.yaml`, plus an
   `api/oapi-codegen.<name>.yaml` that uses `include-tags: [<name>]` and
   writes to `internal/modules/<name>/internal/httpapi`. Add it to `make gen`.
2. Write migrations in `db/migrations/` (goose, expand → migrate → contract).
   Tenant tables need:
   - `store_id uuid NOT NULL REFERENCES stores(id)`;
   - an index whose leading column is `store_id`;
   - `ENABLE` and `FORCE ROW LEVEL SECURITY`;
   - the `tenant_isolation` policy (ADR-0002).
3. Add a sqlc package under `sqlc.yaml` for `db/queries/<name>/`. Every
   tenant query takes `store_id`, and the value comes from
   `tenant.FromContext` (via `postgres.InTenantTx`).
4. Domain and app logic come test-first: table-driven tests, plus fuzz
   tests for any parser of external input.
5. Handlers come next. Authorize each one through the module Authorizer,
   map errors only through `internal/platform/errs` (the `problem` package
   renders them), and register them in `cmd/platform` via `server.Deps.Mount`.
6. Give every endpoint these tests:
   - happy path;
   - validation;
   - authorization;
   - cross-tenant (store A gets 404 for store B's resource).
7. Side effects become River jobs inserted with `InsertTx` in the same
   transaction (ADR-0003). Job args carry `store_id`.
8. Write `docs/modules/<name>.md` and an ADR for any architectural decision.
