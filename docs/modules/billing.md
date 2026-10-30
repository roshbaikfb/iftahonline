# Module: billing

The billing module owns **plans** and each store's **subscription**. It
is the only module that knows plans by name. Every other module asks
[entitlements](entitlements.md) what a store may do.

Design: [ADR-0045](../adr/0045-plans-as-data-and-one-entitlements-service.md).

**Status (P7 7a):** plans, their grants and the subscription lifecycle
are in place.

| Coming | PR |
|---|---|
| Fees on delivered orders, invoices, agency accounts | 7c |
| Payments and dunning | 7d |
| The console's plan editor | 7e |
| The merchant's billing page | 7f |

## Public API (`internal/modules/billing/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Grants()` | Wiring; the `GrantSource` entitlements reads |
| `GrantSource.GrantsTx(ctx, tx)` | What the store in ctx is granted now, by feature key, read in its tenant transaction |
| `Grant`, `Grants` | `Value` (0/1 for a flag, a count, units a month) or `Unlimited`; a key not listed is not granted |

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
| `subscriptions` | tenant (RLS) | One row per store; `app_user` cannot delete. Existing stores got a 14-day trial of pro from the migration |

`app_user` may only read the platform tables.

## Tests

| What | Test |
|---|---|
| Every lifecycle step and its refusals; the dunning path | `domain.TestDunning` and the other `domain` tests |
| Grants per status on the seeded plans; queries per read | `app.TestGrants`, `app.TestGrantsQueries` |
| Tenant isolation; merchant code cannot change plans | `app.TestSubscriptionsAreTenantRows` |
| The backfill for existing stores | `app.TestMigrationStartsTrialsForExistingStores` |
| No plan-name checks anywhere else | `internal/tools/noplannames` |
