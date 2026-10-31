# Module: identity

The identity module owns **accounts, sessions, store memberships and the
audit log**. It authenticates every API request and decides what the caller
may do in a store. Design documents:

- [ADR-0005](../adr/0005-authentication.md): authentication;
- [ADR-0006](../adr/0006-authorization.md): authorization;
- [ADR-0007](../adr/0007-cross-module-tx-tenant-jobs.md): cross-module transactions, tenant-scoped jobs and user-scoped RLS;
- [ADR-0008](../adr/0008-session-lookup-in-postgres.md): session lookup.

It covers P1 parts 3a and 3b. Code and email delivery are described in
[ADR-0009](../adr/0009-notifications-and-invitations.md).

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

## Exported helpers

- `NewAuditRecorder` writes the audit log for other modules.
- `CountStaff(ctx, tx)` counts the store's seats (members and open
  invitations) in its tenant transaction: the usage meter (ADR-0061).
- `StaffEmails(db, roles…)` lists the store's members' emails for those
  roles; messaging uses it to email quota warnings to the owner and
  managers.

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
| catalog.read | ✓ | ✓ | ✓ | ✓ |
| catalog.manage | ✓ | ✓ | | |
| inventory.manage | ✓ | ✓ | | ✓ |
| imports.manage | ✓ | ✓ | | |
| storefront.manage | ✓ | ✓ | | |
| messaging.read | ✓ | ✓ | ✓ | |
| messaging.manage | ✓ | ✓ | | |
| promotions.manage | ✓ | ✓ | | |
| marketing.manage | ✓ | ✓ | | |

A store has exactly one owner, enforced by a partial unique index.

## Sign-up and login

### Sign-up

`POST /v1/auth/signup` does everything in one tenant transaction:

- creates the user;
- creates the store and its `<slug>.iftahonline.com` subdomain (through
  `tenancy.CreateStoreTx`);
- adds the owner membership;
- opens the store's setup wizard (through `onboarding.Starter.StartTx`,
  [ADR-0034](../adr/0034-setup-wizard-and-starter-templates.md));
- opens a session;
- writes the `store.created` audit entry.

If any step fails, nothing remains. Store field errors are reported as
`store.<field>`. Sign-up is limited to 5 per hour per IP.

### Store address check

`GET /v1/auth/slug-availability?slug=` answers the sign-up form before
the account exists: `{slug, available, reason}`, the slug normalized as
sign-up saves it, `reason` one of `invalid`, `reserved`, `taken`. It asks
`tenancy.Service.SlugAvailability`, which reads the hosts directly (not
through the resolver's caches). Public, and limited to 60 per 10 minutes
per IP. It is advisory: sign-up checks again (422 `store.slug` `taken`).

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

### Support sessions (impersonation, ADR-0055)

- **What it is:** a platform admin's session as a store's owner. It is
  marked in `user_sessions`:
  - `impersonator_id`;
  - `scope_store_id`;
  - `read_only`.
- **How it opens:** `Module.Impersonate` opens it from the console. The
  dashboard then exchanges the console's one-time code (60 s, in Redis,
  sealed) at `POST /v1/auth/impersonation`.
- **Bounds:**
  - It lasts 5 to 60 minutes and is never extended.
  - It is read-only by default; a change answers 403.
  - Any other store answers 404.
  - Outside its store it may only read and use `/v1/auth/*`.
- **`/v1/me`:** lists only its store and adds `impersonation`, with
  `read_only` and `expires_at`.
- **Audit:** in the store's log:
  - `impersonation.started` when it opens;
  - `impersonation.request` for every request, with the method, the
    route and `refused`;
  - `impersonated_by` added to every other entry written during it.

## Second factor (TOTP)

**Enrolment:**

1. `POST /v1/me/mfa/totp` returns a seed: base32 plus an `otpauth://` URI,
   which the dashboard shows as a QR code. The seed is sealed with
   `secretbox` (AAD `totp:<user>`) and is not active yet.
2. `POST /v1/me/mfa/totp/confirm` with a valid code enables 2FA and returns
   **10 recovery codes**, shown once and stored as SHA-256.

**Codes:**

- Codes follow RFC 6238 (SHA-1, 6 digits, 30 s), with ±1 step for clock
  drift.
- Each step can be used once, enforced by an atomic `totp_last_step`
  update. A replayed code fails, even within its 30 seconds.

**Sign-in:**

- With 2FA on, password login (and phone login) returns **202
  `mfa_required`** with a `challenge_id` (5 min) and no session.
