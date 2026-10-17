# ADR-0002: Shared database, `store_id` on every tenant table, RLS as second wall

- Status: Accepted
- Date: 2026-10-17

## Context

Thousands of small and medium stores share the platform. Giving each tenant
its own database or schema does not scale operationally: we would have to
run 5,000 migrations, manage 5,000 connection pools, and face catalog bloat.
A shared schema is efficient, but a single missing `WHERE store_id = …`
clause leaks one merchant's data to another, and that is the worst possible
incident for a SaaS.

## Decision

1. **One PostgreSQL 16+ cluster and one schema.** Every tenant-owned table
   has `store_id uuid NOT NULL`, a foreign key to `stores`, and an index
   whose leading column is `store_id`.
2. **First wall (application).**
   - Every sqlc query on a tenant table takes `store_id` as a parameter.
   - That value comes only from `tenant.FromContext(ctx)`, which the HTTP
     tenant-resolution middleware sets (domain → store) or the job's
     arguments set.
   - A missing tenant is an error (`tenant.ErrNoTenant`), never a default.
3. **Second wall (database).**
   - Tenant tables have `ENABLE` and `FORCE ROW LEVEL SECURITY` with this
     policy:
     ```sql
     CREATE POLICY tenant_isolation ON <t>
       USING (store_id = app.current_store_id())
       WITH CHECK (store_id = app.current_store_id());
     ```
   - `app.current_store_id()` reads `current_setting('app.store_id', true)`.
     When the setting is unset it returns NULL, so no rows match.
   - `postgres.InTenantTx` runs
     `SELECT set_config('app.store_id', $1, true)` (that is, `SET LOCAL`) at
     the start of every tenant transaction. The setting therefore cannot
     leak across pooled connections.
4. **Roles.**
   - Migrations run as `app_owner`, which owns the tables.
   - Services connect as `app_user`. It is `NOBYPASSRLS`, is not the
     owner, and has only DML grants.
   - Platform-wide jobs that must cross tenants (billing, analytics) use a
     dedicated role with an explicit, audited policy, never `app_user`.
5. **IDs.** Public IDs are ULIDs, stored as `uuid` (16 bytes, sortable by
   time) and shown as Crockford base32. Sequential IDs are never exposed.
6. **Tests.** Every endpoint has a cross-tenant test: store A gets 404 for
   store B's resource. The RLS wall has its own integration test on a real
   PostgreSQL.

## Consequences

- **Positive:**
  - Defense in depth: a bug has to get past both walls to leak data.
  - One connection pool and one set of migrations.
  - Indexes led by `store_id` keep per-tenant queries fast.
- **Negative:**
  - Every tenant query must run inside a transaction, because `SET LOCAL`
    only applies to a transaction.
  - RLS adds a small planner cost.
  - Cross-tenant admin queries need a separate role.
- **Noisy neighbours:** handled with per-store rate limits, statement
  timeouts, and month-partitioned `orders`, `order_items` and `events`
  tables. A store that grows very large can later move to its own cluster,
  because routing is keyed by `store_id`.
