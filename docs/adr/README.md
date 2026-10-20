# Architecture Decision Records

Format: Context, Decision, Consequences (Michael Nygard style). A record is
never edited after acceptance except to change its status; a new ADR
supersedes it.

| # | Title | Status |
|---|-------|--------|
| [0001](0001-go-modular-monolith-linux.md) | Go modular monolith, one binary, on Linux | Accepted |
| [0002](0002-shared-db-store-id-rls.md) | Shared database, `store_id` on every tenant table, RLS as second wall | Accepted |
| [0003](0003-river-jobs.md) | River (Postgres-backed) for background jobs and the outbox | Accepted |
| [0004](0004-host-resolution-cache.md) | Host → store resolution with a two-level cache | Accepted |
| [0005](0005-authentication.md) | Authentication: opaque sessions, argon2id, phone OTP, TOTP | Accepted |
| [0006](0006-authorization.md) | Authorization: roles, one permission matrix, 404 for non-members | Accepted |
| [0007](0007-cross-module-tx-tenant-jobs.md) | Cross-module transactions, tenant-scoped jobs, user-scoped RLS | Accepted |
| [0008](0008-session-lookup-in-postgres.md) | Session lookup in PostgreSQL, no Redis session cache (yet) | Accepted |
| [0009](0009-notifications-and-invitations.md) | Delivering codes and emails without a provider; invitation tokens | Accepted |
| [0010](0010-super-admin-console.md) | Super-admin console boundaries | Accepted |
| [0011](0011-catalog-and-stock-reservations.md) | Catalog data model and race-safe stock reservations | Accepted |
| [0012](0012-image-pipeline.md) | Product image pipeline: presigned uploads, re-encoded masters, signed imgproxy delivery | Accepted |
| [0013](0013-tenant-safe-search.md) | Tenant-safe product search with Arabic normalization | Accepted |
| [0014](0014-catalog-importers.md) | Catalog importers: CSV and WooCommerce as resumable jobs | Accepted |
| [0015](0015-domain-events-and-storefront-reads.md) | Domain events between modules; the catalog's storefront read side | Accepted |
