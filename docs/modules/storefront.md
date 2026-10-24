# Module: storefront

The storefront module owns the store's **look and pages**: the theme
(colors, font, radius), the **logo**, the **home page**, **landing pages**
and **policies**. Pages are ordered **sections** whose settings follow a JSON
schema per section type; merchants never supply HTML or scripts. Design:
[ADR-0016](../adr/0016-storefront-themes-and-sections.md). Events:
[ADR-0015](../adr/0015-domain-events-and-storefront-reads.md). Rendering
for shoppers, caching and freshness:
[ADR-0018](../adr/0018-storefront-rendering-and-caching.md). Cart, edge
purges and browser budgets:
[ADR-0019](../adr/0019-cart-edge-purge-and-browser-budgets.md). Carts in
Redis, the one-step order form and anti-fake checks:
[ADR-0022](../adr/0022-redis-carts-one-step-orders-anti-fake.md). The
form's shipping and COD-fee line (`POST /order/quote`):
[ADR-0025](../adr/0025-shipping-zones-rates-cod-fee.md).

## Public API (`internal/modules/storefront/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount` | Wiring, and the merchant HTTP API (behind identity's membership middleware) |
| `ThemeChanged`, `PageChanged`, `EventThemeChanged`, `EventPageChanged` | Events the module publishes |
| `PageKind`, `KindHome`, `KindLanding`, `KindPolicy` | Page kinds, as carried by `PageChanged` |
| `NewShop(ShopDeps)`, `Shop.Pages`, `Shop.Assets`, `Shop.Listen` | The storefront role: pages (tenant from host resolution), static files, cache invalidations from Redis |
| `InvalidatingEvents`, `InvalidateSubscriber`, `InvalidateHandler` | The event subscription every role makes, so the worker tells storefront instances which store changed |
| `PurgeSubscriber`, `PurgeHandler`, `PurgeTags` | The edge purge on the same events (`platform/cdn`; Cloudflare when configured) |
| `ShopDeps.Orders` (`orders.Shop`), `ShopDeps.Captcha` | Shoppers' orders through the orders module (adapted inside the module: shoppers only ever read a generic refusal), and Turnstile for risky submissions |
| `AssetsPrefix`, `CacheConfig`, `DefaultCacheConfig` | Shop wiring |

## Events

| Event | Published when |
|---|---|
| `storefront.theme.changed` | The theme is saved, a new logo goes live, or the logo is deleted |
| `storefront.page.changed` (`page_id`, `kind`, `slug`, `previous_slug`, `deleted`) | A home page, landing page or policy is saved or deleted; `previous_slug` when a landing page moved |

The module consumes `catalog.product.changed`, `catalog.product.deleted`,
`catalog.category.changed`, `payments.connection.changed` and its own
two events: the subscriber
`storefront.invalidate_pages` publishes the store ID on the Redis
channel `storefront:invalidate`, and every storefront instance drops that
store's cached pages, and `storefront.purge_edge` purges the edge
cache's pages (`payments.connection.changed` purges the whole store:
every footer lists the payment methods).

## Rendering (`internal/web`)

| Path | Page |
|---|---|
| `/` | Home: the home page's sections |
| `/p/{slug}` | Product: gallery, variant picker, description, schema.org `Product` + `BreadcrumbList` |
| `/c/{slug}` | Category: 24 products per page, `?after=` cursor |
| `/pages/{slug}` | A published landing page's sections |
| `/policies/{slug}` | A published policy |
| `/cart` (GET, POST) | The shopper's cart; POST adds a line |
| `/cart/update` (POST) | Changes a line's quantity (0 removes it) |
| `/order` (POST) | A landing page's one-step order form |
| `/checkout` (GET, POST) | The cart's summary with the order form; POST places the order and empties the cart |
| `/thank-you/{ref}` | An order's summary (`ref` is the order ID; the page shows its short code); for a card order, its payment status, and `?pay=1` sends the shopper on to pay |

Every page (the approved U1 design):
- **Header.** The theme's announcement bar when set; the brand, the
  other language and the cart (icon, with the `cart_n` count). On phones
  the top categories are in a `<details>` menu; on wide screens they are
  a row under the bar, the current page underlined (`aria-current`).
- **Footer.** The store's name and its about text; "Help" (the
  published policies); "Contact us" (WhatsApp as a `wa.me` link, email
  as `mailto:`) when either is set; "Payment methods": cash on delivery,
  plus the card brands of the store's country (Visa · Mastercard, and
  Meeza in Egypt or mada in Saudi Arabia) when card payment is on. That
  check is the cached `cardOffered`, taken once per store view.
- **Product cards.** A gray "sold out" tag, or a red "N% off" tag
  (rounded down) with the sale price in red. On wide screens there is an
  add-to-cart button: a form for a product with one variant, a link to
  the product otherwise, nothing when it is sold out.
- **Sections.** They lay out in one wrapping row: a hero followed by
  promo tiles share a line on wide screens.
- **The default home.** In this order: hero (button to `#s-newest`),
  category tiles, trust badges, newest products, WhatsApp invitation
  (hidden until the store has a number).
