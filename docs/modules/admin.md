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
| `GET /admin/v1/growth` | The growth page (ADR-0081, `billing.read`): the last 12 Cairo months of collected revenue, stores and conversion, recurring revenue per day, churn, and plans today; a failed section is named in `errors` |
| `GET /admin/v1/metrics` | Monthly recurring revenue per currency and new stores (last 7 and 30 Cairo days, and per day) for the home (ADR-0075). Read on opening and on «تحديث الآن», not every minute. A failed section is absent and named in `errors` |
| `GET /admin/v1/search?q=` | One query across what the role reads (ADR-0079): stores (name, slug, domain), the stores of an owner by exact email or phone (contacts.read; logged masked as `admin.searched_owner`), invoices, payments by exact reference, agencies, admins; 5 of each, each source timed on its own (`failed`); `jump` for an exact invoice number, ID or domain |
| `GET /admin/v1/push`, `POST /admin/v1/push/subscriptions`, `POST /admin/v1/push/subscriptions/remove` | The admin's mobile notifications (ADR-0080): the public key and device count; turn a device on or off |
| `GET /admin/v1/alerts`, `PUT /admin/v1/alerts/email` | The alerts the role hears of (ADR-0080): open, and resolved in the last 30 days, each in words with its page; the admin's own email switch |
| `GET /admin/v1/audit-log` | The admin log, newest first: each entry with its admin's name and, for a store or its invoice, the store's (through the directory, one query a page). Filters: `admin`, `family` (store, billing, content, security: action prefixes), `store` (its actions and its invoices', indexed in migration 00089), `from` and `to` (Cairo days, `to` included) |
| `GET /admin/v1/admins` | The console's admins (name, email, active) for the log's filter; no secrets |
| `GET /admin/v1/stores/{id}/profile` | A store's owner and numbers, read by `cmd/platform`'s `storeProfiler` from identity (`OwnerContactTx`), orders (`ActivityTx`) and catalog (`CountProducts`) in one tenant transaction. The owner's contact is returned in full to admins and never logged |
| `GET /admin/v1/stores/{id}/billing` | The store's billing page: plan, status, period, card, open invoices |
| `GET /admin/v1/stores/{id}/balance` | The store's prepaid balance, its level and its latest 50 entries (ADR-0083) |
| `POST /admin/v1/stores/{id}/balance/credits` | Adds to the balance: `amount_minor`, `reason` (transfer, compensation, correction), `reference`, `note`. Recorded here first (`balance.credited`) |
| `POST /admin/v1/agencies/{id}/switch-to-pays` | An unpaid agency's stores onto pay as you sell, as agreed with it: `store_ids`, `collection` (balance, or the agency's card copied), `reason`. Its invoice is voided and the stores that stay are invoiced again (ADR-0087). Recorded here first (`agency.switched_to_pays`) |
| `GET /admin/v1/billing/payment-rules`, `PUT …/{country}`, `POST …/{country}/remove` | Payment rules by the merchant's country (ADR-0089): the billing gateway and the storefront's card gateways; ZZ is the others and stays. Changes need a reason, recorded first (`billing.payment_rule_saved`, `…_removed`) |
| `GET /admin/v1/billing/taxes`, `PUT …/{country}`, `POST …/{country}/remove` | The tax by country (0–50%), ZZ for the others. Recorded first (`billing.tax_saved`, `…_removed`) |
| `GET/PUT /admin/v1/billing/collection` | The collection settings: prepaid balance and card with a minimum, each on or off, and the minimum per currency (a currency left out keeps its own, ADR-0090). Saving needs a reason and is recorded first (`billing.collection_saved`) |
| `GET /admin/v1/billing/fx-rates`, `PUT …/{currency}` | Each store currency's rate to the dollar (ADR-0090), for fees of orders in another currency than their account's. Setting one needs a reason and is recorded first with the rate it replaces (`billing.fx_rate_saved`) |
| `POST /admin/v1/stores/{id}/invoices/{invoice}/settle` | A confirmed transfer: `reference` and `reason`. 404 unless it is an open invoice of the store's account. Recorded here first (`invoice.settled`) |
| `GET /admin/v1/agencies`, `POST /admin/v1/agencies`, `GET /admin/v1/agencies/{id}` | Agency accounts (ADR-0059): name, currency (EGP in Egypt, USD elsewhere, ADR-0090), country and agency plan; the view lists its store IDs. Creation is recorded once it has an ID (`agency.created`) |
| `POST /admin/v1/agencies/{id}/stores/{store_id}`, `POST …/{store_id}/remove` | A store joins (from no account, or from its own once that owes nothing: 409 `open_invoice`; the currency must match) or leaves onto the free plan. Recorded first (`store.agency_joined`, `store.agency_left`) |
| `GET /admin/v1/billing/summary` | The invoices page's cards (ADR-0076): open and overdue invoices per currency, collected since the Cairo month began (total with VAT and the VAT in it), the last 7 days' failed charges per outcome and reason |
| `GET /admin/v1/invoices`, `GET /admin/v1/invoices/{id}` | Every account's invoices, newest first, filtered by `status` (open, overdue, paid, void), `currency` and `q` (a number's start or a part of the name), keyset pages; one invoice with its lines and payment attempts. A store's account shows the store's name now (through the directory, one query a page) |
| `POST /admin/v1/invoices/{id}/settle` | Any account's open invoice, an agency's too, as a confirmed transfer: `reference` and `reason`. Recorded here first (`invoice.settled`, with the account and number, and the store only for a store's account) |
| `GET /admin/v1/invoices/export` | The list as CSV with the same filters (at most 50,000 rows, Cairo times, decimals, formula-like names defused). Recorded here first with the filters (`billing.invoices_exported`) |
| `GET /admin/v1/payments` | Every payment attempt, newest first, by `outcome` and `gateway`, keyset pages |
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
| A store | `/console/stores/{id}` | Header with the store's link; billing with "settle as a transfer"; its prepaid balance (level, the way it pays, the latest entries, «إضافة رصيد» with a reason, a reference and a note; none for an agency's store); its agency; a support session (read-only or with changes); its features with this store's exceptions (a switch for a flag, a number or «بلا حد» for a limit, until a date), set and removed from dialogs with a reason; suspend or reactivate in a closed «منطقة خطرة». The owner (name, email, phone with call and WhatsApp, last use of the dashboard; shown, never logged) and the store's numbers (orders in 30 days, last order, products), and its last five admin actions |
| Collection settings | `/console/collection` | The prepaid balance and the card with a minimum, each a switch, and the minimum per currency; saved with a reason. Read-only for a role without `billing.manage` (ADR-0083) |
| Plans and features | `/console/plans`, `/console/plans/{code}`, `/console/plans/new` | One card per plan as merchants see it (price in EGP with SAR and AED on hover, the annual saving, the first limits, trial, shown or hidden, version); the features catalog in its own tab (`?tab=features`), a new feature from a dialog. A plan's page: basics, prices or fees, its limits and switches for its features, a sticky bar that publishes the next version with the reason, its versions, and retiring it in a closed «منطقة خطرة» (not the free plan). Each card, the save bar, the versions and the danger zone say how many stores are on the plan (trialing, active or past due), from `app.plan_subscribers()` |
| Agencies | `/console/agencies`, `/console/agencies/{id}` | List, create, the stores (by name), take a store out; while the agency owes an invoice, a role with `billing.manage` switches chosen stores to pay as you sell (ADR-0087, mockup 8) |
| Ads | `/console/ads` | Ads in free stores (ADR-0062): each placement's live banners and AdSense unit, the publisher and the switch, `/ads.txt`'s line; our banners with their clicks of 30 days and state (active, scheduled, ended, stopped), stop or show, and a new banner (the image is uploaded, then re-encoded) |
| Invoices and payments | `/console/billing`, `/console/billing/invoices/{id}` | Under «الإيراد» (ADR-0076, the approved mockup). Four cards per currency, never added across currencies: open, overdue (opens the list filtered), collected this Cairo month with its VAT under it, and the last 7 days' failed payments with the most frequent reason. «الفواتير»: every account's invoices, narrowed by status, currency and a number or name (`?status=&currency=&q=`, kept in the address), each with its store or agency, period, total, due day, status and why it is late; «تصدير CSV» downloads the same filters. «المدفوعات» (`?tab=payments&outcome=&gateway=`): every attempt with its gateway, reference, outcome and reason; a duplicate waiting for a refund is highlighted with «تم الاسترداد». An invoice shows its lines and VAT, its attempts, and while open «سجّل كمدفوعة بتحويل» (an agency's too). On phones each row is a card. The home's overdue and duplicate cards link here |
| Team | `/console/team` | Under «الأمان», owners only (ADR-0078, the approved mockup). Every admin (you first, never with actions on your own row) with role, state and last sign-in, then the invites not accepted (how long they still work). «دعوة أدمن» asks a name, an email, a role (each with what it may do) and a reason, then shows the link once to copy. Change a role, disable, enable, reset the authenticator (a link shown once), renew or cancel an invite: each in a dialog with a reason. What each role may do, below. Phones get cards |
| Invite | `/console/invite#<token>` | The invitee's page, before any sign-in: who invited them and to which role, a password twice, the QR (an `<img>` of the server's SVG) with the key as text, and a code; then signed in. A bad link says only that it no longer works |
| Search | anywhere: Ctrl+K (⌘K), «/» outside a field, or «ابحث في كل شيء» atop the menu (the top bar on phones) | A palette (ADR-0079, the approved mockup): asks from two characters after a pause; groups by kind (the owner's stores first, then stores, agencies, invoices, payments, admins), each a link; ↑↓ and Enter, Esc closes; Enter on an exact invoice number, ID or domain opens it. Says when nothing matches (contacts match whole), notes that an owner lookup is logged masked, and names a kind that could not be read. Full screen on phones |
| Admin log | `/console/audit` | By day, newest first: each action in Arabic (an unknown key shows as it is, `admin/audit.ts`), its target as a link with the store's name, the reason on its own line, the details as chips, who («أنت» or the admin's name), the time and network. Narrowed by kind, admin, Cairo days and store (`?family=&admin=&from=&to=&store=`, kept in the address); a store's page shows its last five and links to the rest |

