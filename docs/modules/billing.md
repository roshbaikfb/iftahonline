# Module: billing

The billing module owns **plans** and each store's **subscription**. It
is the only module that knows plans by name. Every other module asks
[entitlements](entitlements.md) what a store may do.

Design: [ADR-0045](../adr/0045-plans-as-data-and-one-entitlements-service.md)
(plans), [ADR-0048](../adr/0048-pay-as-you-sell-fees.md) (fees) and
[ADR-0049](../adr/0049-billing-accounts-and-monthly-invoices.md) (accounts
and invoices).

**Status (P7 7a):** plans, their grants and the subscription lifecycle
are in place.

| Coming | PR |
|---|---|
| Fees on delivered orders | 7c-1 (done) |
| Invoices, agency accounts | 7c-2 (done) |
| Payments and dunning | 7d (done) |
| The console's plan editor | 7e-2a (done) |
| The merchant's billing page | 7f-2 (done) |

## Public API (`internal/modules/billing/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Grants()` | Wiring; the `GrantSource` entitlements reads |
| `GrantSource.GrantsTx(ctx, tx)` | What the store in ctx is granted now, by feature key, read in its tenant transaction |
| `Grant`, `Grants` | `Value` (0/1 for a flag, a count, units a month) or `Unlimited`; a key not listed is not granted |

| `Fees.OrderMoved(ctx, order)` | Follows an order to its current status: a delivered order's fee (once), a returned order's fee cancelled or credited |
| `Orders`, `Order`, `OrderStatus` | The order reader fees need; `cmd/platform` adapts `orders.Fulfilment` (billing does not import orders) |
| `SellFeesSubscriber` | `billing.sell_fees`, subscribed to `orders.order.status_changed` in the worker |

