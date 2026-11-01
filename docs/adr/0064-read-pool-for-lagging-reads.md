# ADR-0064: A read pool for reads that may lag

- Status: Accepted
- Date: 2026-11-01

## Context

P8's profit dashboard runs heavy reads: weeks of daily figures per
product, campaign and governorate. The P8 acceptance criteria say
analytics queries use the read pool only, and a test must prove it.

Constraints:

- **No pool exists yet.** There is one primary pool per role. A replica
  is wired in P9 (deploy), but the code must be ready before that.
- **RLS stays on.** A replica serves `app_user` like the primary. A
  tenant transaction's `SET LOCAL app.store_id` is allowed on a hot
  standby, so ADR-0002's second wall holds there too.
- **A slow or missing replica must not take the API down.** Only the
  pages that read from it may fail.

## Decision

1. **Configuration (api role only):**
   - `DATABASE_READ_URL`: the replica, as `app_user`. Optional. When it
     is unset, the api reads `DATABASE_URL` through a separate pool, so
     analytics code always reads from a read pool.
   - `DB_READ_MAX_CONNS` (default 10) and `DB_READ_STATEMENT_TIMEOUT`
     (default 15 s). A separate pool also keeps dashboard reads from
     starving the primary pool's order traffic.
   - The startup log reports `read_replica: true|false`, never the URL.
2. **`postgres.ReadPool` is its own type**, not a `*pgxpool.Pool`:
   - Code that must read lagging data (analytics' store) takes a
     `*postgres.ReadPool`, so passing it the primary pool fails to
     compile.
   - `Begin` always starts a read-only transaction. Its sessions also
     default to `default_transaction_read_only=on`. A write fails with
     `25006 read_only_sql_transaction` (tested).
   - It satisfies `postgres.Beginner`, so `InTenantTx` works unchanged.
   - `postgres.ReadOnly(pool)` wraps a pool the caller built. Tests use
     it to trace their own pools, and its transactions are read-only too.
3. **Readiness does not depend on the read pool.** `/readyz` keeps
   checking the primary and Redis. A failing replica makes analytics
   endpoints answer 503 instead of draining the whole API.
4. **What goes to the read pool:** reads that may be seconds behind,
   i.e. analytics today. Writes, and reads made in order to write, stay on
   the primary. That includes the scheduled jobs that refresh analytics'
   aggregates.

## Consequences

- **Positive:**
  - Heavy reads can move to a replica with one environment variable,
    without a code change.
  - A write cannot reach the read pool by mistake. The type system and
    the database both refuse it.
- **Negative:**
  - **Replication lag.** Figures can be seconds or minutes behind the
    primary, depending on the replica. This is acceptable for aggregates
    that jobs refresh anyway.
  - **Without a replica, the primary has more connections:** the read
    pool's 10 on top of `DB_MAX_CONNS`. Size PgBouncer for both (P9).
