# ADR-0042: Coupons

- Status: Accepted
- Date: 2026-10-29

## Context

P6 asks for coupons: percent off, a fixed amount off, or free shipping,
with limits and an expiry. The owner approved (2026-10-28):

- one coupon per order;
- the discount comes off the items only, never off delivery or the
  cash-on-delivery fee (free shipping waives only the delivery rate);
- an order cancelled before it ships gives its use back.

Orders already price themselves twice: `Quote`, for the storefront's
form, and `PlaceOrder`. Shipping plugs into both through a port.
Modules may not import each other's internals, and a coupon's last use
must not be taken twice by two orders racing for it.

## Decision

1. **A `promotions` module owns coupons.**
   - A coupon has a code: unique per store, upper case, A-Z, 0-9, `-`
     and `_`.
   - It has a kind: `percent` (1–100, rounding down), `fixed` (at most
     the items' subtotal) or `free_shipping`.
   - It may have a minimum subtotal, a start and an end, `max_uses`, and
     `max_per_customer`, where a customer is the customers module's
     record of a phone number.
   - It can be active or not.
   - Merchants manage coupons with `promotions.manage` (owner and
     manager).
2. **Orders reach coupons through a port** (`orders.Coupons`, with
   `Preview` and `RedeemTx`), which `cmd/platform` adapts, as it does for
   shipping. Orders never import promotions. A code that does not apply
   is `orders.CouponRejected`. Orders report it as a field error on
   `coupon_code` whose code is the reason: `not_found`, `inactive`,
   `not_started`, `expired`, `min_subtotal`, `used_up`, `per_customer`.
3. **Two passes, one answer.**
   - `Quote` and the pricing before `PlaceOrder` call `Preview`. That
     checks everything except the per-customer limit, because the
     customer is not known yet.
   - Inside the placing transaction, once the customer is recorded,
     `RedeemTx` checks every limit (the customer's too), records the
     order's redemption, and takes the use, as the transaction's last
     step (P6 review):
     - The use is taken by a conditional update (`uses < max_uses`,
       `active`). The coupon's row is locked only from there to the
       commit, not across the stock reservation and the order's insert.
       A concurrent order waits on that update, reads the count the
       first left, and finds no use (`used_up`).
     - The per-customer limit holds under a transaction-scoped advisory
       lock on (store, coupon, customer): one customer's orders count
       their uses one at a time, and other customers wait for nothing.
   - If the coupon's terms changed in between, the order is refused
     (`coupon_code: changed`) and nothing is placed.
   - A replayed order returns the first one and redeems nothing.
4. **Orders store the discount.** Migration 00050 adds
   `orders.discount_minor` and `coupon_code`, and the total's rule
   becomes `subtotal − discount + shipping + COD fee`.
   - The migration is expand only: the new checks are `NOT VALID`, the
     old rule is dropped from the parent and every partition (as 00029
     did), and old code writes no discount.
   - The order API shows `discount_minor` and `coupon_code`.
   - Staff orders take a `coupon_code` too.
5. **Uses come back on cancellation before shipping.** The worker
   subscribes `promotions.release_coupons` to
   `orders.order.status_changed`. When an order moves from `pending`,
   `confirmed` or `ready_to_ship` to `cancelled`, its redemption is
   released and the use uncounted, once. That covers the merchant, the
   customer's answer, and the order confirmation's deadline (ADR-0040).
6. **A coupon an order held keeps its code** (orders show it), and it
   cannot be deleted; it can only be turned off.

## Consequences

- The last use is taken once: in tests, 20 redemptions raced for 5 uses,
  and 5 orders raced for 1.
- An order cancelled after shipping, or returned, keeps its use: the
  parcel cost the merchant.
- The storefront's coupon field and the thank-you page's discount line
  come in 6d-2.
