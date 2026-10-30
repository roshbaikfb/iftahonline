# Module: promotions

The promotions module owns the store's **coupons**: the merchant's
codes, what each takes off an order, and its uses. Orders take a coupon
through a port, in the transaction that places them. An order cancelled
before it ships gives its use back. Design:
[ADR-0042](../adr/0042-coupons.md).

## Public API (`internal/modules/promotions/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount` | Wiring; the merchant HTTP API (behind identity's membership middleware) |
| `Module.Coupons()` → `Coupons` | What orders use (adapted in `cmd/platform` to `orders.Coupons`): `Preview(code, subtotal)` and `RedeemTx(tx, code, subtotal, customer, order)` |
| `Module.OrderStatusHandler()`, `ReleaseCouponsSubscriber` | Gives a use back when an order goes from pending, confirmed or ready_to_ship to cancelled; subscribe it to `orders.EventOrderStatusChanged` (worker role) |
| `Discount`, `Rejected`, `Reason` | What a coupon takes off; why it does not apply (`not_found`, `inactive`, `not_started`, `expired`, `min_subtotal`, `used_up`, `per_customer`) |

## Coupons

| Kind | Takes off |
|---|---|
| `percent` | 1–100% of the items' subtotal, rounded down |
| `fixed` | an amount off the items, at most their subtotal |
| `free_shipping` | the delivery rate (the COD fee stays) |

A coupon may also have:
- a minimum items' subtotal;
- `starts_at` and `ends_at` (the end is exclusive);
- `max_uses` (all customers) and `max_per_customer` (one phone number,
  as the customers module records it);
- `active`.

Codes are unique per store, case-insensitive (stored upper case), and
3–32 characters of A-Z, 0-9, `-` and `_`.

## Applying one

1. **Quote** (the storefront's form) and the pricing before an order
   call `Preview`. It checks everything but the per-customer limit.
2. **In the placing transaction**, once the customer is known,
   `RedeemTx` does the rest:
   - it checks every limit; for a per-customer limit, under an advisory
     lock on (store, coupon, customer) held until the commit;
   - it records the order's redemption (`coupon_redemptions`, one per
     order) and takes the use with a conditional update
     (`uses < max_uses`, `active`).

   It is the placing transaction's last step, so the coupon's row is
   locked only until the commit. Concurrent orders take the last uses
   once (`TestLastUseTakenOnce`), one customer's concurrent orders take
   a once-per-customer code once (`TestOncePerCustomerRace`). If the
   terms changed since the quote, the order is refused.
3. **On cancellation before shipping**, the worker releases the order's
   redemption and uncounts the use, once.

## HTTP API (tag `promotions`)

| Operation | Notes |
|---|---|
| `listCoupons` `GET /v1/stores/{store}/coupons` | Newest first, with `uses` |
| `createCoupon` `POST …/coupons` | 409 when the code is taken |
| `getCoupon`, `updateCoupon` (`PUT`), `deleteCoupon` | Every field is replaced on update. The code is fixed once an order held the coupon (409); such a coupon cannot be deleted (409), only turned off |

All need `promotions.manage` (owner, manager) and are audited
(`coupon.created`, `.updated`, `.deleted`).

## Dashboard (6g)

Built to the approved 6g mockups:
- **الكوبونات** (`/stores/{id}/coupons`): each coupon's code, discount, terms,
  uses (a bar when bounded), validity and state. The states are on, off,
  starts on a date, ended, and used up.
- **كوبون جديد / تعديل** (`…/coupons/new`, `…/coupons/{coupon}`):
  - the code, uppercased, or a random one;
  - the kind, and its percent or amount;
  - the terms;
  - the dates, typed in the store's time zone and sent as instants;
  - on or off.

  A live summary and what the shopper sees at checkout come with it. The form
  is checked as the server checks it, and a taken or locked code shows on its
  field. A coupon no order used can be deleted. On a phone the summary leads
  and the buttons stay at the bottom.

## Tables

| Table | Notes |
|---|---|
| `coupons` | Tenant table under RLS. PK `(store_id, id)`; unique `(store_id, code)` serves the checkout's lookup (an index search, pinned by a plan test). `uses` is taken by a conditional update, the order's last step |
| `coupon_redemptions` | Tenant table under RLS. One per order (PK `(store_id, order_id)`): the coupon, the customer, the discount; `released_at` once given back. `(store_id, coupon_id, customer_id)` counts a customer's uses and covers the FK. `app_user` cannot delete |

## Rules worth knowing

- **Testing:**
  - `domain` covers codes, validation, and `Apply` for every kind and
    limit.
  - `app` runs on real PostgreSQL. It covers managing coupons, preview
    and redeem, the per-customer limit, cross-tenant codes, releases,
    and 20 redemptions racing for 5 uses.
  - `httpapi` covers the happy path, validation, authorization, and a
    cross-tenant test for every operation.
  - `acceptance_test.go` runs catalog, orders, promotions and River. A
    coupon's discount reaches the stored order; a second use by the
    customer is refused; a cancellation before shipping gives the use
    back; 5 shoppers race for 1 use.
