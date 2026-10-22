# ADR-0019: Carts, edge purges, and browser budgets for the storefront

- Status: Accepted; §1 (carts in PostgreSQL) superseded by [ADR-0022](0022-redis-carts-one-step-orders-anti-fake.md)
- Date: 2026-10-20

## Context

ADR-0018 serves every storefront page from caches: the instance's memory,
and the edge with `s-maxage=60`. Three things were still open:
- **Shoppers need a cart.** Pages must stay identical for everyone or the
  edge cache is worthless.
- **The edge TTL should be minutes, not 60 s**, without breaking the
  promise that a price edit shows within 60 s.
- **The P3 acceptance asks for browser evidence**: a mobile journey (home →
  product → landing page) and a Lighthouse budget (LCP < 1.5 s on
  simulated 4G, JS < 30 KB).

## Decision

1. **Server-side carts in the storefront module.**
   - Tables `carts` and `cart_items`, under RLS.
   - **The cookie is the only key.** A cart is found only by the SHA-256
     of a random 256-bit token (`platform/authtoken`). The token lives in a
     `__Host-cart` cookie (HttpOnly, Secure, SameSite=Lax, path /). The
     database never holds a usable token.
   - **Expiry.** A cart lives 30 days from its last change.
   - **Limits.** 20 per line and 50 lines per cart.
   - **Lines name catalog variants by ID only**, with no foreign key across
     modules. Adding a line checks the catalog: the variant must be for
     sale, and the new quantity must be in stock.
   - **The cart page reads current prices** for every line in one catalog
     call (`SellableVariants`, new on `catalog.Catalog` and
     `catalog.Storefront`). Lines that stop selling drop out; out-of-stock
     ones show as unavailable.
   - **Stock is not reserved in the cart.** Reservation (ADR-0011) happens
     at checkout (P4).
2. **Cart writes are plain forms** (post, then redirect, then get):
   `POST /cart` and `POST /cart/update`, under the language prefix like the
   pages. They work without JavaScript.
   - **Cross-site posts are refused.** `Origin`, or else `Sec-Fetch-Site`,
     or else `Referer` must name the store's host.
   - **Rate limits.** 30 writes per minute per IP and 1,200 per minute per
     store (`platform/ratelimit`). The limiter fails open if Redis is down,
     because a shop that cannot take orders loses more than the limit
     protects.
   - **Forms are capped at 4 KiB.**
   - **Cart pages are never shared.** They are `private, no-store` and
     `noindex`.
3. **Cached pages never vary by shopper.** The header's cart badge reads a
   `cart_n` cookie (the unit count, not a secret) that every cart response
   sets.
   - The alternative was a `/cart.json` call on every page view. It would
     have sent each view to the origin, undoing the edge cache.
4. **Thank-you page.** `/thank-you/{ref}` renders an `OrderSummary` through
   the `storefront.OrderSummaries` port, which the orders module (P4) will
   implement. Until then the page is a 404. Like the cart, it is never
   cached and never indexed.
5. **Purging the edge by tag** (`platform/cdn`).
   - **Every role subscribes `storefront.purge_edge`** to the same events
     as the in-memory invalidation (ADR-0018). The worker maps each event
     to tags:

     | Event | Tags purged |
     |---|---|
     | A product changed or deleted | `p-<product>` and `l-<store>` |
     | A category, the theme, the logo, a policy | `s-<store>` |
     | A home or landing page | `pg-<page>` |

     `l-<store>` is a new tag on every listing page (home, category,
     landing), so a new product reaches pages that do not show it yet.
   - **Cloudflare (purge by tag)** is used when `CLOUDFLARE_ZONE_ID` and
     `CLOUDFLARE_API_TOKEN` are set.
     - Only the worker reads them. The api never holds the token.
     - Tags go in batches of 30.
     - Rate limits and 5xx are retried; a rejected request fails at once.
     - Without credentials the purger is a no-op, and the subscription
       stays so every role's subscriber list matches.
   - **The storefront's `s-maxage` comes from `EDGE_CACHE_TTL`.** The
     default is 60 s, the safe value without purging; it can rise to e.g.
     10 minutes once purging is on.
6. **Browser checks in `make ci`** (`make storefront-e2e`).
   - **The demo store.** `internal/tools/sfdemo` starts throwaway
     PostgreSQL and Redis (`testinfra.Start`). It seeds one store through
     the merchant APIs and serves it with the production middleware at
     `souq.localhost`; Chromium resolves `*.localhost` to the loopback
     address, so host routing is real. It is a test tool and is never
     deployed.
   - **Playwright on a Pixel 7**: home → product (variant price) → landing
     page (countdown, FAQ) → order → cart and badge, plus the English
     mirror. Any console error fails the test, which catches CSP
     violations.
   - **Lighthouse CI** (`@lhci/cli`): mobile with simulated 4G, three runs,
     median.
     - Assertions: LCP ≤ 1.5 s, CLS ≤ 0.1, scripts ≤ 30 KiB,
       accessibility and SEO ≥ 0.9.
     - Reports stay on disk; nothing is uploaded.

## Consequences

- **Positive:**
  - The edge can keep pages for minutes and still show an edit within
    seconds.
  - Carts cost the edge nothing.
  - The P3 acceptance is checked on every change. Measured: product page
    LCP 1.2 s, landing page 1.35 s, performance 100, JS 1.9 KB
    uncompressed.
- **Negative:**
  - **Expired carts stay in the table** until a cleanup job exists.
    Deleting them needs a per-store pass under RLS; it comes with orders
    (P4).
  - **The demo store has no product images** (no imgproxy in CI). On real
    stores the LCP element is usually an image served by imgproxy through
    the edge, which these numbers do not cover.
  - **Purges depend on the worker and on Cloudflare.** If both fail for
    longer than the edge TTL, shoppers see stale pages up to that TTL.
    River keeps retrying the purge.
  - **Checkout is not built yet.** The cart's checkout button is disabled
    until P4.
