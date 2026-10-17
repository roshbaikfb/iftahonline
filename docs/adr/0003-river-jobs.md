# ADR-0003: River (Postgres-backed) for background jobs and the outbox

- Status: Accepted
- Date: 2026-10-17

## Context

Side effects such as messages, webhooks, search sync and image processing
must happen **if and only if** the state change that caused them commits.
Publishing to Redis or a broker after a commit loses events when the
process crashes. Publishing before the commit sends events for rolled-back
changes.

## Decision

1. **We use [River](https://riverqueue.com)** (`riverqueue/river` with the
   `riverpgxv5` driver). Jobs are rows in PostgreSQL.
2. **Transactional outbox.** A side effect is enqueued with
   `client.InsertTx(ctx, tx, args, opts)` in the **same** transaction as the
   state change, so commit or rollback is atomic for both.
3. **Workers** run in the `platform worker` role. Every job must:
   - be idempotent, using unique job options or idempotency keys toward
     providers;
   - set an explicit timeout;
   - use River's retry policy with exponential backoff.
4. **Job args carry `store_id`.** The worker sets the tenant context from
   it before calling module code, so the tenant rules from ADR-0002 still
   apply.
5. **Migrations.** River's schema is applied by **goose**, our only
   migration tool. The SQL is copied from `river migrate-get` for the
   pinned River version into `db/migrations/`. Each River upgrade must
   check `river migrate-get` for new versions and add a goose migration in
   the same PR. The worker's readiness check fails if River's tables are
   missing.

## Consequences

- **Positive:**
  - Exactly-once enqueue semantics with no extra infrastructure.
  - Jobs are visible and queryable with SQL.
  - Backups also cover the job queue.
- **Negative:**
  - Job throughput is limited by PostgreSQL. River handles thousands of
    jobs per second on modest hardware, which is well above our projected
    need.
  - Very high-volume streams such as analytics events should go to
    partitioned tables and be batch-processed, not one job per event.
- **Operations:** the job tables need autovacuum tuning, and completed jobs
  are cleaned up by River's built-in retention.
