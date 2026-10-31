# Module: admin (super-admin console)

Platform operators use this console to find any store and to suspend or
reactivate it. Design: [ADR-0010](../adr/0010-super-admin-console.md),
which builds on ADR-0005 §8 and ADR-0007 §5.

## Surface

| Operation | Notes |
|---|---|
| `POST /admin/v1/auth/login` | Email, password **and** TOTP code in one request. Sets `__Host-asid` (SameSite=Strict) |
| `POST /admin/v1/auth/logout` | |
| `GET /admin/v1/me` | |
| `GET /admin/v1/stores?query=&status=&cursor=&limit=` | Slug prefix or name substring; newest first; keyset paging (at most 100 per page) |
| `GET /admin/v1/stores/{id}` | The store and its domains |
| `POST /admin/v1/stores/{id}/suspend` / `reactivate` | `reason` required (3 to 500 characters). 409 if the store is in the wrong state |
| `POST /admin/v1/stores/{id}/impersonate` | Support session as the store's owner (ADR-0055). Requires a `reason`. `minutes` is 5 to 60 (default 30). `write` defaults to false, so the session is read-only. Answers with a one-time code and the dashboard URL that exchanges it. Recorded here (`store.impersonated`) before identity opens anything |
| `GET /admin/v1/features`, `PUT /admin/v1/features/{key}` | The features catalog (ADR-0056). Saving adds a key or renames one; the kind is fixed. `enforced` says whether the code checks it |
| `GET /admin/v1/plans`, `POST /admin/v1/plans` | Every plan version. Publishing puts the next version on sale; stores renew into it |
| `POST /admin/v1/plans/{code}/retire` | Off sale; never the free plan |
| `GET /admin/v1/stores/{id}/entitlements` | The store's plan grants, its overrides and the effective grants (ADR-0057) |
| `PUT /admin/v1/stores/{id}/entitlements/{feature}`, `POST …/{feature}/clear` | Per-store feature flags: a grant instead of the plan's, optionally until a date; only enforced features. Recorded here first (`store.feature_overridden`, `store.feature_override_cleared`) |
| `GET /admin/v1/health` | Database and Redis checks, and counts: stores by state, subscriptions by status, open and overdue invoices, the job queue (ADR-0058). A failed section is absent and named in `errors`; the page still answers |
| `GET /admin/v1/stores/{id}/billing` | The store's billing page: plan, status, period, card, open invoices |
| `POST /admin/v1/stores/{id}/invoices/{invoice}/settle` | A confirmed transfer: `reference` and `reason`. 404 unless it is an open invoice of the store's account. Recorded here first (`invoice.settled`) |
| `GET /admin/v1/agencies`, `POST /admin/v1/agencies`, `GET /admin/v1/agencies/{id}` | Agency accounts (ADR-0059): name, currency and agency plan; the view lists its store IDs. Creation is recorded once it has an ID (`agency.created`) |
| `POST /admin/v1/agencies/{id}/stores/{store_id}`, `POST …/{store_id}/remove` | A store joins (from no account, or from its own once that owes nothing: 409 `open_invoice`; the currency must match) or leaves onto the free plan. Recorded first (`store.agency_joined`, `store.agency_left`) |
| `GET /admin/v1/ads`, `PUT /admin/v1/ads/settings` | Ads in free stores (ADR-0062): AdSense's settings and every banner with its last 30 days' clicks. Settings are validated, then recorded first (`ads.settings_saved`) |
| `POST /admin/v1/ads/uploads`, `POST /admin/v1/ads/banners`, `PUT /admin/v1/ads/banners/{id}/active` | A banner's image upload (2 MiB, JPEG, PNG or WebP), the banner (re-encoded, recorded once saved: `ads.banner_created`), and show or stop (recorded first: `ads.banner_active_set`) |
| `GET /admin/v1/audit-log` | Every admin action, newest first |

Every route answers **404 unless `Host` is `ADMIN_HOST`** (default
`admin.<PLATFORM_BASE_DOMAIN>`). Requests that change state must be
same-origin.

## The console's page (7f-3b)

The UI is `web/dashboard/admin.html`, a second page of the dashboard's
build, served under `/console` on `ADMIN_HOST` (see `deploy/README.md`).
It follows the approved mockups, with a dark sidebar and the «أدمن» badge.

