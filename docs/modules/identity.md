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
- `OwnerContactTx(ctx, tx)` reads the store's owner for the console's
  store page: name, email (and whether it is verified), phone, and their
  last use of the dashboard (support sessions left out). Shown to admins,
  never logged; `ok` is false for a store without an owner.
- `OwnerStores(ctx, db, email, phones)` lists the stores whose owner has
  exactly this email or one of these E.164 phones, through
  `app.owner_stores` (migration 00093, `app_admin` only, with an
  `owner_lookup` policy on `store_memberships` for the function's owner):
  store IDs for the console's search, never the contact (ADR-0079).
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

- **The dashboard** (7f-3a):
  - **`/impersonate`** reads the code from the fragment, wipes it from
    the address bar, exchanges it once, and opens the store.
  - **The banner.** Every screen of a support session shows an amber bar
    with read-only or write, the end time and "إنهاء الجلسة".

## Second factor (TOTP)

**Enrolment:**

1. `POST /v1/me/mfa/totp` returns a seed: base32, an `otpauth://` URI and
   `qr_svg`, the URI as a QR code drawn by the server (`platform/qr`, shared
   with the console's team invitations). The seed is sealed with
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

## The account page (D4)

«حسابي» (`/stores/{id}/account`; `/account` opens it in the first store,
and the new-device alert links there) holds the user's own account, the
same for every store:

- **Name:** `PATCH /v1/me` (1 to 100 characters). The email does not change.
- **Password:** `POST /v1/me/password` takes the current password and the
  new one. A wrong current password is 422 on the field `current_password`,
  and failures are throttled per user like logins, so a stolen session
  cannot guess it. The change signs out every other session, keeps this
  one, and emails the user (`identity.password.changed`).
- **Phone, 2FA and devices:** the existing `/v1/me/phone`,
  `/v1/me/mfa/totp*` and `/v1/me/sessions` endpoints. The page asks for a
  seed only while 2FA is off, shows the recovery codes once with copy and
  download, and names devices from their User-Agent ("Chrome على Windows").
- **Phone sign-in:** the login page has a «كود على الموبايل» tab
  (`/v1/auth/otp/*`). A number without `+` is read in the browser's
  country (by time zone), else Egypt. The page shows the number masked,
  and a new code can be asked for after a minute.
- **Support sessions** (ADR-0055) see the page but change nothing: Scope
  refuses account writes outside the store, and the page asks for no seed.

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
network prefix, never the full IP, and links to the account page
(`/account`).

## Email links (D3)

Two kinds of one-time link are emailed: resetting a forgotten password
and confirming an email address.

- **Storage:** `user_tokens` keeps only the SHA-256 of the 256-bit secret.
  The secret waits sealed in Redis until the `identity.link.email` job
  sends it, as with invitations. It travels in the URL fragment
  (`/reset-password#…`, `/verify-email#…`), so it never reaches a server
  log.
- **One at a time:** a new link of a purpose closes the user's older ones.
  A link works once. A reset link works for an hour, a confirmation link
  for two days.
- **Forgot:** `POST /v1/auth/password/forgot` answers 202 whether or not
  the email has an account. It is limited per IP and to one email a
  minute per address (`Limits.LinksPerEmail`).
- **Reset:** `POST /v1/auth/password/reset/check` returns the email of an
  open link without using it, so the page can show it.
  `POST /v1/auth/password/reset` uses the link, sets the password, and
  signs out every session. It also enqueues `identity.password.changed`,
  which emails the user. It then signs in like a login: an account with
  a second factor gets a 202 challenge, so a mailbox alone never bypasses
  two-step verification. If the new password is refused, the transaction
  rolls back and the link stays usable.
- **Confirm:** sign-up sends the first link in its transaction.
  `POST /v1/me/email/verification` sends a new one (nothing when already
  confirmed). `POST /v1/auth/email/verify` needs no session, because the
  link may open in another browser. It confirms only the address the link
  was sent to. An account created from an invitation is confirmed already,
  since the invitation reached that mailbox.
- **Dashboard:**
  - `/forgot-password` is linked from the login page.
  - `/reset-password` and `/verify-email` are where the links land.
  - A banner on every store screen asks for confirmation until the email
    is confirmed, with a resend button.
- Errors: a used, replaced or expired link is 404 `link_not_found`.

## Platform support on a store's team (C1, ADR-0095)

The console acts on a store's members through `admin.StoreSupport`. It is
adapted from the module's methods in `cmd/platform/storeteam.go`. Each
action takes the admin's ID and a reason. Each lands in the store's
activity log with `support_admin` in the metadata, after the console has
recorded it in its own log.

| Action | Module method | When it happens | Activity |
|---|---|---|---|
| The team | `SupportTeam` | — | — |
| Remove two-step verification | `RequestMFAReset` | 24 hours later (`SupportDelay`) | `support.mfa_reset_requested`, then `_done` or `_cancelled` |
| Cancel a waiting request | `CancelSupportRequest` | at once | `support.mfa_reset_cancelled` (`by: console`) |
| Disable / enable an account | `SetMemberDisabled` | at once, in every store | `member.disabled` / `member.enabled` |
| Sign out everywhere | `SignOutMember` | at once | `member.signed_out` |

- **The team** (`SupportTeam`) lists, in one transaction:
  - members, from one query (`ListTeam`): two-step state, account status,
    and last use of the dashboard (support sessions left out). The phone
    is masked by `MaskPhone`;
  - pending invitations;
  - requests open or closed in the last 7 days.
- **A request** (`support_requests`, RLS) refuses:
  - a non-member: 404 `member_not_found`. This also covers another
    store's account;
  - a member without two-step verification: 409 `mfa_not_enabled`;
  - a second open request: 409 `request_open`.

  In the same transaction it:
  - records a cancel link (`user_tokens`, purpose `support_cancel`) for
    the member, and one for the owner when that is someone else;
  - enqueues `identity.support.link_email` for each link;
  - schedules `identity.support.execute` at `effective_at`.
- **Carrying it out** (`identity.support.execute`, at `effective_at`):
  - It locks the request; a request cancelled meanwhile is left as it
    is. Run too early, it fails and River retries it.
  - It removes the TOTP seed and the recovery codes, signs the member out
    everywhere, and closes the request and its links.
  - It enqueues `identity.support.notice`, which emails the member.
- **The link page:** `/support-request#<secret>` on the dashboard. The
  secret is in the URL fragment.
  - `POST /v1/support-requests/check` answers the kind, the masked
    email, and when the request was made and takes effect. It also gives
    `member_name`, and `for_member`, which is false on the store owner's
    copy. The page then names the member instead of «your account».
  - `POST /v1/support-requests/cancel` cancels the request, closes every
    link it sent, and enqueues `identity.support.cancelled`. That job
    emails the admin who asked through `WorkerDeps.NotifyAdmin`, which is
    the console's mailer.
  - Neither needs a session. Both are limited per IP. A closed or unknown
    link is 404 `link_not_found`.
- **The emails** give times in the store's time zone, for example «الأحد
  8 نوفمبر 2:30 م بتوقيت مكة» (`platform/arabic.Moment`).
- **Other stores:** `app.member_stores` (SECURITY DEFINER, `app_admin`
  only) lists the stores an account works on. The console warns with it
  before disabling an account.

## Moving a store's ownership (C2, ADR-0095)

The old owner always stays on as a manager. The new owner must already be
an active member. A store has one owner at a time: `DemoteOwner` runs
before `PromoteToOwner` in the same transaction.

- **The owner, at once:** `POST /v1/stores/{store_id}/ownership` with
  `{user_id, password, code?}`.
  - It needs `members.manage` and the owner's role. A support session is
    refused.
  - The password is checked with the same per-user throttle as
    `ChangePassword` (`verifyPassword`). When the owner has two-step
    verification on, a TOTP or recovery code is needed too
    (`checkSecondFactor`).
  - The activity log records `store.ownership_transferred`.
  - Both people are emailed (`ownership_given`, `ownership_received`).
- **Platform support, in 24 hours:** `RequestOwnershipTransfer`, a
  `support_requests` row of kind `ownership_transfer` (`from_user_id` is
  the owner at that time).
  - Only the owner gets a cancel link. The new owner gets a heads-up
    (`ownership_transfer_asked`).
  - The link page names the new owner, and the button reads «ألغِ
    الطلب، لم أطلبه».
  - When the job runs, it moves the ownership. If the team changed
    meanwhile (the owner moved it already, or the new owner left or was
    disabled), it closes the request as
    `support.ownership_transfer_skipped`.

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

### The invitation page (D2)

The dashboard's `/invite` page (signed in or out) reads the link's
fragment and asks `POST /v1/invitations/preview` (public, token in the
body, throttled per IP like logins). The answer is the store's name, who
invited, the role, the invited email, the expiry, and `has_account`. A
used, expired, revoked or unknown link is 404 `invitation_not_found`.

The page then joins:
- a signed-in invitee as that email in one click;
- a new one by creating the account (the email is fixed);
- one with an account after signing in on the same URL.

Someone signed in as another email is told to sign out.

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
| `user_tokens` | Platform-wide | One-time email links (D3): `purpose` is password_reset or email_verify; `token_hash` unique; index on `(user_id, purpose)` |

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

The dashboard's «سجل النشاط» (D6, `/stores/{id}/activity`, for the owner)
reads 50 at a time, with «عرض الأقدم» for the next page.

- **Wording:** each action is said in Arabic as a verbal noun ("إلغاء
  الطلب"), so the sentence never assumes who made the change. Useful
  metadata is shown left-to-right: the domain, the gateway, the coupon
  code, the roles. Orders and products link to their pages.
- **Unknown actions:** an action the dashboard does not know yet reads
  «تغيير في المتجر» (`web/dashboard/src/lib/activity.ts`). A new audit
  action should get its sentence there.
- **Who:** the actor is named from today's team (`members`). Otherwise it
  reads «عضو سابق», «المتجر تلقائيًا» when there is no actor, or «فريق
  دعم المنصة» for entries from a support session.

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
