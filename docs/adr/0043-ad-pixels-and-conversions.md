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
   - AddToCart: a ULID made by the cart handler, rendered on the private
     cart response and sent by the server.
   - InitiateCheckout: made by the checkout page (private), sent by both.
   - ViewContent and PageView are browser-only. Pages are cached, and no
     endpoint takes beacons.
5. **Server events are River jobs** (6e-3). One job per provider and
   event, inserted in the transaction of the state change.
   - Meta: `/{pixel}/events`. TikTok: Events API 1.3. Snap: CAPI v3. GA4:
     Measurement Protocol.
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
