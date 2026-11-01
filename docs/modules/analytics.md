# Module: analytics

The net profit dashboard. It shows what the store's delivered orders
earned after product costs, delivery, COD fees, returns and ad spend,
by product, campaign and governorate.

- **Its own facts.** The module keeps its own facts from the orders
  module's events.
- **Scheduled figures.** Scheduled jobs compute daily figures.
- **Read pool.** The dashboard reads them on the read pool.

Design: [ADR-0069](../adr/0069-profit-dashboard-facts-and-rollups.md).
Costs: [ADR-0068](../adr/0068-product-costs-and-carrier-charges.md).
The read pool: [ADR-0064](../adr/0064-read-pool-for-lagging-reads.md).

## Public API (`internal/modules/analytics/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount` | Wiring and the merchant API (api role, with `Read`) |
| `Module.LearnHandler`, `LearnSubscriber` (`analytics.learn`) | Keeps an order's facts. Subscribe it to `orders.order.placed`, `status_changed` and `payment_changed` (worker role) |
| `Module.RegisterWorkers`, `Module.PeriodicJobs` | The backfill, the rollups (every 10 minutes) and the sweeps of carrier charges (daily) |
| `StoreInfo`, `StoreSource` | The store's currency and time zone; `cmd/platform` adapts tenancy |
| `AdSpendSource`, `AdSpendLine` | The interface for ad-platform importers to come |

Deps:
- `orders.Outcomes`;
- `shipping.Charges`;
- `postgres.ReadPool`;
- the plan's `profit` feature.

## Tables (migration 00079, all under RLS)

| Table | Holds |
|---|---|
| `analytics_orders`, `analytics_lines` | Each order's facts and lines (revenue after discount, costs) |
| `profit_settings` | The store's estimates: delivery cost, return cost, COD fee (basis points) |
| `ad_spend` | Ad spend by day, platform and campaign (manual or import) |
| `analytics_dirty` | Days to compute again |
| `profit_daily` | One row per day, dimension (total, product, campaign, governorate) and key |
| `analytics_backfills` | Stores whose past was queued |

## HTTP API (tag `analytics`)

| Operation | Permission |
|---|---|
| `getProfit` (`from`, `to` inclusive, ≤ 366 days; `by`) | finance.read + plan `profit` |
| `getProfitSettings` / `updateProfitSettings` | finance.read / finance.manage |
| `listAdSpend` / `createAdSpend` / `deleteAdSpend` | finance.read / finance.manage; the list stops at 500, its `count` and `total_minor` cover every entry of the range |

## Dashboard (8f, the approved mockup)

- **«الربح الصافي»** (`/profit`, sidebar group «التقارير»):
  - the totals and margin;
  - «من أين جاء الربح» (income, each cost, what is left);
  - each day's profit (losses in orange);
  - a breakdown by product, campaign or governorate. Governorates
    returning over 20% are flagged.
  - A banner counts delivered orders with unknown costs.
  - Ranges: 7, 30 or 90 days, or two dates.
  - A plan without `profit` shows the dashboard locked, with the plans.
- **«التقديرات والإنفاق الإعلاني»** (`/profit/settings`):
  - the delivery, return and COD fee estimates;
  - ad spend by day, platform and campaign, this month's list with
    deletes.
- **Product costs** are typed in the products editor's «التكلفة» column
  (ADR-0068).

## Rules worth knowing

- **Delivered.** Items, plus what the customer paid for delivery, less
  product cost and delivery. Delivery is the carrier's charge when
  known; otherwise the estimate (default: the rate charged) plus the
  COD fee estimate.
- **Returned.** The kept deposit, less delivery both ways. Open and
  cancelled orders earn nothing yet.
- **Unknown costs** are counted, never guessed (`cost_missing`).
- **Ad spend** matches orders by campaign name (`utm_campaign`).
- **Settings changes** recompute every day.
- **The dashboard never reads the primary.** Without a read pool it is
  not served.