| `Module.Console()`, `Console` | The console's view (ADR-0058): `Health` (counts through `app.billing_health()`), `Revenue` (monthly recurring revenue per currency through `app.billing_paying()` and `domain.MRR`, ADR-0075), `Today` (the same figure by currency and plan with each plan's Arabic name, with the paying accounts, `domain.Today`), `Collected` and `Converted` (paid invoices per Cairo month before VAT, and the stores with a paid subscription line, through `app.billing_collected()` and `app.billing_converted()`; the console's growth page, ADR-0081), `Summary` of the store in ctx, `SettleStoreInvoice` (a confirmed transfer; 404 unless it is an open invoice of the store's account). The invoices and payments page (ADR-0076): `Totals` (open, overdue, collected this Cairo month with its VAT, the last 7 days' failures, through `app.billing_summary()`), `Invoices` and `Payments` (every account's, newest first, filtered, keyset pages of at most `MaxListPage`, through `app.billing_invoices()` and `app.billing_attempts()`), `Invoice` (one with its lines, read in its account's transaction, and its attempts), `SettleInvoice` (any account's open invoice, an agency's too), `PaymentsByReference` (the gateway's exact reference, through `app.billing_attempts_by_reference()` and a partial index; the console's search, ADR-0079) |
| `Module.Agencies()`, `Agencies`, `Agency` | Agencies from the console (ADR-0059): create, list, view, `JoinAgency` and `LeaveAgency` for the store in ctx |
| `NewCatalog(adminPool, ids)`, `Catalog` | The console's plan catalog on `app_admin` (ADR-0056): features, plan versions with the stores on each (`app.plan_subscribers()`: trialing, active or past due, counts only), publish, retire |
| `Feature`, `PlanVersion`, `Price`, `CurrencyFee` | The catalog's DTOs; `Validate()` checks them |

It exports no plan codes. `internal/tools/noplannames` fails the build
on any plan-name comparison outside this module.

## Plans

| Code | Kind | Trial | Notes |
|---|---|---|---|
| `free` | free | — | Every new store; ads on the storefront (7g); subdomain only |
| `pays` | pays | — | No subscription; a fee on delivered orders (7c) |
| `merchant`, `pro`, `brand` | subscription | 14 days | Monthly or annual (annual = 10 months) |
| `agency` | agency | 14 days | Many stores, one invoice (7c) |

The seeded prices and limits are **placeholders**: set them before launch
([go-live](../go-live.md)).

| Feature | Kind | free | pays | merchant | pro | brand | agency |
|---|---|---|---|---|---|---|---|
| `orders_per_month` | monthly | 30 | ∞ | 300 | 1500 | ∞ | ∞ |
| `messages` | monthly | 0 | 300 | 500 | 2000 | 10000 | 10000 |
| `products` | count | 50 | 1000 | 500 | 5000 | ∞ | ∞ |
| `staff` | count | 1 | 2 | 3 | 10 | 25 | 50 |
| `custom_domains` | count | 0 | 1 | 1 | 5 | 5 | 5 |
| `card_payments` | flag | — | ✓ | ✓ | ✓ | ✓ | ✓ |
| `coupons` | flag | — | ✓ | ✓ | ✓ | ✓ | ✓ |
| `pixels` | flag | — | ✓ | ✓ | ✓ | ✓ | ✓ |
| `feeds` | flag | — | ✓ | — | ✓ | ✓ | ✓ |
| `ad_free` | flag | — | ✓ | ✓ | ✓ | ✓ | ✓ |

### Editing the catalog (ADR-0056)

- **Features:** the console adds keys and renames or reorders them. A
  key's kind is fixed. A key the code does not know is display-only.
- **Publishing:** the next version of a code goes on sale and the
  previous one is retired. A code keeps its kind, and one free plan is
  on sale at a time.
- **Retiring:** takes a code off sale. Its stores keep their version.
  The free plan cannot be retired.
- **Renewals:** a renewal is invoiced at the newest active version that
  sells the store's cycle in the account's currency
  (`subscriptions.renewal_plan_id`). Paying it moves the subscription
  there.
- **Free stores:** they follow a new free version within the
  entitlements cache's 30 s.

## Pay-as-you-sell fees (ADR-0048)

- **When.** A plan's fee is charged on each **delivered** order, from
  `plan_fees`. Only the pay-as-you-sell plan has one: 2.5% in the seed,
  a placeholder.
- **The base** is the goods after their discount, with no shipping and
  no COD fee.
- **The fee** is rounded half up, within the rule's bounds, and never
  more than its base.

| Event | Fee |
|---|---|
| Delivered | A `due` row in `sell_fees`, once per order, with the plan, rate and base of that moment; period = the delivery's month (UTC) |
| Returned before the invoice | `cancelled` |
| Returned after the invoice | `credit_due`: credited on the next invoice (7c-2) |

## Accounts and invoices (ADR-0049)

- **Accounts.** An account is who pays: a store's own account, made the
  first time it has something to bill, or an agency's, which the console
  sets up (ADR-0059).
- **The account wall.** Account tables sit behind their own RLS on
  `app.account_id`, which billing sets from the store's own subscription
  or from a system job.

| Job | Does |
|---|---|
| `billing.invoice_month` | Daily (and at start): asks for the previous month's invoice of every billable store (`app.billable_stores`) |
| `billing.invoice_store` | Gives the store its account if needed; asks for the account's invoice |
| `billing.invoice_account` | Writes the account's invoice of the month, once |

An invoice holds:
- each store's renewal ending by the next month's first day, at its plan
  version's price (an agency's plan once per account);
- the store's `due` fees up to the month;
- credits for returned orders, as negative lines;
- the country's VAT (EG 14%, SA 15%, AE 5%).

Numbers are `INV-<year>-<n>` and have no gaps. An invoice is due three
days after issue. When there is nothing to bill, or the credits are as
large as the charges, no invoice is written and the credits wait.

## Plan changes (ADR-0050)

- **The event.** Every lifecycle step is written with
  `billing.subscription.changed` in its transaction. Every role's bus
  sends it to:
  - entitlements, whose cached grants are dropped on every instance over
    Redis;
  - the storefront, whose pages are refreshed and purged at the edge.
- **Period ends.** `billing.end_periods` (hourly and at start) lists, through
  `app.periods_ended`, the stores whose trial or cancelled period
  ended. `billing.end_period` moves each to the free plan, keeping
  `trial_used`.

### Agencies (ADR-0059)

- **The plan:** an agency account names its plan code
  (`billing_accounts.plan_code`).
- **Joining:** a store joins from no account, or from its own once that
  owes nothing; its old invoices stay there.
  - It takes the agency's plan version, status and period.
  - The first store's period runs from now to the first of next month.
  - The agency is locked while a store joins.
- **Leaving:** the store goes to the free plan with no account; its next
  invoice opens its own.
- **Listing:** `app.agency_accounts()` lists agencies for the console
  (names and counts).

## Founding merchants (ADR-0074)