- `POST /v1/auth/mfa` completes sign-in with a TOTP code or a recovery
  code. Recovery codes are one-time.
- Wrong second factors are limited per user. A challenge allows 5
  attempts.

**Disabling:** `POST /v1/me/mfa/totp/disable` needs the password **and** a
current code, so a stolen session alone cannot remove 2FA.

## Phone

### Verifying a number

1. `POST /v1/me/phone` sends a code.
2. `POST /v1/me/phone/verify` confirms it.

Only verified numbers are stored, and a number belongs to one account.

### Signing in

- `POST /v1/auth/otp/start` always answers 202 with a challenge. Unknown
  numbers get a decoy and nothing is sent, so accounts cannot be
  enumerated.
- `POST /v1/auth/otp/verify` completes the sign-in. It returns the MFA
  challenge instead of a session when 2FA is on.

### Codes

- 6 digits, uniformly random, valid 5 minutes, 5 attempts per challenge
  (an atomic counter).
- Only `HMAC(OTP_PEPPER, purpose, challenge, code)` is stored, so a code
  never works for another purpose.

### Limits

These protect against SMS pumping:

- per number: 1 per minute, 10 per day;
- per IP: 10 per hour.

Rate-limit keys use an HMAC of the number, so Redis key names hold no
phone numbers.

### Availability

Phone OTP returns 503 in production until an SMS or WhatsApp provider is
configured (ADR-0009). In development, codes appear in Mailpit as email to
`<digits>@sms.invalid`.

## New-device alerts

A device is identified by:

- the `__Host-did` cookie (browser), set on routes that can open a
  session, or
- the `X-Device-ID` header (app).

`user_devices` keeps only its SHA-256. A user's first device does not
alert. Any later unknown device enqueues `identity.device.alert` in the
session's transaction. The alert email gives the time, User-Agent and
network prefix, never the full IP, and links to the security settings.

## Team: invitations and members

| Operation | Permission |
|---|---|
| `GET /members` (one joined query, no N+1) | `members.read` |
| `PATCH /members/{user}` role, `DELETE /members/{user}` | `members.manage`. The owner can be neither changed nor removed |
| `GET/POST /invitations`, `DELETE /invitations/{id}` | `members.manage` |

### Invitations

- An invitation grants manager, support or shipping, never owner.
- It is valid for 7 days.
- There is one open invitation per email per store, and an existing
  member cannot be invited.
- The link is `DASHBOARD_URL/invite#<store ULID>.<secret>`. Only
  SHA-256(token) is stored. The token waits sealed in Redis until the
  email job sends it.

### Accepting

`POST /v1/invitations/accept` works in one of two ways:

- **The invited email has an account:** the caller must be signed in as
  it. Signed out, the answer is 401; as another account, 403.
- **No account yet:** `name` and `password` create one, and the
  membership and a session are created in the same transaction.

### Removal

Removal is immediate: the member's next request to the store gets 404.
These actions are audited: `member.invited`, `invitation.revoked`,
`member.joined`, `member.role_changed` and `member.removed`.

### Plan limit (ADR-0046)

An invitation passes `entitlements.AdmitTx(staff)`:
- seats are members (the owner included) and open invitations;
- past the plan's grant it is a 403 with code `plan_limit`;
- revoking an invitation gives its seat back.

## Tables

| Table | Scope | Notes |
|---|---|---|
| `users` | Platform-wide | Email is citext and unique. Status: active or disabled. Disabling a user kills their sessions at once |
| `user_sessions` | Platform-wide | `token_hash` is unique; there is an index on `(user_id, created_at)` |
| `store_memberships` | Tenant (RLS) | PK `(store_id, user_id)`, index on `user_id`, one owner per store. `member_self_read` lets a user read their own rows across stores |
| `audit_log` | Tenant (RLS) | Partitioned by month and append-only (see below) |
| `users` (3b) | Platform-wide | `phone` (unique, E.164, verified only), `totp_secret` (sealed), `totp_enabled_at`, `totp_last_step` |
| `user_recovery_codes` | Platform-wide | SHA-256, one-time (`used_at`) |
| `user_devices` | Platform-wide | SHA-256 of the device ID, last seen, UA, IP prefix |
| `store_invitations` | Tenant (RLS) | `token_hash` unique, one open invitation per email per store |

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
- Each partition is built as a standalone table and then attached with
  `ATTACH PARTITION`, which never blocks inserts (migration 00013).
  `CREATE TABLE … PARTITION OF` deadlocked with concurrent sign-ups;
  `TestPartitionCreationDoesNotDeadlockWriters` guards against it.

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