- **Not shown yet.** Search and order tracking are in the mockups but
  come with U2d. No control is rendered before it works.

The cart:
- **Redis and limits.** The cart is a Redis hash found by the SHA-256 of
  a token in the `__Host-cart` cookie (`sf:cart:<store>:<hash>`). It lives
  30 days from its last change; 20 per line, 50 lines.
- **Write protection.** Writes are same-origin forms, rate-limited per IP
  and per store, capped at 4 KiB.
- **The badge.** The `cart_n` cookie carries the unit count, so cached
  pages never vary by shopper.

Orders (ADR-0022):
- **The form.** It is the same for every shopper (landing pages stay
  cached). The page script fills the idempotency key; a hidden `website`
  field is the honeypot.
- **Checks, in order.**
  1. Same-origin and size.
  2. The honeypot.
  3. Limits per store, per IP and per phone (soft limits ask for a
     challenge, hard limits refuse).
  4. Turnstile when risky; that response's CSP alone allows Cloudflare.
  5. The orders module (validation, duplicate window, blocked
     customers, stock).

Card payment (ADR-0030):
- **Where it is offered.** Checkout from the cart offers it when the
  store has a gateway connected (`payments.CardCheck`; no keys in this
  role). Landing pages' quick forms stay cash on delivery.
- **The trip.** A card order goes to `/thank-you/{ref}?pay=1`. That page
  sends the shopper on to the API host's `/v1/pay/…`
  (`CHECKOUT_URL`), then to the gateway, and back.
- **Why no redirect from the form.** The form's `form-action 'self'`
  would also bind the redirect chain.
- **What the thank-you page shows.** Paid; not paid yet (it looks again
  every 10 s, with "pay now"); or cancelled after the hour.
- **The quote.** It leaves out the COD fee for card payment.

Languages and responses:
- The store's locale has no prefix; the other language is under `/en`
  or `/ar`. Upper-case slugs redirect (301); anything else is the store's
  404.
- Responses carry `Cache-Control: public, max-age=0, s-maxage=60` and
  `Cache-Tag` (`s-`, `p-`, `pg-`, `c-`).

Styles, scripts and fonts:
- The CSP allows the inlined stylesheet by its sha256 (`platform/csp`).
- Static files are under `/assets/` with content-hashed names: one
  module script and the self-hosted fonts.
- Sources are in `internal/web/assets/src` and are minified by esbuild
  at `make gen`. Font provenance and licenses are in `assets/fonts`.

## Section types

Each type has a schema in `internal/sections/schemas/<type>.json`, with
shared definitions in `defs.json` (IDs, single lines, links). It also
has a Go type in `internal/sections/types.go` (`sections.Decode`).

| Type | Settings |
|---|---|
| `hero` | `badge`, `heading`, `subheading`, `button_label` + `button_link` (a link alone shows "Shop now"), `image_product_id`, `align` |
| `product_grid` | `title`, `source` (newest / category + `category_id` / manual + `product_ids` ≤ 24), `limit` ≤ 24, `layout` (grid / carousel) |
| `product_landing` | `product_id`, `headline`, `highlights` (≤ 6), `show_description`, `cta_label` |
| `reviews` | `title`, `items` (1–20 of `name`, `location`, `rating` 1–5, `text`) |
| `faq` | `title`, `items` (1–30 of `question`, `answer`) |
| `trust_badges` | `items` (1–6 of `icon` from shipping/cod/returns/secure/support/quality, `title`, `text`) |
| `countdown` | `title`, `subtitle`, `ends_at` (RFC 3339), `hide_when_ended` |
| `category_tiles` | `title`, `category_ids` (≤ 12; empty: the top categories), `limit` ≤ 12 |
| `promo_tiles` | `items` (1–3 of `label`, `title`, `link`, `tone` amber/green/blue/primary) |
| `whatsapp_cta` | `title`, `text`; shown only with the theme's WhatsApp number |

**Links** are `/path` on the store, `#s-<section id>` on the page, or `https://…`, and nothing else.
**Empty texts** are filled at render time in the shopper's language.

**Adding a type** takes:
1. a schema;
2. a Go type and a case in `Decode`;
3. a `domain.SectionType` constant;
4. the enum in `api/openapi.yaml`;
5. a templ component;
6. test cases in `sections_test.go`.

## Logo

The logo follows the product image pipeline (ADR-0012, ADR-0017) through
`platform/media`:
1. `createLogoUpload` returns a presigned POST. PNG, JPEG or WebP are
   accepted, at most 2 MiB; SVG and GIF are refused.
2. The browser uploads the file.
3. `completeLogoUpload` queues `storefront.process_asset` on the `media`
   queue, which runs `media.Ingest` (sniff, bound, re-encode).

How the live logo changes:
- A new logo goes live only once it is ready, and the old master is then
  deleted.
- A failed upload keeps the live logo and reports `failure_reason`.
- An upload that is never completed expires after an hour
  (`storefront.expire_asset_upload`).