| Piece | What |
|---|---|
| `founding_program` | One row: on, seats (100), discount (2000 bps). The console writes it through `NewFounding(adminPool)` |
| `founding_members` | A store's membership (RLS): owner, seat, discount kept, locked plan, cycle and price, lost and why |
| First period | While seats are left and the owner never joined, the subscribe invoice carries a `founding_discount` line; paying it takes the next seat (`app.founding_join`) |
| Renewals | Same plan and cycle: the lower of the lock and the current price, then the discount. Another plan or cycle: its price, locked again |
| `billing.founding_sweep` | Daily: ends memberships of stores off a subscription or closed (`cancelled`), or with a renewal open 30 days past due (`unpaid`) |

Seats are never reused, and an owner joins once (`owner_id` is unique).
Identity's `OwnerTx` gives billing the owner through `Deps.Owner`.

## Collection and dunning (ADR-0051)

| Job | Does |
|---|---|
| `billing.dunning` | Hourly with `BILLING_COLLECTION_ENABLED=true`. Lists, through `app.dunning_due`, invoices to charge, stores to suspend, to hold and to cancel |
| `billing.charge_invoice` | Charges the account's card on file, outside a transaction, then records the attempt and settles or fails every store of the account |
| `billing.suspend` | The grace ran out: suspended, on the free plan's grants, the storefront still open (ADR-0082) |
| `billing.hold` | 30 days suspended and unpaid: held (`subscriptions.held_at`, `stores.billing_hold`), the maintenance page |
| `billing.cancel` | 90 days after the suspension: cancelled (held if it was not), data due for deletion (nothing deleted in P7) |
| `billing.notify` | Emails the owners (payment failed with the retry, free-plan and closing dates; suspended to the free plan with the closing date; held; paid), then sends the same on WhatsApp to their verified phones. WhatsApp is best effort and logged on failure (ADR-0054) |

- **Retries.** A failed charge is retried 1, 3 and 5 days after the
  first failure.
- **Grace.** The store is past due from that failure, with 7 days of
  grace.
- **Paying,** by card or a confirmed transfer (`Collections.SettleInvoice`):
  - closes the invoice;
  - renews the period the invoice billed;
  - brings every store back and releases its hold.
- **A payment of an invoice no longer open** (paid on two pages, or on
  the page while the card was charged, or after a transfer was settled)
  is kept as a `duplicate` attempt, once per gateway transaction, with an
  error log line (ADR-0063). The console lists it
  (`Console.DuplicatePayments`) for an admin to refund on the gateway,
  then records the refund (`Console.MarkRefunded`). A transfer settled on
  such an invoice is refused.
- **Cards on file.** They are sealed in `billing_payment_methods` and
  saved by the gateway's token callback (7d-2) through
  `Collections.SavePaymentMethod`.

## Paymob (ADR-0052)

The `paymob` gateway charges cards on file through the platform's own
Paymob accounts, one per currency:
1. It asks first (inquiry by the attempt's reference
   `<invoice>-<attempt>`).
2. Then it creates an Intention on the MOTO integration.
3. Then it pays with the card token.

A lost answer is found by the retry, never charged twice. The shared
client is `platform/paymob`.

| Variable (worker) | Meaning |
|---|---|
| `BILLING_PAYMOB_<EGP\|SAR\|AED>_SECRET_KEY` | The account's Intention API key |
| `BILLING_PAYMOB_<…>_API_KEY` | For inquiries |
| `BILLING_PAYMOB_<…>_MOTO_INTEGRATION_ID` | Merchant-initiated charges; Paymob enables it on request |
| `BILLING_PAYMOB_BASE_URL` | Overrides the API (tests) |

## The dashboard (7f-2, the approved mockups)

- **`/stores/{id}/billing`** (owner only) has four parts:
  - **The plan:** status, cycle, the period's date, how it renews, and
    a scheduled change or a cancellation, with "تراجع" to undo it.
  - **Actions:** "change plan", "switch to annual" (a paid monthly plan
    with an annual price) and "cancel renewal" (asks first).
  - **The card on file.**
  - **This month's usage** (ADR-0061), and the open invoices with
    "pay now", which goes to Paymob. Trial and past-due banners sit
    above.
- **`/stores/{id}/billing/plans`:** a monthly or annual toggle and a card
  per plan on sale, with "try N days free" while a trial is available.
  - **A paid plan** goes to Paymob.
  - **Other choices** return to the billing page with what happened.
  - **The comparison** is one row per catalog feature.
- **Prices before VAT.** The picker's prices are before VAT, which
  invoices add.

## Choosing a plan (ADR-0060)