Every change asks for a reason.

## Countries in the console (G7, the approved mockups)

- **Collection settings** (`/collection`) has three tabs:
  - `?tab=ways`: the ways pay-as-you-sell fees are paid (ADR-0083).
  - `?tab=countries`: the payment rules and taxes by country (ADR-0089).
    Each table lists the countries by name, with the others' row (ZZ)
    last; ZZ can be changed but never removed. Each change asks for a
    reason.
  - `?tab=fx`: the exchange rates (ADR-0090). A currency without a rate
    is flagged: its fees wait.
  - `billing.manage` edits all three; other roles see them read-only.
- **A store's page** shows its country, and `stores.manage` changes it
  (ADR-0088). The dialog says what changes (card gateways for new
  connections, taxes on the next invoices) and what does not (the open
  billing account's currency, the store's currency, the connected
  gateway).
- **The plan editor** lists the pound and the dollar first, each with
  whom it bills. The riyal and the dirham, kept for older accounts, are
  folded under them (ADR-0090).

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

## Alerts (ADR-0080)

- **The check:** `admin.alerts_check` runs every 5 minutes in the worker,
  and only when the worker has `ADMIN_DATABASE_URL`.
- **Reads:** the health check (Redis, jobs, billing, stores) and the
  duplicate payments, into `domain.Signals`.
