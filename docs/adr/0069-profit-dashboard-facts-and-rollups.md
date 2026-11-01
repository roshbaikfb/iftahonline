# ADR-0069: The profit dashboard: its own facts, daily rollups, the read pool

- Status: Accepted
- Date: 2026-11-01

## Context

P8 asks for a profit dashboard. It shows revenue minus product cost,
shipping, COD fees, returns and ad spend. It breaks these down per
product, campaign and governorate.

P8's requirements:

- **Read replica.** The dashboard runs on the read replica (ADR-0064).
- **Scheduled refresh.** Scheduled jobs refresh its aggregates.
- **Ad spend.** Ad spend is entered by hand first. An ad-platform
  importer interface comes later.
- **Acceptance test.** A test proves analytics queries use the read pool
  only.

The owner approved (2026-11-01):

- **Delivery costs.** They start as the store's estimates. A carrier's
  payout replaces them (ADR-0068).
- **Plan feature.** The dashboard is a plan feature (`profit`).

Constraints:

- **Module boundaries.** Analytics reads orders and shipping only
  through their api.go (CLAUDE.md rule 2).
- **Prepared queries.** Queries are prepared and use indexes; there is no
  N+1.
- **The replica lags.** Writes go to the primary.

## Decision

1. **Analytics keeps its own facts** (migration 00079, all tenant tables
   under RLS):
   - **What triggers a fact.** The module subscribes to
     `orders.order.placed`, `status_changed` and `payment_changed` as
     `analytics.learn`. Each event re-reads the order through
     `orders.Outcomes`, then upserts its row in `analytics_orders` and
     its lines in `analytics_lines`. Repeated or out-of-order events are
     harmless.
   - **What a fact holds:**
     - the store-local day;
     - the profit state (open, delivered, returned or failed delivery,
       cancelled);
     - the governorate and the campaign (`utm_campaign`);
     - revenue (the subtotal less the discount, spread over the lines by
       their totals);
     - the delivery rate and the COD fee charged to the customer;
     - what the courier collects;
     - a prepaid delivery the merchant keeps;
     - the risk band;
     - each line's cost (`Line.UnitCost × qty`, or unknown).
   - **Backfill.** A store's first learnt order queues its backfill
     (`analytics.backfill`, once).
2. **The profit model** (`domain.OfOrder`) is deterministic integer
   arithmetic:
   - **Delivered:** items plus what was paid for delivery, less product
     cost and the delivery cost. The delivery cost is the carrier's real
     charge, or else the store's estimate (by default, the rate the
     customer was charged) plus `cod_fee_bp` of what was collected.
   - **Returned:** the kept deposit, less the delivery both ways (the
     carrier's charge, or both estimates). The goods come back.
   - **Open and cancelled:** counted, earning and costing nothing.
   - **Unknown line costs** are counted (`cost_missing`), never guessed.
   - **Ad spend** goes to the day's total and to its campaign, matched by
     name to `utm_campaign`.
   - **Products** take their lines' revenue and cost. They share delivery,
     fees and returns by revenue share, with integer remainders to the
     last product, so products sum to the order exactly.
3. **Daily rollups.**
   - **Dirty days.** Every change marks a day dirty in `analytics_dirty`:
     a fact, an ad spend entry, or the settings (which mark every day).
   - **Fan-out.** `analytics.rollups` runs every 10 minutes and queues one
     `analytics.rollup` per store with dirty days. It finds them through
     the owner-only `app.analytics_dirty_stores()`, which returns store
     IDs only.
   - **The rollup.** Each store's rollup takes up to 31 days per
     transaction, removing them as it goes, and rewrites their
     `profit_daily` rows: dimension total, product, campaign or
     governorate. A day marked again meanwhile is computed by the next
     run.
4. **Carrier charges.** `analytics.sweeps` runs daily and queues an
   `analytics.sweep` for each store with delivered or returned orders of
   the last 60 days whose charge is not known
   (`app.analytics_sweep_stores()`). The sweep asks
   `shipping.Charges.CarrierCharges`, 500 orders a batch and at most
   20 batches, records what is known, and marks those days dirty.
5. **The dashboard reads the read pool only.**
   - `Service.Profit` runs one read-only transaction on
     `*postgres.ReadPool`: the totals, the days and one dimension's
     breakdown (at most 200 keys, most profitable first).
   - A service without a read pool refuses to serve it rather than fall
     back to the primary.
   - `TestProfitReadsTheReadPoolOnly` captures both pools and proves zero
     dashboard queries on the primary and three on the read pool.
6. **API** (tag `analytics`):
   - `getProfit` needs finance.read and the `profit` plan feature (paid
     plans).
   - `getProfitSettings` needs finance.read; `updateProfitSettings` needs
     finance.manage and is audited. They cover the delivery cost, the
     return cost and the COD fee in basis points.
   - `listAdSpend` needs finance.read; `createAdSpend` and
     `deleteAdSpend` need finance.manage. A day of the last two years;
     platform meta, tiktok, google, snap or other; a campaign name.
   - Days are the store's, both ends included, at most 366.
7. **Importers come later.** `AdSpendSource` is the interface they
   implement (`Platform`, `Spend(from, to)`). `ImportAdSpend` replaces
   what a platform said of a range and leaves manual entries alone.

## Consequences

- **Positive:**
  - The dashboard reads a few hundred pre-computed rows, whatever the
    store's order count, and never loads the primary.
  - Every figure can be traced to an order fact and a rule in one Go
    function, tested with tables.
  - Real carrier charges correct the estimates as payouts arrive.
- **Negative:**
  - **Lag.** Figures trail the orders by up to about ten minutes, plus
    the replica's delay.
  - **Facts duplicate some order data** (amounts, governorate, campaign,
    line titles). That is the price of a module that reads no other
    module's tables.
  - **Campaign matching is by name**, so ad spend typed under another
    name than the UTM stands alone.
  - **Store currency.** A store's currency is assumed fixed. Figures
    carry the store's currency, not each order's.