| From | To | Effect |
|---|---|---|
| Free, trial or pay as you sell | Free or pay as you sell | At once |
| Free, trial or pay as you sell | A paid plan | The first period is paid on Paymob's page (a `subscribe` invoice, never charged by dunning); it starts when paid |
| A paid period | Another paid plan or cycle | At the renewal (`next_plan_id`, `next_cycle`), no proration |
| A paid period | Pay as you sell or free | After the period (a cancellation, to `next_plan_id` for pay as you sell) |

- **Taking a change back:** choosing the current plan and cycle again
  undoes a scheduled change; `resume` does the same.
- **Settle first:** a past-due store, or one with a billed renewal,
  settles first.

## HTTP API (tag `billing`, ADR-0053)

| Operation | Permission | Notes |
|---|---|---|
| `getBilling` `GET /v1/stores/{id}/billing` | `billing.manage` (owner) | The plan's names, the status and its dates, the card on file, the open invoices |
| `payInvoice` `POST …/billing/invoices/{invoice}/pay` | `billing.manage` | `{checkout_url}` on Paymob, where the card is also saved. 404 for an invoice that is not open or not the store's; 503 for a currency without a checkout |
| `listBillingPlans` `GET …/billing/plans` | `billing.manage` | The public plans on sale in the store's currency, with the features catalog (ADR-0060) |
| `choosePlan` `POST …/billing/subscription` | `billing.manage` | `{plan_code, cycle}` → the action (`apply`, `pay` or `schedule`) and, for `pay`, the `checkout_url` |
| `startTrial`, `cancelRenewal`, `resumeRenewal` | `billing.manage` | A trial (once, from free), stop renewing, keep the plan |

Paymob's callbacks reach `/v1/webhooks/paymob_billing` (billing's
platform provider):
- **Signature.** A callback is verified with a region's HMAC secret.
- **Placement.** It is placed in its store through
  `app.billing_checkout_store`.
- **Processing.** `billing.process_callback` handles it:
  - a card token becomes the card on file;
  - a payment is confirmed by asking Paymob, then settles the invoice.

| Variable (api and worker) | Meaning |
|---|---|
| `BILLING_PAYMOB_<CUR>_PUBLIC_KEY`, `…_CARD_INTEGRATION_ID`, `…_HMAC_SECRET` | The region's checkout: all three, on a region with the account's keys |

## Collecting pay-as-you-sell fees (ADR-0083)

- **Settings** (`billing_settings`, one row, written by `app_admin`):
  - a prepaid balance on or off;
  - the card with a minimum on or off;
  - a minimum per currency (EGP 300, SAR 100, AED 100 to start).

  Both ways start off. The console reads and saves them at
  `/admin/v1/billing/collection` (`billing.NewCollection`).
- **Balance:** `billing_accounts.balance_minor`, changed only under the
  account row's lock. Each change is a `balance_entries` row (`topup`,
  `manual`, `fee`, `fee_refund`) with the balance after it. It may go
  negative through delivered orders.
- **Levels:** `ok`, `low` (under half the minimum), `debt` (zero or
  less).
- **The console** (`Console.StoreBalance`, `Console.CreditBalance`):
  - shows a store's balance and its latest 50 entries;
  - adds to it with a reason, a reference and a note, recorded in the
    admin log;
  - opens the store's account when it has none.

  Agencies' stores have no balance.
