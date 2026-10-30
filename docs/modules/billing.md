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
| Payments and dunning | 7d |
| The console's plan editor | 7e |
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
  sets up (7e).
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