| Screen | Path | What |
|---|---|---|
| Login | — | Email, password and authenticator code in one step |
| Health and stores | `/console/` | The checks and counts (a section that could not be read is named), and the directory with search and a status filter |
| A store | `/console/stores/{id}` | Support session, suspend or reactivate, billing with "settle as a transfer", join an agency, per-store feature flags |
| Plans and features | `/console/plans` | Versions per plan; an editor that publishes the next version from the one on sale; retire (not the free plan); the features catalog |
| Agencies | `/console/agencies`, `/console/agencies/{id}` | List, create, the stores (by name), take a store out |
| Ads | `/console/ads` | Ads in free stores (ADR-0062): each placement's live banners and AdSense unit, the publisher and the switch, `/ads.txt`'s line; our banners with their clicks of 30 days and state (active, scheduled, ended, stopped), stop or show, and a new banner (the image is uploaded, then re-encoded) |
| Admin log | `/console/audit` | Newest first, the reason first, paged |

Every change asks for a reason.

## How it is wired (`cmd/platform`)

- **Enabling it:** the api role serves the console only when
  `ADMIN_DATABASE_URL` is set (the `app_admin` role).
- **Reading:** the console reads the store directory through
  `tenancy.Module.Directory()`, which runs on that pool.
- **Writing:** the console changes stores through `tenancy.Service`, as
  `app_user` inside the target store's tenant transaction.
- **Impersonation:** `Deps.Impersonate` is adapted from
  `identity.Module.Impersonate` in `cmd/platform/impersonate.go`.
  `Deps.DashboardURL` is `MAIL_DASHBOARD_URL`. The CLI has neither, and
  refuses with 503.
- **Plan catalog:** `Deps.Plans` is `billing.NewCatalog` on the admin
  pool, and `Deps.Enforced` is `entitlements.Enforced`. Catalog changes
  are recorded here first (`feature.saved`, `plan.published`,
  `plan.retired`), with the reason.
- **Per-store flags:** `Deps.Overrides` is `entitlements.Module.Overrides()`.
- **Billing:** `Deps.Billing` is `billing.Module.Console()` and
  `Deps.Agencies` is `billing.Module.Agencies()`. The API role's billing
  has the stores, the holds and the bus, which settling and agencies
  need.
- **Ads:** `Deps.Ads` is `ads.Module.Console()`, built with the admin
  pool and the media bucket.
- **Health:** the job counts come from `app.job_health()` (migration
  00065), granted to `app_admin`. It returns numbers only.
- **Shared platform packages:** `platform/password`, `platform/totp`,
  `platform/authtoken`, `platform/email` and `platform/clientinfo`. They
  were moved out of identity when the console became their second user.

## Tables (migration 00014)

| Table | Access |
|---|---|
| `platform_admins` | `app_admin` SELECT, INSERT, UPDATE; **no access for `app_user`**. The TOTP seed is sealed (AAD `admin-totp:<id>`) |
| `admin_sessions` | Same. Only SHA-256(token) is stored; 30-minute idle timeout, 8-hour cap |
| `admin_audit_log` | `app_admin` SELECT, INSERT; append-only through a trigger (even for the owner) |
| `stores`, `store_domains` | `app_admin` SELECT through the `admin_read` policies |

## Operations

### Creating an admin

```sh
read -rs PW && printf '%s\n' "$PW" | \
  ADMIN_DATABASE_URL=… SECRETBOX_KEYS=… platform admin create --email ops@iftahonline.com --name "Ops"
```

- The password comes from stdin (at least 14 characters).
- The TOTP secret and `otpauth://` URI are printed once. Add them to an
  authenticator app right away.

### Login protection

- Failures are limited to 5 per email per 30 minutes and 10 per IP per
  15 minutes.
- A code can be used once.

### Disabling an admin

```sql
UPDATE platform_admins SET status = 'disabled' WHERE email = '…';
```

Run this as `app_admin` or as the owner. Live sessions stop working on the
next request.

## Tests

| What | Test |
|---|---|
| Host gating | `TestOnlyOnAdminHost` |
| End-to-end flow, CSRF and logout | `TestConsoleFlow` |
| A merchant cookie or bearer token never opens the console | `TestConsoleRefusesWithoutAdminSession` |
| Replay, lockout and disabled admins | `TestLoginNeedsPasswordAndTOTP` |
| Both audit trails | `TestSuspendAndReactivate` |
| `app_admin` cannot write stores or read users, audit logs or sessions; `app_user` cannot read `platform_admins` | `TestAdminDirectory` |
| The CLI | `TestAdminCLI` |
| The real binary | `TestAPIServesConsoleOnlyOnAdminHost` |
