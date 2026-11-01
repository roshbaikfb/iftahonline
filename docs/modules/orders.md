# Module: orders

The orders module owns **orders**: placing them (idempotent, deduplicated,
with stock held and the customer recorded), their **state machine** and
timeline, lists, bulk moves and CSV exports. Design:
[ADR-0021](../adr/0021-orders-state-machine-idempotency-partitions.md).
Customers and stock holds: [ADR-0020](../adr/0020-customers-order-stock-and-monthly-partitions.md).

## Public API (`internal/modules/orders/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount` | Wiring, and the merchant HTTP API (behind identity's membership middleware) |
| `Order`, `Status`, `Status…` constants | The order DTO and its states |
| `TransitionError`, `ErrInvalidTransition`, `ErrOrderNotFound`, `ErrIdempotencyKeyReused`, `ErrVersionConflict` | Errors |
| `OrderPlaced`, `OrderStatusChanged`, `EventOrderPlaced`, `EventOrderStatusChanged` | Events the module publishes |
| `StoreInfo`, `StoreSource` | The store's currency, country and zone; `cmd/platform` adapts tenancy |
| `Shop.Bestsellers(ctx, limit)` | Product IDs by units sold in confirmed orders over the last 30 days, most first, ≤ `MaxBestsellers` (48) |
| `Shop.Track(ctx, code, phone)` | Order tracking: the order a shopper names by its 8-character code and phone (any usual format), read through that phone's recent orders (`orders_store_phone`) and matched by code; anything else is `ErrOrderNotFound`, whichever part was wrong |
| `Conversions` (`NewConversions`) | `Conversion(ctx, id)`: an order with its lines and signals, for the server-side conversions (ADR-0043); `ok` is false without signals |
| `Attribution`, `Signals` | Where an order came from: UTM, and what ad providers match a conversion with |
| `Fulfilment` (`Module.Fulfilment`, `NewFulfilment`) | `Get` and `TransitionSystemTx`: a carrier's moves in the caller's transaction ([ADR-0027](../adr/0027-shipments-tracking-order-moves.md)); `TransitionSystemReasonTx` is the same with an outcome reason (the confirmation's deadline cancels as `unconfirmed`) |
| `OutcomeReason`, `Reason…` constants | Why an order did not reach its customer, from its latest move ([ADR-0065](../adr/0065-risk-bands-from-delivery-outcomes.md)) |
| `Outcomes` (`Module.Outcomes`, `NewOutcomes`) | `Get` and `Before(ctx, before, limit)`: the store's orders, newest first, without lines, a page (≤ 100) at a time; what the risk module learns from |

The module uses these other modules, each through its `api.go`:

- `catalog.Catalog`: prices; holding, releasing, committing and
  restocking stock;
- `customers.Customers`: recording the customer and refusing blocked
  ones;
- `shipping.Quoter`: the delivery rate and the COD fee for the order's
  address ([ADR-0025](../adr/0025-shipping-zones-rates-cod-fee.md));
- `Coupons` (a port `cmd/platform` adapts to promotions): a coupon's
  discount ([ADR-0042](../adr/0042-coupons.md)).

## State machine

| From | To | Stock | Permission |
|---|---|---|---|
| pending | confirmed | — | orders.manage |
| pending, confirmed, ready_to_ship | cancelled | release | orders.manage |
| confirmed | ready_to_ship | — | orders.fulfil |
| ready_to_ship | shipped | commit | orders.fulfil |
| shipped | delivered, failed_delivery | — | orders.fulfil |
| failed_delivery | shipped (retry) | commit (no-op) | orders.fulfil |
| failed_delivery, delivered | returned | restock | orders.fulfil |

## Events

| Event | Published when |
|---|---|
| `orders.order.placed` (`order_id`, `source`) | An order is placed |
| `orders.order.status_changed` (`order_id`, `from`, `to`) | Any transition |

The module subscribes to its own `orders.order.status_changed` with
`orders.count_sales` (`NewSalesCounter`, the worker): bestsellers follow
each order's current status ([ADR-0033](../adr/0033-shop-search-and-bestsellers.md)).

## Tables

All are tenant tables under RLS. The first three, and `order_signals`,
are partitioned by month on the order's creation time, which its ULID
encodes.

| Table | Notes |
|---|---|
| `orders` | Contact, `lines_hash` for the duplicate window, amounts in minor units: `total = subtotal − discount + shipping + cod_fee`, all fixed at placement; `coupon_code` the coupon taken; `utm_source`, `utm_medium`, `utm_campaign`, `utm_content`, `utm_term` the campaign that brought it (00052, nullable). Indexes per list driver: status, phone, customer. |
| `order_items` | Copied titles and prices. Primary key on the order's partition key. |
| `order_events` | The timeline; append-only. `actor` names a system that made a move (`carrier:bosta`). |
| `product_sales` | Bestsellers: units per `(store_id, product_id, day)` from confirmed orders, the day being the order's; index `(store_id, day)`; days older than 40 pruned (00037) |
| `order_sales_counted` | Which orders are counted, so counting is idempotent (00037) |
| `order_signals` | One row per storefront order that has any (ADR-0043): Meta's `_fbp`/`_fbc`, TikTok's `_ttp` and click ID, Snapchat's and Google's click IDs, the IP address and user agent. Written in the placing transaction; `app_user` cannot change or delete rows; partitions dropped after one month (00052). Never shown in the API. |
| `order_idempotency` | `(store_id, key)` → fingerprint and order. Kept a week, purged per store by keyed orders. |

## HTTP API (tag `orders`)

| Operation | Permission |
|---|---|
| `listOrders` (status, from/to, governorate, phone, customer_id, cursor) | orders.read |
| `getOrder` (lines and timeline) | orders.read |
| `createOrder` (a phone order; `Idempotency-Key`) | orders.manage |
| `transitionOrder` (optional `version`, optional `reason`) | the move's permission |
| `bulkTransitionOrders` (≤ 100; one result per order; optional `reason`) | the move's permission, per order |
| `releaseOrder` (lift the risk rules' hold of a pending order) | orders.manage |
| `exportOrders` (CSV) | orders.export |
| `countOrders` (every status with its count, zeros included; one query on `orders_store_status`) | orders.read |
| `getOrderStats` (`range` today, 7d or 30d, by the store's time zone; sales are every order but cancelled and returned ones, which are summed apart, totals including shipping and the COD fee; `previous` is the same span one period earlier; `daily` the last 30 days; `oldest_pending_at`). Three queries: one walk of the primary key for both periods, one grouped read for the chart, one step of `orders_store_status` | orders.read |

Roles:

| Role | Permissions |
|---|---|
| Owner, manager | All |
| Support | read, manage |
| Shipping | read, fulfil |

## Dashboard

The merchant dashboard's orders screens (list with filters, bulk
confirm, order page with moves, CSV export) use this API; see
[ADR-0023](../adr/0023-merchant-dashboard-shell.md).
The order page also shows the coupon's discount (`discount_minor`,
`coupon_code`) in the totals, the campaign it came from (`attribution`) in
its own card, and the customer's confirmation by message (see
[messaging](messaging.md)).

