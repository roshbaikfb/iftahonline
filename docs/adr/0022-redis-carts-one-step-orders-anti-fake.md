# ADR-0022: Carts in Redis, one-step orders, and anti-fake checks

- Status: Accepted
- Date: 2026-10-23
- Supersedes: ADR-0019 §1 (carts in PostgreSQL)

## Context

P4 asks for three things on the storefront:

- **Carts:** a server-side guest cart in Redis with a TTL.
- **A one-step order form** (name, phone, governorate, city, address,
  notes) on cash-on-delivery landing pages.
- **Defences against fake orders:**
  - phone validation;
  - a honeypot;
  - an adaptive CAPTCHA;
  - per-phone and per-IP limits;
  - a duplicate-order window.

ADR-0019 kept carts in PostgreSQL. It left their cleanup open and the
checkout button disabled.

**Two constraints shape the design:**

- **Storefront pages are cached at the edge for everyone** (ADR-0018). A
  landing page cannot carry anything personal or per-request, including
  a server-made idempotency key.
- **Most shoppers order once, on a phone, often on a weak network.**
  Double submits are common; repeated fake orders come from a few
  phones and networks.

## Decision

1. **Carts live in Redis** (`store.Carts`).
   - **One hash per cart**, keyed `sf:cart:<store>:<hex sha256(token)>`.
     The cookie's token is the only key, and Redis never holds a usable
     one. A token opens nothing in another store.
   - **Order and limits.** Lines keep the order they were added in (a
     sequence number per line). A Lua script sets a line and enforces
     the 50-line limit atomically.
   - **Expiry.** Every change renews a 30-day TTL, so untouched carts
     expire without a job.
   - **Unchanged contract.** The cart's behaviour and limits are those
     of ADR-0019. Its tests pass unchanged, now on real Redis.
   - **The old tables.** `carts` and `cart_items` are no longer used. A
     contract migration drops them after a release (expand → migrate →
     contract).
2. **The one-step form.**
   - **Where it appears.** Landing pages' product sections carry the
     whole form and post to `/order`. The cart's button opens
     `/checkout`, the same form under the cart's summary.
   - **Placing the order.** Both go through the orders module
     (`orders.NewShop`, in the storefront role), then redirect to
     `/thank-you/<order id>`. Checkout empties the cart.
   - **Plain HTML.** The form works without JavaScript. Governorates are
     a list (`platform/geo`, from the store's currency).
   - **The idempotency key is made in the browser**
     (`crypto.randomUUID`), because the page is shared. Without script,
     the orders module's 30-minute duplicate window (ADR-0021) still
     holds.
   - **Errors.** The form comes back, private and never cached, with
     the shopper's values and a message per field.
3. **Checks, in order** (`submitOrder`):
   1. Same-origin form posts only, at most 8 KiB.
   2. **Honeypot.** A hidden `website` field, invisible to people and to
      assistive technology. If it is filled, the order is refused like a
      blocked customer's.
   3. **Limits** (`platform/ratelimit`, GCRA on Redis):

      | Subject | Soft limit (challenge) | Hard limit (refuse) |
      |---|---|---|
      | Per store | — | 6,000/min |
      | Per IP | 3 per 10 min | 30 per 10 min |
      | Per phone | 2 per hour | 6 per hour |

      - A phone is keyed by its last 9 digits, so `010…`, `+2010…` and
        Arabic-Indic digits count as one number.
      - Per-IP limits are looser than per-phone ones because carriers
        and offices share addresses.
   4. **The adaptive challenge.** Past a soft limit, or when the limiter
      is unavailable, the form must carry a valid Cloudflare Turnstile
      answer.
      - The first risky submission gets the form back with the widget.
      - Only that private response's CSP allows
        `https://challenges.cloudflare.com` (script and frame). Cached
        pages never do.
      - Verification has a 3 s timeout and 3 attempts that share one
        `idempotency_key`, because a token is single-use.
      - Without Turnstile keys, or if Cloudflare is unreachable, a risky
        submission is refused as "busy" rather than let through.
   5. **The orders module.** Phone validation for the store's country,
      the duplicate window, a blocked customer, stock.
   - **Shoppers never learn why they were refused.** A blocked customer,
     a honeypot and a reused key all read "We could not place this
     order. Please contact the store."
4. **Load test** (`make loadtest-orders`).
   - **Setup.** k6 runs against the demo store in load mode: real
     PostgreSQL and Redis, the production storefront code and
     middleware, and loopback trusted as the edge proxy. Each order is
     its own shopper (phone and `X-Forwarded-For`).
   - **Nothing is relaxed for the test.** No limit is lifted and no
     check is skipped.

## Consequences

- **Positive:**
  - **Fast.** At 50 orders/s, p95 is 18.9 ms with 0 errors (3,000 orders
    on the dev machine). The submit costs 4.4 µs in our own code.
  - **Most shoppers never see a challenge.** Repeat offenders and
    scripted floods do.
  - **Carts clean themselves up**, and the edge cache is untouched by
    carts or orders.
- **Negative:**
  - **Redis now holds state shoppers care about.** If Redis is down,
    carts are unavailable and order submissions need a challenge.
    Landing-page orders still work, but cart checkout does not. Redis
    must run with persistence (AOF), or carts are lost on restart.
  - **One hot product serializes on its stock row** (ADR-0011). At 200
    orders/s on a single product, p95 rose to 186 ms (no errors). A
    flash sale on one item is the limit to watch.
  - **No minimum fill time.** A minimum time to fill the form cannot be
    checked on a cached page (the render time is the cache's).
  - **Without keys, risky orders are refused.** Production needs
    `TURNSTILE_SITE_KEY` and `TURNSTILE_SECRET_KEY`; until they are
    set, a shopper past a soft limit sees "busy" instead of a challenge.
