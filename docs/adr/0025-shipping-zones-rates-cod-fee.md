# ADR-0025: Shipping zones, rates, free shipping and the COD fee

- Status: Accepted
- Date: 2026-10-23

## Context

Until now orders shipped for 0 (ADR-0021 §8). P5 asks for:

- shipping zones by governorate and city, with rates;
- free-shipping rules;
- a cash-on-delivery fee option.

Egyptian and Gulf merchants price delivery the way their courier bills
them: a flat price for a group of governorates (Cairo and Giza, the
Delta, Upper Egypt…), a few cities priced differently (a far district, a
new city), free delivery above an order value, and often a fee for
collecting cash.

Constraints:

- Quotes run on every order and every form update, on the storefront
  path (3,000 rps peak).
- Shoppers type city names freely: المعادي / المعادى, with or without
  hamza.
- Landing pages are edge-cached for everyone, so a page cannot carry a
  shopper's quote.

## Decision

1. **A `shipping` module owns the rules.**
   - **Zones.** A zone groups governorates (platform/geo codes of the
     store's country) under a flat `rate_minor`.
   - **One zone per governorate**, enforced by the primary key
     `(store_id, area)`. Zones need a governorate each, so a store has
     at most as many zones as its country has governorates (27 in
     Egypt); there is no separate limit.
   - **City rates.** A zone may give a city its own rate (≤ 200 per
     zone). Cities match on their search form (`arabic.NormalizeSearch`),
     however the shopper spells them.
   - **Free shipping.** A free-shipping threshold on the subtotal is set
     per store, and a zone may override it.
   - **The COD fee** is set per store, and a zone may override it:
     - a fixed amount plus basis points (≤ 10%) of the subtotal plus
       shipping, i.e. what the courier collects;
     - clamped between a minimum and a maximum;
     - rounded half up.
   - **A store without zones delivers everywhere for 0** (its COD fee
     still applies), exactly as before. Once it has zones, governorates
     outside them are not delivered.
   - Weight-based rates are left out (decided with the plan).
2. **Quotes come from a cached config.**
   - A store's config is its settings and zones with their areas and
     cities. It loads in four indexed queries however many zones there
     are; the query count and plans are tested.
   - It is cached in memory per instance: an LRU of 5,000 stores with a
     30 s TTL. Entries are keyed by generation, like the storefront's
     caches (ADR-0018).
   - Every change publishes `shipping.rates.changed` in its
     transaction. Its subscriber publishes the store on the Redis
     channel `shipping:invalidate`, and every role that quotes listens
     (`Module.Listen`). A lost message costs at most the TTL.
   - The writing instance drops its own entry at once.
   - The quote itself is pure arithmetic: about 0.6 µs and 2
     allocations with 27 zones and 200 cities (`BenchmarkQuote`).
3. **Orders are charged what the quote says.**
   - `PlaceOrder` prices the lines, then quotes the delivery for the
     governorate, city and subtotal.
   - A governorate outside the zones is a validation error on
     `governorate`, code `not_delivered`, before anything is written.
   - **Fixed at placement.** `shipping_minor` and the new
     `cod_fee_minor` (migration 00029) are fixed when the order is
     placed, and `total_minor` includes both (new CHECK
     `orders_total_with_fees`). A later rate change does not touch
     placed orders.
   - **Staff orders too.** Staff entering phone orders are charged the
     same way, since the courier bills the merchant the same.
   - **Expand only.** The column defaults to 0 and the new CHECK
     accepts old writers. Partitions may hold a local copy of the old
     CHECK (they are built standalone and attached), so the migration
     drops it on each one.
   - The order API gains `cod_fee_minor`, and the CSV export gains a
     `cod_fee` column at the end, so imports keyed by position keep
     working.
4. **The shopper sees the cost before submitting.**
   - `orders.Shop.Quote` prices an order exactly as `PlaceOrder` will.
   - **`POST /order/quote`** on the store host answers the one-step
     form's script with one line in the page's language, e.g. «الشحن:
     40 ج.م · رسوم الدفع عند الاستلام: 10 ج.م · الإجمالي: 500 ج.م», or
     «لا نوصّل إلى هذه المحافظة حاليًا.».
     - It is called when the governorate, city, variant or quantity
       changes.
     - Only the address and the product are sent, never the name or
       phone.
     - The landing form quotes its variant; checkout quotes the cart.
     - The answer is `private, no-store`, same-origin only, and limited
       to 60/min per IP (a limiter outage still answers, since a quote
       changes nothing).
   - Cached pages stay identical for everyone; nothing about shipping
     is in them, so rate changes need no edge purge.
   - Without script, the order still shows its charges on the thank-you
     page: subtotal, shipping, COD fee, total.
5. **Permissions.** `shipping.read` goes to every role (support quotes
   callers, shipping staff plan rounds); `shipping.manage` to the owner
   and manager.

## Consequences

- **Positive:**
  - Merchants model their courier's price list: zones, city
    exceptions, free shipping and the COD fee.
  - Shoppers see the final amount before they order, and the order
    carries the same amounts as the quote.
  - Quotes cost no query on a warm cache and stay correct within
    seconds of a change everywhere.
- **Negative:**
  - **A quote may lag a change** by up to 30 s on an instance that
    missed the invalidation. An order placed in that window uses the
    old rate.
  - **Adding the first zone stops delivery** to every governorate not
    in a zone. The dashboard (5f) must make this obvious.
  - **The quote line needs JavaScript.** Shoppers without it learn the
    charges on the thank-you page.
  - **City matching is exact on the search form.** A typo («المعادة»)
    falls back to the zone's rate, which is the safe side for a
    shopper.
  - **The new CHECK scans every orders partition once.** That is
    acceptable before launch; later changes of this kind need NOT VALID
    plus VALIDATE per partition.