- **Streaks:** two bad checks open an alert and two clean ones resolve it
  (`admin_alert_state`, `admin_alerts`).
- **Email:**
  - Each active admin whose role covers the kind gets one email when the
    alert opens.
  - For a red alert, the same admins get one more when it resolves.
  - Admins with `alert_email` off get none.
  - Each email sent is recorded in `admin_alert_deliveries`. One that fails
    is tried again at the next check, and one not sent within a day is
    dropped.
- **Mobile notifications (Web Push):**
  - Any admin turns them on per device (`admin_push_subscriptions`,
    migration 00095); the same admins as the email are told, on the same
    events, recorded as channel `push`.
  - Endpoints are accepted and reached only on the push services' hosts
    (`pushEndpointOK`), through `platform/safehttp`.
  - Off without `VAPID_PUBLIC_KEY`/`VAPID_PRIVATE_KEY`; a device the push
    service no longer knows is forgotten.
- **In the console** (`web/dashboard/src/admin/AlertsPage.tsx`,
  `alerts.ts`):
  - `/console/alerts` shows the email switch and notifications on this
    device, then the open and resolved alerts.
  - The bell in the menu and the phone's top bar shows the open count.
  - The home's «يحتاج انتباهك» lists the same open alerts, or a red card
    when they cannot be read.
  - The service worker is `public/console-sw.js` (scope `/console`), with
    `console.webmanifest` and the icons for the iPhone's home screen.

