# Architecture Decision Records

Format: Context, Decision, Consequences (Michael Nygard style). A record is
never edited after acceptance except to change its status; a new ADR
supersedes it.

| # | Title | Status |
|---|-------|--------|
| [0001](0001-go-modular-monolith-linux.md) | Go modular monolith, one binary, on Linux | Accepted |
| [0002](0002-shared-db-store-id-rls.md) | Shared database, `store_id` on every tenant table, RLS as second wall | Accepted |
| [0003](0003-river-jobs.md) | River (Postgres-backed) for background jobs and the outbox | Accepted |