## Plan limit (ADR-0046)

Every placed order counts one `orders_per_month` through
`entitlements.CountTx`, in the order's transaction:
- **Never refused.** The plan's monthly orders are a soft limit; an order
  past it is placed like any other.
- **Counted once.** A replayed or duplicate submit aborts before the
  count.
- **The merchant hears.** At 80% and 100% the count publishes
  `entitlements.usage.crossed`, and messaging warns the merchant.

## Rules worth knowing

- **Line costs** (ADR-0068). Each line keeps its variant's cost when it
  was placed (`Line.UnitCost`, `order_items.unit_cost_minor`; nil when not
  known). It is not in the order API.

- **Prepaid delivery** (ADR-0067, migration 00076).
  - **When.** A `prepay` verdict, in a store that takes cards
    (`CardCheck`), sets `deposit_minor` (shipping + COD fee) and
    `deposit_status = awaiting`. Otherwise the order is held.
  - **Paying.** `OnlineDue()` is what the shopper pays online: a card
    order's total, or the deposit. `SetPaymentStatusTx` moves the
    deposit for such orders. The deposit is paid from the link sent to
    the customer's phone (ADR-0070); the customer's own yes does not
    confirm the order while it is unpaid (`ErrDepositDue`).
  - **Collecting.** `OnDelivery()` is what the courier collects: the
    total, less a paid deposit, even partly refunded (a gesture, not a
    reason to collect the delivery again); a fully refunded one is
    collected. A refund after the shipment exists does not change the
    shipment's COD: the merchant adjusts it with the carrier.
- **Risk rules** (ADR-0066, migration 00074).
  - **Which orders.** A storefront cash order asks `RiskCheck` (the risk
    module's rules) before it is placed. It keeps the verdict
    (`risk_action`, `risk_band`).
  - **Hold.** A `hold` cannot be confirmed (`409 order_held`) until
    `releaseOrder` (orders.manage, audited `order.released`) sets it to
    `released`.
  - **Verify.** A `verify` cannot be confirmed by a `customer:*` actor
    (`ErrNeedsCall`); staff confirm it after calling.

- **Outcome reasons** (ADR-0065, migration 00072). A move to cancelled,
  failed_delivery or returned may say why: `refused` or `no_answer`.
  Any other move with a reason is a validation error on `reason`. The
  confirmation's deadline cancels with `unconfirmed`, which people
  cannot send. The order keeps its latest move's reason
  (`outcome_reason`), so a failed delivery that is delivered on the next
  attempt loses it. The risk module learns customers' phones from it.

- **Charges.** Every order (storefront or staff) is charged the shipping
  quote for its governorate, city and subtotal. A governorate outside
  the store's zones is a validation error on `governorate`, code
  `not_delivered`. `Shop.Quote` prices an order the same way without
  placing it, for the storefront form.
- **Coupons** (ADR-0042). An order, or a quote, may name a
  `coupon_code`. Its discount comes off the items; free shipping waives
  the rate. The code is previewed when pricing and redeemed in the
  placing transaction once the customer is known. A code that does not
  apply is a field error on `coupon_code` with the reason; terms that
  changed in between are `changed`; nothing is placed. A replayed order
  redeems nothing.
- **Attribution and signals** (ADR-0043). `NewOrder.Attribution` (UTM)
  and `NewOrder.Signals` come from the shopper's browser. They are
  cleaned, never refused: anything too long is cut, and an ID that is not
  one printable word, or an IP that does not parse, is dropped (a fuzz
  test covers this). They are not part of the idempotency fingerprint.
  The order shows `attribution`; the signals stay for the server-side
  conversions (6e-3).

- **A customer's order history** is `listOrders?customer_id=…`.
- **Shoppers** (P4c, the storefront) will place orders through the same
  `PlaceOrder`. They must see only a generic refusal for a blocked
  customer.
- **Testing:**
  - `internal/ordertest` builds the real catalog and customers modules
    for this module's tests.
  - `TestOrderQueriesUseIndexesUnderRLS` checks the list, duplicate and
    by-ID plans on 40,000 orders.
- **Fuzzing.** `domain`: `FuzzValidateNewOrder`.
