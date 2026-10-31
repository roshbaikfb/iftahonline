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
| The merchant's billing page | 7f |

## Public API (`internal/modules/billing/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Grants()` | Wiring; the `GrantSource` entitlements reads |
| `GrantSource.GrantsTx(ctx, tx)` | What the store in ctx is granted now, by feature key, read in its tenant transaction |
| `Grant`, `Grants` | `Value` (0/1 for a flag, a count, units a month) or `Unlimited`; a key not listed is not granted |

| `Fees.OrderMoved(ctx, order)` | Follows an order to its current status: a delivered order's fee (once), a returned order's fee cancelled or credited |
| `Orders`, `Order`, `OrderStatus` | The order reader fees need; `cmd/platform` adapts `orders.Fulfilment` (billing does not import orders) |
| `SellFeesSubscriber` | `billing.sell_fees`, subscribed to `orders.order.status_changed` in the worker |

| `Module.Console()`, `Console` | The console's view (ADR-0058): `Health` (counts through `app.billing_health()`), `Summary` of the store in ctx, `SettleStoreInvoice` (a confirmed transfer; 404 unless it is an open invoice of the store's account) |
| `Module.Agencies()`, `Agencies`, `Agency` | Agencies from the console (ADR-0059): create, list, view, `JoinAgency` and `LeaveAgency` for the store in ctx |
| `NewCatalog(adminPool, ids)`, `Catalog` | The console's plan catalog on `app_admin` (ADR-0056): features, plan versions, publish, retire |
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

## Collection and dunning (ADR-0051)

| Job | Does |
|---|---|
| `billing.dunning` | Hourly with `BILLING_COLLECTION_ENABLED=true`. Lists, through `app.dunning_due`, invoices to charge, stores to suspend and stores to cancel |
| `billing.charge_invoice` | Charges the account's card on file, outside a transaction, then records the attempt and settles or fails every store of the account |
| `billing.suspend` / `billing.cancel` | The grace ran out: suspended and held (maintenance page). After 90 days: cancelled, data due for deletion (nothing deleted in P7) |
| `billing.notify` | Emails the owners (payment failed with the retry and closing dates, suspended, paid), then sends the same on WhatsApp to their verified phones. WhatsApp is best effort and logged on failure (ADR-0054) |

- **Retries.** A failed charge is retried 1, 3 and 5 days after the
  first failure.
- **Grace.** The store is past due from that failure, with 7 days of
  grace.
- **Paying,** by card or a confirmed transfer (`Collections.SettleInvoice`):
  - closes the invoice;
  - renews the period the invoice billed;
  - brings every store back and releases its hold.
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

## The subscription lifecycle

| From | Step | To |
|---|---|---|
| free (no row, or active on the free plan) | `StartTrial` (once per store) | trialing |
| trialing | the trial ends unpaid (`EndPeriod`) | free |
| any | a payment (`Activate`) | active, for a period |
| active (paid) | a renewal fails (`PaymentFailed`) | past_due; the plan applies 7 days more |
| past_due | grace ends unpaid (`Suspend`) | suspended: store closed, data kept |
| suspended | 90 days pass (`Close`) | cancelled: the data may be deleted |
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
| `payment_attempts` | account (RLS) | Every try: approved, declined, no method, manual; never updated or deleted by merchant code |
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
| No plan-name checks anywhere else | `internal/tools/noplannames` |
