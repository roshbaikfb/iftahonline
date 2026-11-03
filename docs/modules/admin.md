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
| `GET /admin/v1/metrics` | Monthly recurring revenue per currency and new stores (last 7 and 30 Cairo days, and per day) for the home (ADR-0075). Read on opening and on «تحديث الآن», not every minute. A failed section is absent and named in `errors` |
| `GET /admin/v1/stores/{id}/billing` | The store's billing page: plan, status, period, card, open invoices |
| `POST /admin/v1/stores/{id}/invoices/{invoice}/settle` | A confirmed transfer: `reference` and `reason`. 404 unless it is an open invoice of the store's account. Recorded here first (`invoice.settled`) |
| `GET /admin/v1/agencies`, `POST /admin/v1/agencies`, `GET /admin/v1/agencies/{id}` | Agency accounts (ADR-0059): name, currency and agency plan; the view lists its store IDs. Creation is recorded once it has an ID (`agency.created`) |
| `POST /admin/v1/agencies/{id}/stores/{store_id}`, `POST …/{store_id}/remove` | A store joins (from no account, or from its own once that owes nothing: 409 `open_invoice`; the currency must match) or leaves onto the free plan. Recorded first (`store.agency_joined`, `store.agency_left`) |
| `GET /admin/v1/billing/duplicates`, `POST …/{id}/refunded` | Payments of invoices already paid (ADR-0063), those to refund first; recording a refund takes the gateway's refund reference and a reason, recorded first (`payment.refunded`) |
| `GET /admin/v1/ads`, `PUT /admin/v1/ads/settings` | Ads in free stores (ADR-0062): AdSense's settings and every banner with its last 30 days' clicks. Settings are validated, then recorded first (`ads.settings_saved`) |
| `POST /admin/v1/ads/uploads`, `POST /admin/v1/ads/banners`, `PUT /admin/v1/ads/banners/{id}/active` | A banner's image upload (2 MiB, JPEG, PNG or WebP), the banner (re-encoded, recorded once saved: `ads.banner_created`), and show or stop (recorded first: `ads.banner_active_set`) |
| `GET /admin/v1/audit-log` | Every admin action, newest first |

Every route answers **404 unless `Host` is `ADMIN_HOST`** (default
`admin.<PLATFORM_BASE_DOMAIN>`). Requests that change state must be
same-origin.

## The console's page (7f-3b)

The UI is `web/dashboard/admin.html`, a second page of the dashboard's
build, served under `/console` on `ADMIN_HOST` (see `deploy/README.md`).
It follows the approved mockups, with a dark sidebar in titled groups and
the «أدمن» badge; on phones a top bar opens the sidebar as a drawer.

| Screen | Path | What |
|---|---|---|
| Login | — | Email, password and authenticator code in one step |
| Home | `/console/` | «يحتاج انتباهك» first: each problem (a check down, duplicate payments, failed or retrying or late jobs, an unread section, held stores, overdue invoices, past-due subscriptions) as one card, the urgent first, or «كل شيء يعمل». Then four numbers (active stores, trials, paying accounts, founding seats), the monthly recurring revenue per currency and the new stores with a bar per day, the systems in one line with every count behind «كل الأرقام», duplicate payments waiting for a refund (only while one waits), and the five newest stores. The health check is read again every minute while the tab is visible |
| Stores | `/console/stores` | The directory, newest first: status tabs with their counts, search by name or address; the tab and the search live in the URL. Cards instead of a table on phones |
| A store | `/console/stores/{id}` | Header with the store's link; billing with "settle as a transfer"; its agency; a support session (read-only or with changes); its features with this store's exceptions (a switch for a flag, a number or «بلا حد» for a limit, until a date), set and removed from dialogs with a reason; suspend or reactivate in a closed «منطقة خطرة». Dashed slots keep the places of the owner, the store's numbers and its admin history |
| Plans and features | `/console/plans`, `/console/plans/{code}`, `/console/plans/new` | One card per plan as merchants see it (price in EGP with SAR and AED on hover, the annual saving, the first limits, trial, shown or hidden, version); the features catalog in its own tab (`?tab=features`), a new feature from a dialog. A plan's page: basics, prices or fees, its limits and switches for its features, a sticky bar that publishes the next version with the reason, its versions, and retiring it in a closed «منطقة خطرة» (not the free plan). Each card, the save bar, the versions and the danger zone say how many stores are on the plan (trialing, active or past due), from `app.plan_subscribers()` |
| Agencies | `/console/agencies`, `/console/agencies/{id}` | List, create, the stores (by name), take a store out |
| Ads | `/console/ads` | Ads in free stores (ADR-0062): each placement's live banners and AdSense unit, the publisher and the switch, `/ads.txt`'s line; our banners with their clicks of 30 days and state (active, scheduled, ended, stopped), stop or show, and a new banner (the image is uploaded, then re-encoded) |
| Admin log | `/console/audit` | By day, newest first: each action in Arabic (an unknown key shows as it is, `admin/audit.ts`), its target as a link, the reason on its own line, the details as chips, who («أنت», another admin, the system), the time and network; paged |

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
