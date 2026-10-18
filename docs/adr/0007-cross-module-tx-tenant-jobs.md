# ADR-0007: Cross-module transactions, tenant-scoped jobs, user-scoped RLS

- Status: Accepted
- Date: 2026-10-18

## Context

Sign-up must create a user (identity), a store and its default subdomain
(tenancy), and the owner membership (identity) atomically. Modules must
not touch each other's tables (CLAUDE.md rule 2). Jobs run outside any
HTTP request, so they need the tenant context to be rebuilt, and
enqueueing a job must not become a way to act on another store. Finally,
"which stores am I a member of?" crosses tenants by nature, and it must
work without weakening RLS.

## Decision

1. **Transactions that span modules.**
   - A module that must join a caller's transaction exposes an `api.go`
     method with a `Tx` suffix that takes `pgx.Tx`, for example
     `tenancy.CreateStoreTx(ctx, tx, …)`.
   - The calling module owns the transaction. The callee runs only its
     own sqlc queries on it.
   - Dependencies point one way: identity → tenancy.
2. **Sign-up runs as the new store.**
   - The store's ULID is generated before the transaction starts.
   - Sign-up runs inside `postgres.InTenantTx` with that ID, so every
     tenant row (the store, its domain, the membership, the audit entry)
     passes RLS `WITH CHECK`.
   - `users` is a global table: identity is platform-wide, and a user may
     belong to many stores.
3. **Jobs declare their scope** (`internal/platform/jobs`):
   - **`TenantArgs`** (`store_id`):
     - The insert middleware rejects a zero store, and also a store that
       differs from `tenant.FromContext`. So a job can only be enqueued
       for the store whose request or job is running.
     - The worker middleware restores the store into the job's context.
     - A job with a malformed store ID is cancelled, not retried.
   - **`GlobalArgs`:** the job runs with no tenant. Any tenant-scoped
     code it reaches fails with `tenant.ErrNoTenant`.
   - **Args with neither** are rejected at insert. The JSON key
     `store_id` is reserved for this purpose.
4. **User-scoped reads.**
   - `app.current_user_id()` reads `app.user_id`, set with `SET LOCAL`
     semantics, just like `app.store_id`.
   - `store_memberships` has one extra permissive policy, `FOR SELECT`
     only: `USING (user_id = app.current_user_id())`.
   - A user can list their own memberships across stores. Nobody can
     write through this policy or see other users' rows.
   - The RLS catalog test allow-lists this policy by name. Any other
     permissive policy that applies to `app_user` fails CI.
5. **Cross-tenant platform reads use a separate role.**
   - Super-admin reads that span stores use the `app_admin` database role
     and a dedicated pool, never `app_user`.
   - `app_admin` gets explicit per-table policies.
   - Like the other roles, it is created by infrastructure, not by
     migrations (ADR-0002).

## Consequences

- **Positive:**
  - Sign-up is atomic without any module reading another's tables.
  - Jobs obey the same two walls as HTTP handlers.
  - "My stores" needs no RLS bypass.
- **Negative:**
  - `…Tx` methods couple the callee to pgx. We accept this, because pgx
    is our only database driver.
- **Operations:** production must create `app_admin`, as the initdb script
  does in development.