- **Paying from the balance** (ADR-0084), for an account whose
  `collection` is `balance`:
  - a delivered order's fee is a `fee` entry, once per order, and the
    fee is marked `sell_fees.prepaid`;
  - a return is a `fee_refund` at once, before or after the invoice;
  - the month's invoice is paid from the balance, which gives one
    `invoice` entry: its tax;
  - running out puts the store past due (then ADR-0082's path), coming
    back restores it, and a low balance is told (`balance_low`,
    `balance_empty`, `balance_restored`).
- **Topping up** (ADR-0085): `POST /v1/stores/{id}/billing/balance/topups`
  (at least the minimum) opens Paymob's page through a `topup` checkout.
  The confirmed payment is one `topup` entry (`checkout_id`), and a
  repeated callback adds nothing. `GET /v1/stores/{id}/billing/balance`
  is the merchant's view of the balance.

## The subscription lifecycle

| From | Step | To |
|---|---|---|
| free (no row, or active on the free plan) | `StartTrial` (once per store) | trialing |
| trialing | the trial ends unpaid (`EndPeriod`) | free |
| any | a payment (`Activate`) | active, for a period |
| active (paid) | a renewal fails (`PaymentFailed`) | past_due; the plan applies 7 days more |
| past_due | grace ends unpaid (`Suspend`) | suspended: an ordinary free store, still open (ADR-0082) |
| suspended | 30 days pass unpaid (`Hold`) | suspended and held: the maintenance page, data kept |
| suspended | 90 days from the suspension (`Close`) | cancelled (and held): the data may be deleted |
| active (paid) | the merchant cancels (`CancelAtEnd`), then the period ends (`EndPeriod`) | free |

While a plan does not apply (trial over, suspended, cancelled), the
store gets the free plan's grants. Steps are pure functions with
table-driven tests. The jobs that take them arrive with payments (7d).

## Tables (migration 00056)

| Table | Kind | Notes |
|---|---|---|
| `features` | platform | The catalog. The console may add display-only keys |
| `plans` | platform | Versions: `UNIQUE (code, version)`; one active version per code; one active free plan |
| `plan_prices` | platform | `(plan_id, currency, cycle)` |
| `plan_entitlements` | platform | `(plan_id, feature)`; `value` NULL is unlimited |
| `plan_fees` | platform | `(plan_id, currency)`: `rate_bps`, `min_minor`, `max_minor` (00057) |
| `sell_fees` | tenant (RLS) | `(store_id, order_id)`; `sell_fees_store_period` serves invoices; `app_user` cannot delete (00057) |
| `billing_accounts`, `billing_account_stores` | account (RLS on `app.account_id`) | A store is in one account (00058) |
| `invoices`, `invoice_lines` | account (RLS) | `UNIQUE (account_id, period)`; lines are never updated or deleted by merchant code |
| `billing_payment_methods` | account (RLS) | The card on file, sealed (00060) |
| `payment_attempts` | account (RLS) | Every try: approved, declined, no method, manual, duplicate (00069); never deleted; only a duplicate's `refunded_at` and `refund_reference` are ever updated |
| (indexes, 00090) | | invoices `(issued_at DESC, id DESC)`, `(due_at)` where open, `(paid_at)` where paid; payment attempts `(created_at DESC, id DESC)`: the console's lists and summary (ADR-0076) |
| `billing_checkouts` | tenant (RLS) | A "pay now": our reference `bc_<ULID>`, Paymob's order (00061) |
| `invoice_counters` | platform | One row per year: gapless numbers |
| `tax_rates` | platform | VAT by country |
| `subscriptions` | tenant (RLS) | One row per store; `app_user` cannot delete. Existing stores got a 14-day trial of pro from the migration. `account_id` (who pays) and `renewal_invoice_id` (a billed renewal) from 00058 |

`app_user` may only read the platform tables.

## Tests

| What | Test |
|---|---|
| Every lifecycle step and its refusals; the dunning path | `domain.TestDunning` and the other `domain` tests |
| Grants per status on the seeded plans; queries per read | `app.TestGrants`, `app.TestGrantsQueries` |
| Tenant isolation; merchant code cannot change plans | `app.TestSubscriptionsAreTenantRows` |
| The backfill for existing stores | `app.TestMigrationStartsTrialsForExistingStores` |
| Fee rounding and bounds; returns | `domain.TestFee`, `domain.TestSellFeeReturned` |
| Fees once per delivery, cancelled or credited on return, none on other plans | `app.TestSellFees` |
| The orders adapter (base, statuses) | `cmd/platform` `TestBillingOrders` |
| A pay-as-you-sell invoice (late fee, credit, VAT, once); a renewal billed once; an agency's one invoice; nothing to bill; the month's run; the account wall | `app.TestInvoiceOfAPayAsYouSellStore`, `TestInvoiceRenewsAPaidPlanOnce`, `TestAgencyGetsOneInvoice`, `TestNothingToBill`, `TestStartMonth`, `TestAccountWall`, `TestAccountTablesUseTheWall` |
| The console's invoices and payments: an invoice with its lines and attempts, every filter (a `%` is literal), validation, settled and collected with its VAT, the failures counted; keyset pages | `app.TestConsoleInvoicesAndPayments`, `TestConsolePages` |
| No plan-name checks anywhere else | `internal/tools/noplannames` |
| The growth figures: a paid invoice in its Cairo month before VAT, its store converted and an unpaid one not, Today's accounts and plans; `domain.Today` matches `MRR` | `app.TestGrowthFigures`, `domain.TestToday` |
