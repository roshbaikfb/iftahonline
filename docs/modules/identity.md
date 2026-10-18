# Module: identity

The identity module owns **accounts, sessions, store memberships and the
audit log**. It authenticates every API request and decides what the caller
may do in a store. Design documents:

- [ADR-0005](../adr/0005-authentication.md): authentication;
- [ADR-0006](../adr/0006-authorization.md): authorization;
- [ADR-0007](../adr/0007-cross-module-tx-tenant-jobs.md): cross-module transactions, tenant-scoped jobs and user-scoped RLS;
- [ADR-0008](../adr/0008-session-lookup-in-postgres.md): session lookup.

This document covers **P1 part 3a**. Phone OTP, TOTP 2FA, new-device
alerts and invitations come in part 3b.

## How other modules use it

Other modules never import identity. They receive three things through
their constructors, and `cmd/platform` wires them:

| From identity | Platform type | Used for |
|---|---|---|
| `Module.Scope()` | `func(http.Handler) http.Handler` | Middleware in front of every API route |
| `Module.Authorizer()` | `authz.Authorizer` | `Authorize(ctx, perm)` as the first line of every handler |
| `identity.NewAuditRecorder(ids)` | `audit.Recorder` | `RecordTx(ctx, tx, entry)` inside the module's own transaction |

The recorder needs only an ID generator, so tenancy can be built before
identity. Identity's sign-up depends on tenancy, not the other way around.

## Request flow (`Scope`)

1. **Device:** User-Agent and client IP (from the server's RealIP) are
   kept in context for sessions and the audit log.
2. **Credentials:** the `__Host-sid` cookie (dashboard) or
   `Authorization: Bearer` (mobile app).
3. **CSRF:** a cookie-authenticated `POST`, `PUT`, `PATCH` or `DELETE`
   must carry `Sec-Fetch-Site: same-origin`, or an `Origin` matching the
   host. Bearer tokens are exempt. A merchant's storefront subdomain counts
   as foreign.
4. **`/v1/stores/{store_id}/…` routes:**
   - no session: 401;
   - malformed ID, non-member, or unknown store: **404**;
   - otherwise one query sets the tenant and the principal (user, session,
     store, role).
5. **Other routes:** a valid session sets the identity. A stale cookie is
   ignored, so login still works with an old cookie.

## Roles and permissions

There is one matrix, `internal/domain/rbac.go`, and `TestMatrix` pins
every cell.

| Permission | owner | manager | support | shipping |
|---|:-:|:-:|:-:|:-:|
| store.read | ✓ | ✓ | ✓ | ✓ |
| store.update | ✓ | ✓ | | |
| domains.read | ✓ | ✓ | ✓ | |
| domains.manage | ✓ | ✓ | | |
| members.read | ✓ | ✓ | ✓ | |
| members.manage | ✓ | | | |
| audit.read | ✓ | ✓ | | |

A store has exactly one owner, enforced by a partial unique index.

## Sign-up and login

### Sign-up

`POST /v1/auth/signup` does everything in one tenant transaction:

- creates the user;
- creates the store and its `<slug>.iftahonline.com` subdomain (through
  `tenancy.CreateStoreTx`);
- adds the owner membership;
- opens a session;
- writes the `store.created` audit entry.

If any step fails, nothing remains. Store field errors are reported as
`store.<field>`. Sign-up is limited to 5 per hour per IP.

### Login

`POST /v1/auth/login` behaves the same for every kind of failure:

- An unknown email, a wrong password and a disabled account all get the
  same 401, after the same argon2id work (a dummy hash for unknown emails).
- Failures are counted per email: 5 per 15 min. When the budget is spent,
  the next attempts get **429 even with the right password** until it
  refills. A successful login resets the count.
- Attempts are also limited to 30 per 15 min per IP.
- If Redis is down, both limiters refuse with 503.

### Passwords

- Length is 10 to 128 characters, counted in characters, not bytes. There
  is a short list of common passwords, and a password must not contain the
  email's local part.
- Hashing is argon2id. The defaults are `ARGON2_MEMORY_KIB=19456` and
  `ARGON2_TIME=2`.
- `PASSWORD_HASH_CONCURRENCY` (default 8) bounds memory. With the
  defaults, peak memory is about 152 MiB.
- When the parameters are raised, old hashes are upgraded on the next
  successful login.
- One hash with the defaults takes about 15 ms
  (`BenchmarkHashDefault`).

### Sessions

- **Token:** 256 random bits. The database keeps only its SHA-256.
- **Lifetime:** a 7-day sliding idle timeout and a 30-day absolute cap.
  Activity is recorded at most once every 5 minutes.
- **Web:** the token is sent only in
  `__Host-sid; HttpOnly; Secure; SameSite=Lax; Path=/`.
- **Mobile:** send `"client": "mobile"`. The token comes back in the body
  and is used as a bearer token.
- **Revocation:** `GET /v1/me/sessions` lists the user's sessions, and
  `DELETE /v1/me/sessions/{id}` signs one out. Another user's session ID
  returns 404.
- **Privacy:** the database stores only the IP's /24 (IPv4) or /48 (IPv6)
  and a truncated User-Agent.

## Tables

| Table | Scope | Notes |
|---|---|---|
| `users` | Platform-wide | Email is citext and unique. Status: active or disabled. Disabling a user kills their sessions at once |
| `user_sessions` | Platform-wide | `token_hash` is unique; there is an index on `(user_id, created_at)` |
| `store_memberships` | Tenant (RLS) | PK `(store_id, user_id)`, index on `user_id`, one owner per store. `member_self_read` lets a user read their own rows across stores |
| `audit_log` | Tenant (RLS) | Partitioned by month and append-only (see below) |

`member_self_read` is `FOR SELECT` and applies only to the user's own
rows. It is the one extra permissive policy that the RLS catalog test
allows.

## Audit log

### Append-only

- `app_user` has no `UPDATE`, `DELETE` or `TRUNCATE` on the table or on
  any partition.
- A trigger, cloned to every partition, rejects updates and deletes even
  from the table owner.

### Partitions

- `app.ensure_audit_log_partitions(n)` creates the next months'
  partitions.
- It is `SECURITY DEFINER`, takes no table names from the caller, and gives
  each partition its own forced RLS and grant revokes, because querying a
  partition directly skips the parent's policies.
- The `identity.audit.partitions` job runs it daily and when the worker
  starts, keeping 3 months ahead.

### What an entry holds

Store, actor (from the principal, or empty for the system), action
(`resource.verb`), target, IP prefix, and metadata. Metadata never holds
secrets or personal data.

### Reading

`GET /v1/stores/{id}/audit-log` (permission `audit.read`) returns entries
newest first, keyset-paginated with `next_cursor`, up to 200 per page.

## Tests

| What | Test |
|---|---|
| Role × permission matrix | `TestMatrix` |
| Cross-tenant 404 with real sessions and RLS; `TestMain` fails if a store-scoped identity operation lacks one | `TestCrossTenant` |
| A raw query without a tenant sees 0 rows | `TestAuditLog` (`AssertNoRowsWithoutTenant`) |
| Membership lookup is a constant 4 statements | `TestAuthenticateInStore` |
| CSRF | `TestCSRF` |
| Lockout | `TestLoginLockout`, `TestLoginLockoutOverHTTP` |
| Real binary: sign up, then use the token, with no secrets logged | `TestAPISignupEndToEnd` |

There are also fuzz targets for email normalization, session tokens and
stored hashes.