## Growth (ADR-0081)

- **The page:** `GET /admin/v1/growth` (`billing.read`) covers the last
  12 Cairo months: collected revenue, stores and conversion, recurring
  revenue per day, churn per complete month, and plans today. Each
  section has its own timeout; a section that fails is named in `errors`.
- **The reading:** `admin.growth_snapshot` runs every hour in the
  worker. It keeps each day's latest recurring revenue per currency
  (`admin_growth_mrr`), and each month's first set of paying accounts
  (`admin_growth_paying`, `admin_growth_months`, migration 00096).
- **Churn:** a month counts only when it and the next were both read in
  their first `ChurnGraceDays` (3) days.
- **Sources:** billing (`Today`, `Collected`, `Converted`) and tenancy
  (`Directory.StoresByMonth`), through their interfaces only.
- **In the console:** `/console/growth` (`web/dashboard/src/admin/GrowthPage.tsx`,
  `growth.ts`), in the menu under الإيراد for `billing.read`. It shows
  one currency at a time, bars and a line without a chart library, and
  each chart's figures under «عرض الأرقام».

## Tables (migration 00014)

| Table | Access |
|---|---|
| `platform_admins` | `app_admin` SELECT, INSERT, UPDATE; **no access for `app_user`**. The TOTP seed is sealed (AAD `admin-totp:<id>`) |
| `admin_sessions` | Same. Only SHA-256(token) is stored; 30-minute idle timeout, 8-hour cap |
| `admin_audit_log` | `app_admin` SELECT, INSERT; append-only through a trigger (even for the owner) |
| `admin_invites` | `app_admin` SELECT, INSERT, UPDATE; none for `app_user` (00092). Only SHA-256(token); the TOTP seed sealed for the admin it makes; one open invite per admin and per new email; 72 hours |
| `stores`, `store_domains` | `app_admin` SELECT through the `admin_read` policies |

## Roles (ADR-0077)

| Role | May |
|---|---|
| owner | Everything, the team and the admin log |
| operations | Stores: suspend, reactivate, support sessions, overrides; a store owner's contact; reads billing and the catalog |
| finance | Invoices and payments: settle, refunds, CSV; plans, features, founding, agencies; reads stores |
| content | The site, guides and ads |
| viewer | Reads stores, billing, the catalog and content |

