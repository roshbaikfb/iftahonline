# ADR-0010: Super-admin console boundaries

- Status: Accepted
- Date: 2026-10-19

## Context

Platform operators must be able to find any store and suspend it, for
example on abuse or fraud reports. That is cross-tenant by nature, and a
console that can reach every store is the most valuable target in the
system. ADR-0005 §8 and ADR-0007 §5 set the outline: separate admins,
mandatory 2FA, its own host, and an `app_admin` database role. This
record fixes the remaining boundaries.

## Decision

1. **The console is a module of its own (`internal/modules/admin`).**
   - Admins are not merchant users. They have their own tables, sessions
     and audit log.
   - `app_user` is revoked from all three tables, so a bug in merchant
     code cannot read or forge admin data.
2. **Admins are created only from the CLI.**
   - `platform admin create --email … --name …` reads the password from
     stdin and prints the TOTP enrolment once.
   - No HTTP endpoint creates admins.
   - Admin passwords must be at least 14 characters.
3. **Login is one step:** password **and** current TOTP code together.
   - There is no password-only intermediate state to attack.
   - Each code works once.
   - Failures are limited per IP and per email (lockout).
4. **Sessions:**
   - A cookie: `__Host-asid; HttpOnly; Secure; SameSite=Strict`.
   - Same-origin is required for writes.
   - 30-minute idle timeout, 8-hour cap.
5. **Host gating.** Every `/admin/v1` route, login included, answers
   **404 on any host but `ADMIN_HOST`**. On store and API hosts the
   console is indistinguishable from an unknown route.
6. **Cross-tenant reads stay inside tenancy.**
   - The tenancy module owns the directory queries (`Directory`: search,
     store detail) and runs them only on the `app_admin` pool.
   - `app_admin` can read `stores` and `store_domains` through
     `admin_read` policies, and nothing else of tenant data: no orders, no
     users, no audit logs.
7. **Writes go through the owning module, as that store.** Suspending or
   reactivating calls `tenancy.Service.SetStatus` inside the target
   store's tenant transaction, as `app_user`. RLS, the cache invalidation
   jobs and the store's own audit entry (system actor) all apply exactly
   as for any other change.
8. **Audit first.**
   - The admin entry (who, why) and the store change live behind different
     roles, so they cannot share a transaction.
   - The admin entry is written **before** the change. A failure then
     leaves a recorded attempt, never an unrecorded change.
   - `admin_audit_log` is append-only, enforced by a trigger, even for the
     table owner.

## Consequences

- **Positive:**
  - Compromising merchant code gives no admin power.
  - Compromising the console role gives read access to the store directory
    only, with every write going through tenancy's normal path.
- **Negative:**
  - An admin action produces two log entries, in the admin log and the
    store log, that are not atomic with each other. The audit-first order
    keeps the admin log complete.
- **Not yet:**
  - adding admins from the console;
  - per-admin roles;
  - IP allow-listing (recommended at the edge for `ADMIN_HOST`).
