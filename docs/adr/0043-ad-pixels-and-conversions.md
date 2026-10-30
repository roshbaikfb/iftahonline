# ADR-0043: Ad pixels, consent, and server-side conversions

- Status: Accepted
- Date: 2026-10-29

## Context

P6 asks for Meta, TikTok and Snap pixels with server-side Conversions
APIs that share an `event_id` with the browser, so each provider counts
a purchase once. The owner approved (2026-10-28) adding:

- Google Ads (the browser tag with enhanced conversions; server-side
  later);
- GA4 (the browser tag plus the Measurement Protocol);
- a closed list of providers: no free-form scripts and no Google Tag
  Manager;
- a cookie consent banner per store, on by default for Saudi stores
  (PDPL).

The storefront's pages are cached for everyone at the edge (ADR-0018).
Their CSP allows scripts from our origin only, and no inline script.
Lighthouse holds the LCP budget at 1.5 s (unchanged).

We have no Meta app, TikTok or Snap account yet. The adapters are built
from the providers' documentation and marked unverified until they run
against a sandbox.

## Decision

1. **A `marketing` module owns the pixels** (6e-1).
   - A store connects each provider once (`store_pixels`, one row per
     provider), by the provider's public IDs:
     - Meta: the pixel ID (digits);
     - TikTok: the pixel code;
     - Snap: the pixel ID (a UUID);
     - Google Ads: `AW-…` plus the purchase conversion's label;
     - GA4: `G-…`.
   - Each provider is a `Spec` in a registry. The spec gives the ID
     patterns, whether the provider takes a test event code, whether it
     has a server API, and the CSP origins its browser tag needs. Adding
     a provider means adding a spec. The API's provider list is
     extensible (ADR-0032 rule).
   - The Conversions API token is sealed with `platform/envelope`. The
     associated data names the module, the provider, the store and the
     row. The token is write-only: the API only says whether one is
     saved, and never logs or audits it.
   - Saves use optimistic locking (`version`). Every change publishes
     `marketing.pixels.changed` in the same transaction.
   - Merchants need `marketing.manage` (owner and manager).
2. **Consent** (`marketing_settings.consent`):
   - `auto`, the default: the storefront asks first where the store's
     country requires it (Saudi Arabia, known from the currency). The
     marketing module does not need the store's country.
   - `required`: always ask first.
   - `off`: never ask.
3. **Pages load the tags from our own script** (6e-2).
   - `px.js`, served from our origin, reads the IDs from data attributes
     and loads each provider's official tag after the page's `load`
     event, and only after consent where consent is needed.
   - The page's CSP adds exactly the origins of the providers the store
     has on. The storefront caches each store's `Browser` view, and drops
     it on `marketing.pixels.changed`, which also purges the edge.
4. **One `event_id` per conversion** (6e-2, 6e-3).
   - Purchase: `purchase:<order ULID>`, rendered on the thank-you page
     and sent by the server.
   - AddToCart: a ULID made by the cart's form post and kept in Redis
     for a minute under the cart's token hash, read once by the cart page
     it redirects to. The shopper never carries it, so cannot name one
     (P6 review).
   - InitiateCheckout: `ic-` and a hash of the cart's token and lines.
     A reload is the same event, and River's uniqueness and the browser
     report it once; another cart is another event (P6 review).
   - ViewContent and PageView are browser-only. Pages are cached, and no
     endpoint takes beacons.
5. **Server events are River jobs** (6e-3). One job per provider and
   event (`marketing.send_conversion`, unique by store, provider and
   `event_id`).
   - Purchase is reported when `orders.order.placed` is delivered, and
     only for an order that has signals. Signals exist only where the
     shopper allowed them, so that is the consent check.
   - AddToCart and InitiateCheckout are enqueued by the storefront when
     it renders the cart page after an add and the checkout page. This
     happens only when the visit may carry signals, and within limits:
     30 per address and 3,000 per store every 10 minutes
     (`conversion:` keys). Past them, or with the limiter down, the
     browser still reports the event and the server does not. Pages are
     public, and every server event costs a job and a call with the
     merchant's token (P6 review).
   - The storefront role holds no keys, by design. Two consequences:
     - A Purchase job carries only its order's ID. The worker reads the
       phone and signals from the order when it sends, so no phone
       number waits in the job table.
     - The cart's and checkout's jobs carry the visit's signals (IP,
       user agent, cookies, click IDs), in the clear, as
       `order_signals` does, until River deletes the job: a day after
       it is sent, three days after it is cancelled or gives up
       (`server.FailedJobRetention`, P6 review; River's default is
       seven), long enough to look into a failure.
   - A refusal for good (4xx except 408 and 429, or TikTok's non-zero
     `code`) cancels the job; anything else is retried 8 times. A pixel
     turned off or removed since is skipped.
   - GA4's client ID is our customer's (or the event's) ID: the GA
     cookie is not read. Server events count, but do not join the
     browser's session; GA4 dedupes the purchase by `transaction_id`.
     Google Ads has no server event yet.
   - Meta: `/{pixel}/events` (the token in the body). TikTok: Events API
     1.3 (the token in a header). Snap: CAPI v3 and GA4's Measurement
     Protocol: their documented query parameters; transport errors drop
     the URL before anything is logged.
   - Phone and e-mail are normalized, then hashed with SHA-256.
   - The order's signals (`fbp`, `fbc`, `ttclid`, `ScCid`, `gclid`, IP,
     user agent) and UTM parameters are written with the order: UTM in
     nullable columns on `orders`, and the signals in `order_signals`,
     partitioned by month and kept one month.

## Consequences

- No merchant code ever runs on a store's pages. A provider we have not
  added cannot be connected.
- A store that turns pixels on loads more script after `load`, and its
  CSP opens to those providers' origins. Stores without pixels are
  unchanged.
- The adapters are unverified until a sandbox. A test event code lets a
  merchant check events in the provider's tool before going live.
- One pixel per provider per store. A second Meta pixel would need a
  list instead of a row.
