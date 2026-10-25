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
| `Shop.Track(ctx, code, phone)` | Order tracking: the order a shopper names by its 8-character code and phone (any usual format), read through that phone's recent orders (`orders_store_phone`) and matched by code; anything else is `ErrOrderNotFound`, whichever part was wrong |
| `Fulfilment` (`Module.Fulfilment`, `NewFulfilment`) | `Get` and `TransitionSystemTx`: a carrier's moves in the caller's transaction ([ADR-0027](../adr/0027-shipments-tracking-order-moves.md)) |

The module uses these other modules, each through its `api.go`:

- `catalog.Catalog`: prices; holding, releasing, committing and
  restocking stock;
- `customers.Customers`: recording the customer and refusing blocked
  ones;
- `shipping.Quoter`: the delivery rate and the COD fee for the order's
  address ([ADR-0025](../adr/0025-shipping-zones-rates-cod-fee.md)).

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

## Tables

All are tenant tables under RLS. The first three are partitioned by
month on the order's creation time, which its ULID encodes.

| Table | Notes |
|---|---|
| `orders` | Contact, `lines_hash` for the duplicate window, amounts in minor units: `total = subtotal + shipping + cod_fee`, all fixed at placement. Indexes per list driver: status, phone, customer. |
| `order_items` | Copied titles and prices. Primary key on the order's partition key. |
| `order_events` | The timeline; append-only. `actor` names a system that made a move (`carrier:bosta`). |
| `order_idempotency` | `(store_id, key)` → fingerprint and order. Kept a week, purged per store by keyed orders. |

## HTTP API (tag `orders`)

| Operation | Permission |
|---|---|
| `listOrders` (status, from/to, governorate, phone, customer_id, cursor) | orders.read |
| `getOrder` (lines and timeline) | orders.read |
| `createOrder` (a phone order; `Idempotency-Key`) | orders.manage |
| `transitionOrder` (optional `version`) | the move's permission |
| `bulkTransitionOrders` (≤ 100; one result per order) | the move's permission, per order |
| `exportOrders` (CSV) | orders.export |

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

## Rules worth knowing

- **Charges.** Every order (storefront or staff) is charged the shipping
  quote for its governorate, city and subtotal. A governorate outside
  the store's zones is a validation error on `governorate`, code
  `not_delivered`. `Shop.Quote` prices an order the same way without
  placing it, for the storefront form.

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