- Objects are removed by `storefront.delete_objects`, which refuses keys
  outside the store's prefix (`media.DeleteStoreObjects`).

## Tables

Both are tenant-owned with RLS (`db/migrations/00019_storefront.sql`).

| Table | Notes |
|---|---|
| `store_themes` | One row per store (`store_id` PK); `settings` JSON; `version` |
| `store_assets` | `kind` (logo); `pending` → `processing` → `ready` / `failed`; at most one `ready` per (store, kind) (partial unique index); `upload_key`, master `object_key`, size |
| `carts`, `cart_items` | No longer used (carts moved to Redis, ADR-0022); a contract migration drops them |
| `store_pages` | `kind` home/landing/policy; `slug` (text, lower-case) unique per (store, kind); `sections` JSON; `body` (policies); `status`; `version` |

## HTTP API (tag `storefront`, permission `storefront.manage`)

All paths are under `/v1/stores/{store_id}/storefront`.

| Path | Operations |
|---|---|
| `/theme` | `getStorefrontTheme`, `updateStorefrontTheme` |
| `/logo`, `/logo/uploads`, `/logo/uploads/{asset_id}/complete` | `getStoreLogo` (`current` + `latest`), `deleteStoreLogo`, `createLogoUpload`, `completeLogoUpload` |
| `/section-types` | `listSectionTypes`: schemas for the editor |
| `/home` | `getHomePage`, `saveHomePage` |
| `/pages`, `/pages/{page_id}` | landing pages: list (without sections), create, get, update, delete |
| `/policies`, `/policies/{policy}` | `listPolicies` (defaults included), `getPolicy`, `savePolicy` |

How saves work:
- Every save sends the `version` it read; 0 means create. A stale
  version is 409 `version_conflict`.
- Unsaved pages and the theme read as defaults with version 0, and pages
  without an id.
- Validation errors name the field, e.g.
  `sections[1].settings.items[0].rating`.

## Limits

| Limit | Value |
|---|---|
| Sections per page | 30 |
| Sections JSON per page | 64 KiB |
| Landing pages per store | 50 (409 `page_limit`) |
| Title | 200 characters |
| Meta description | 300 characters |
| Policy body | 20,000 characters |

## Tests

| Package | What it covers |
|---|---|
| `domain` | Theme (hex, enums, WCAG contrast, `OnColor`); page rules per kind; section IDs, sizes and field naming; defaults |
| `sections` | Valid and invalid settings for every type, with exact field paths (links, enums, conditionals, `8.0`, date-time forms); defaults validate; `Decode`; `FuzzValidateSettings` |
| `app` (real PostgreSQL) | Versions and conflicts for the theme, home and policies; landing CRUD, slug moves, slug taken, the 50-page limit per store; events and audit entries; isolation between stores; no tenant |
| `app` logo | Upload, process, replace (old master deleted), rejection, abandon, expiry, delete; foreign keys refused; isolation between stores |
| `web` (fakes) | Arabic and English pages, hreflang/canonical, prices, structured data, variant picker, pagination, landing sections (one `ProductsByIDs` call), ended countdowns, escaping of merchant text, the CSP hash of the inline style, the cache and `Invalidate`, HEAD/POST, failures, assets; `BenchmarkProductPage` (p95 warm and cold) |
| `app` cart (real Redis, fake catalog) | Add (stock, availability, cap, 50 lines), update and remove, current prices in one catalog call, drop-outs, isolation between stores, forged tokens |
| `web` cart | Add, update and remove through forms, cookies (`__Host-`, HttpOnly, Secure, Lax), the units cookie, language-aware redirects, CSRF (Origin, Sec-Fetch-Site, Referer), rate limit, oversized forms, error messages, the thank-you page |
| module purge | Event → tags for every invalidating event; failures retried, broken payloads cancelled |
| `web` checkout | The landing form (cacheable, governorates, honeypot, key), placing an order, field errors kept with the values, refusals, honeypot, cross-site posts, the adaptive challenge (soft, hard, limiter down, no keys, Cloudflare down), the phone key, checkout from the cart |
| `storefront` (integration) | A landing order through the real orders, customers and catalog modules: thank-you, a repeated submit, held stock, a blocked phone, another store's host |
| `make loadtest-orders` | k6: 50 orders/s for 60 s against `sfdemo -load` (p95 < 150 ms, no failures) |
| `make storefront-e2e` | Playwright on a Pixel 7 (home → product → landing → one-step order; product → cart → checkout; English mirror; no console errors) and Lighthouse budgets (LCP ≤ 1.5 s, CLS ≤ 0.1, JS ≤ 30 KiB) against `internal/tools/sfdemo` |
| module (real PostgreSQL + Redis) | `TestTwoStoresTwoHosts` (one process, two hosts, two themes, no cross-store leaks) and `TestPriceEditReachesStorefront` (edit → event → worker → Redis → fresh page, under 100 ms) |
| `httpapi` | Happy path, validation, authorization (401/403) and cross-tenant cases for all 17 operations; `TestMain` fails if one has no cross-tenant test |