- **The table:** each operation's permission is in
  `internal/httpapi/permissions.go`. The `authorize` strict middleware
  refuses the rest with 403, and an operation missing from the table is
  refused for everyone. `TestEveryOperationHasAPermission` keeps the table
  complete.
- **Contacts:** a store owner's contact (`/profile`) is left out without
  `contacts.read`.
- **The console:** `/me` returns the role and its permissions. The menu,
  the routes (`Need`), the action buttons, and an editor's forms
  (`Editable`, disabled under «عرض فقط») follow them.
- **Changing a role:** from the team page (ADR-0078).

## The team (ADR-0078)

| Endpoint | What |
|---|---|
| `GET /admin/v1/team` | Every admin (role, active, resetting, last sign-in) and the open invites. `team.manage` |
| `POST /admin/v1/team/invites` | Invite an email, name and role with a reason; the token is returned once. 409 for an admin's email or an open invite's |
| `POST /admin/v1/team/invites/{id}/renew`, `…/cancel` | A new link (the old one stops working), or none |
| `PUT /admin/v1/team/admins/{id}/role`, `POST …/disable`, `…/enable`, `…/reset` | Another admin only (409 `not_yourself`); never the last active owner (409 `last_owner`, owners locked in the transaction). Disabling ends its sessions; a reset disables it and returns a link to set a new password and authenticator |
| `POST /admin/v1/invites/lookup`, `POST /admin/v1/invites/accept` | No session (Scope's public routes, throttled like sign-in). The invitee sees who invited them and the seed (text, `otpauth://`, a server-drawn SVG QR); accepting sets the password, checks a code and signs in. Any bad link is the same 404 |

- **Audit first:** every change is in the admin log first, with its reason.
  The invitee's `admin.joined` or `admin.reset_completed` comes from the
  new admin.
- **Email:** none yet. The console shows the link once to copy.

## A store's team (C1, ADR-0095)

The store page has a card listing the store's members and pending
invitations, with platform support's requests. The card reads
`GET /admin/v1/stores/{id}/team`. Identity answers through
`Deps.Support` (`StoreSupport`, adapted in `cmd/platform`).

| Operation | Permission |
|---|---|
| `GET …/team` | `stores.read`. Email and phone need `contacts.read` too; the phone is always masked. |
| `POST …/team/{user}/mfa-reset` | `stores.manage`. Takes effect after 24 hours. Answers 202 with the request. |
| `POST …/team/{user}/ownership` | `stores.manage`. C2: moves the store to that member after 24 hours, and only the owner gets the cancel link. Answers 202 with the request. |
| `POST …/team/{user}/disable`, `/enable`, `/sign-out` | `stores.manage`. Take effect at once. |
| `GET …/team/{user}/stores` | `stores.manage`. Lists the account's other stores, by name. |
| `POST /admin/v1/stores/{id}/support-requests/{req}/cancel` | `stores.manage` |

- Every action needs a reason. The reason is recorded in the admin log
  before identity is called: `store.member_mfa_reset_requested`,
  `store.member_disabled`, `store.member_enabled`,
  `store.member_signed_out`, `store.support_request_cancelled`.
- Each request names the admin who asked, with their role.
- When a merchant cancels from the emailed link, the worker emails that
  admin through `Module.NotifyAdmin`, with the console's link.

## Operations

### Creating an admin

```sh
read -rs PW && printf '%s\n' "$PW" | \
  ADMIN_DATABASE_URL=… SECRETBOX_KEYS=… platform admin create --email ops@iftahonline.com --name "Ops" [--role finance]
```

- The role is `owner` (the default), `operations`, `finance`, `content`
  or `viewer` (ADR-0077).

- The password comes from stdin (at least 14 characters).
- The TOTP secret and `otpauth://` URI are printed once. Add them to an
  authenticator app right away.

### Login protection

- Failures are limited to 5 per email per 30 minutes and 10 per IP per
  15 minutes.
- A code can be used once.

### Disabling an admin

From the team page (ADR-0078): sessions end at once. Without a console
owner, use SQL as `app_admin` or the owner:
`UPDATE platform_admins SET status = 'disabled' WHERE email = '…';`

## Tests

| What | Test |
|---|---|
| Host gating | `TestOnlyOnAdminHost` |
| End-to-end flow, CSRF and logout | `TestConsoleFlow` |
| A merchant cookie or bearer token never opens the console | `TestConsoleRefusesWithoutAdminSession` |
| Replay, lockout and disabled admins | `TestLoginNeedsPasswordAndTOTP` |
| Both audit trails | `TestSuspendAndReactivate` |
| `app_admin` cannot write stores or read users, audit logs or sessions; `app_user` cannot read `platform_admins` | `TestAdminDirectory` |
| Invoices and payments: names, an agency's invoice settled, logged first without a store; the CSV (filters, Cairo times, formula-like names defused) logged with its filters; over HTTP with validation and cross-site refusal | `TestConsoleInvoices`, `TestExportInvoices`, `TestConsoleInvoicesAndPayments` |
| Roles: every operation has a permission; each role over HTTP (403s, /me, the owner's contact) and in the console (menu, pages, buttons, read-only forms) | `TestEveryOperationHasAPermission`, `TestConsoleRoles`, `domain.TestRoles`, `console.test.tsx` «roles» |
| The team: invite, look up and accept (once, code claimed); renew, cancel and expire; role, disable (sessions end), enable, reset (locked out until the link); not yourself; the last owner; over HTTP with no session for the invitee | `TestInviteAdmin`, `TestRenewAndCancelInvite`, `TestExpiredInvite`, `TestTeamChanges`, `domain.TestLeavesAnOwner`, `TestConsoleTeam` |
| Search: per role, jumps, validation; the owner lookup (phone and email, masked in the log, never for finance, never for a name) | `TestSearch`, `TestSearchOwner`, `TestConsoleSearch` |
| Alerts: two checks each way, unknown sections, who is told once, a failed email retried, email off, amber resolutions silent, the role's list; over HTTP | `domain.TestStep`, `TestSignals`, `TestAlertAudience`, `TestAlerts`, `TestConsoleAlerts` |
| Push: only the push services' hosts, keys checked, off without keys, sent once per event to every device, a gone device forgotten; over HTTP | `TestPushEndpointOK`, `TestSubscribePush`, `TestAlerts`, `TestConsolePush` |
| The alerts page: open and resolved with their pages, the bell's count, the email switch (reverted when refused), notifications turned on with the server key and off, a refused permission, no button without keys, support or permission, the iPhone note; the home's cards from the server, unread alerts said | `alerts.test.tsx`, `home.test.tsx` |
| Collection: the settings' bounds and only the console's role saving them; a balance's credits adding up exactly under its lock, in order, another store seeing none; over HTTP by role, with validation and cross-site refusals; the screens (saving both ways and a minimum, read-only for the viewer, a credit to a debt, no credit for the viewer or an agency) | `domain.TestCollectionSettings`, `TestLevelOf`, `TestParseCredit`, `TestCollectionSettings`, `TestCreditBalance`, `TestConsoleCollection`, `collection.test.tsx` |
| Growth: the 12 Cairo months at a month's turn, empty months filled, churn's rate; the hourly reading (a day's latest, a month's first set), churn only for months read in their first days, every section read, those without billing named; over HTTP by role | `domain.TestGrowthWindow`, `TestFillMonths`, `TestChurnRate`, `TestGrowth`, `TestConsoleGrowth` |
| The growth page: this month's numbers, every chart, the currency switch (never converted), churn once complete, a section unread, nothing recorded yet, finance in and content out | `growth.test.tsx` |
| The CLI | `TestAdminCLI` |
| The real binary | `TestAPIServesConsoleOnlyOnAdminHost` |
