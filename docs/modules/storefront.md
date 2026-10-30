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
| `Module.Starter()` → `Starter`, `StarterLook`, `Colors`, `Font`, `Radius`, `Look`, `Section` | What the setup wizard's starter template uses ([ADR-0034](../adr/0034-setup-wizard-and-starter-templates.md)): `ApplyLook` sets the theme's look and the home page, keeping what the merchant wrote; `EnsureDraftPolicy` saves a draft policy unless the merchant saved that one |

## Events

| Event | Published when |
|---|---|
| `storefront.theme.changed` | The theme is saved, a new logo goes live, or the logo is deleted |
| `storefront.page.changed` (`page_id`, `kind`, `slug`, `previous_slug`, `deleted`) | A home page, landing page or policy is saved or deleted; `previous_slug` when a landing page moved |

Checkout starts on card when the store chose it and takes cards
(`payments.CardCheck.CardDefault`, ADR-0036); otherwise on cash on
delivery.

The module consumes `catalog.product.changed`, `catalog.product.deleted`,
`catalog.category.changed`, `catalog.size_chart.changed`, `payments.connection.changed`, `payments.settings.changed`,
`marketing.pixels.changed`, `marketing.feed.changed` (purge only: the feeds) and its own
two events: the subscriber
`storefront.invalidate_pages` publishes the store ID on the Redis
channel `storefront:invalidate`, and every storefront instance drops that
store's cached pages, and `storefront.purge_edge` purges the edge
cache's pages (`payments.connection.changed` purges the whole store:
every footer lists the payment methods; so does a size chart's change,
whose event does not list the products using it).

## Rendering (`internal/web`)

| Path | Page |
|---|---|
| `/` | Home: the home page's sections |
| `/p/{slug}` | Product (U2b): gallery, choices, delivery/payment/returns box, description, related products, sticky bar; schema.org `Product` + `BreadcrumbList` |
| `/c/{slug}` | Category: 24 products per page, `?after=` cursor |
| `/pages/{slug}` | A published landing page's sections |
| `/policies/{slug}` | A published policy |
| `/search?q=&page=` | Shoppers' search (only with `MEILI_SEARCH_KEY`): cards, the count, the next page; nothing found lists the categories; `noindex` |
| `/track` (GET, POST) | Order tracking. The form asks the order number and the phone; POST shows where the order stands (five steps, or cancelled, not delivered, returned) and its parcel. It is private and never cached, and never shows back the phone, the name or the address. It is limited to 10 tries per IP and 300 per store every 10 minutes; a limiter outage refuses. It is linked from the desktop header, the footer and the thank-you page. |
| `/o/{token}` (GET, POST) | The SMS link of an order confirmation (ADR-0040). GET shows the order (code, total, first name) and two buttons; a same-origin POST `answer=confirm\|cancel` settles it through `messaging.ConfirmLinks` (actor `customer:sms`); afterwards the page says how it settled. Private, never cached, `noindex`; an unknown token, or another store's, is 404. Limited to 20 per IP and 600 per store every 10 minutes; a limiter outage refuses. |
| `/thank-you/{ref}` | Also offers "confirm on WhatsApp" (ADR-0041) for a cash-on-delivery order waiting for its answer, when the platform has `WHATSAPP_DISPLAY_NUMBER`: a `wa.me` link with the ready text carrying the order's code |
| `/cart` (GET, POST) | The shopper's cart; POST adds a line |
| `/cart/update` (POST) | Changes a line's quantity (0 removes it) |
| `/order` (POST) | A landing page's one-step order form |
| `/checkout` (GET, POST) | The cart's summary with the order form; POST places the order and empties the cart. The form has a coupon field (ADR-0042): a code the order refuses comes back on it with why |
| `/thank-you/{ref}` | An order's summary (`ref` is the order ID; the page shows its short code); for a card order, its payment status, and `?pay=1` sends the shopper on to pay |
| any page `?preview=<token>` | With a live preview link (ADR-0036): the draft theme and home page, a «معاينة» banner, links that keep the token, `private, no-store`, `noindex`; never cached. A dead token shows the published page |

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
- **Search.**
  - **Where the box is.** Under the bar on phones, in it on wide screens; a plain GET form.
  - **Cache key.** It holds the normalized text and the page, so spellings and junk parameters share one entry, cached like other public pages.
  - **Limits.** A miss asks the index under limits (30 a minute per IP, 600 per store); a cached search is never limited.
- **Language.** The footer links the other language on every page (the
  product page's short header on phones has no switch).

The product page (U2b, the approved mockup):
- **Phones.** A short header: back to the product's category (or home),
  the brand, share and the cart. A bar fixed at the bottom holds the
  WhatsApp order button (only with a number; the message carries the
  product, choice, quantity and link) and "add to cart · total". Wide
  screens keep the full header, in two columns, with the button in the
  form.
- **Gallery.** It scrolls sideways and snaps; the dots and thumbnails
  are anchors, so they work without script. At most 9 pictures.
- **Price and stock.** The price is red on sale, with the old price and
  "وفّر X". "باقي N قطع فقط" shows within the store's `low_stock`
  threshold: the catalog never reports above 10, and the variants'
  JSON carries only the counts the page would show.
- **Choices.** Each option is a group of radio buttons. Colour options
  (اللون، color…) show swatches for 21 common colour names: classes in
  site.css, since the CSP forbids inline styles; an unknown name is a
  text button. A value no available variant has is disabled; the script
  strikes the ones the other choices rule out.
- **The form.** It posts the product and its values (`o0`–`o2`), and
  the cart resolves the variant, so it works without script. With
  script, it follows the choice: price, saving, stock line, total and
  WhatsApp text.
- **The box.**
  - Delivery: the governorate list ("اختر محافظتك لمعرفة الشحن وموعد
    التوصيل"; no governorate is assumed); choosing one asks
    `/order/quote` for the delivery and shipping lines.
  - Payment: COD, and cards only when they are on.
  - Returns: only when the refund policy is published, with its link.
- **Size chart.** When the product has one (ADR-0035): «جدول المقاسات»,
  closed, a table in centimetres with the size as each row's header,
  scrolling sideways on phones.
- **Colours.** A colour the merchant set paints its swatch through a
  second inline `<style>` of the page's own rules (`.swc-<option>-<value>`),
  which the CSP allows by its own hash; only `#rrggbb` is ever written.
  Without one the swatch is guessed from the name.
- **Related products.** The first category's newest (one query, at most
  4). The page carries their `p-` tags, not the listing tag, so a store's
  every edit does not purge every product page.

Cart, checkout and thank-you (U2c, the approved mockups; the cart has
no mockup and follows the same system):
- **Layout.** White cards on a tinted background. The pages link
  `checkout.css` (5 KB) besides the inlined base, and checkout loads
  `checkout.js` (1.6 KB: the totals card, the coupon, the payment
  choice) besides `app.js`, so the pages shoppers land on keep a small
  first paint.
- **Cart.** Each line is a card: its picture (the product's first ready
  image, from the same single `SellableVariants` query), the title, the
  choice, the price, and a stepper of submit buttons (− to 0 removes
  it). Then the subtotal with the item count, and checkout.
- **Checkout (phones).** A short header: back to the cart, "إتمام الطلب"
  and a lock.
- **Checkout, top to bottom:**
  - the summary, opened on demand, with pictures;
  - the delivery details (address hint, notes example);
  - the payment method as cards: the card first with the country's
    brands, cash on delivery chosen by default;
  - the totals card;
  - the button and the note under it, which follow the method.
- **Coupons** (ADR-0042). The quote takes the form's `coupon`. A code
  that applies adds a `discount` line to `amounts` and
  `coupon: {ok: true, code, message}`. One that does not answers
  `coupon: {ok: false, message}` with the reason in the page's language
  (not found, inactive, not started, expired, below its minimum, used
  up, already used by you, changed), and the amounts are without it. The
  "Apply" button and the field's change quote again. The thank-you page
  shows the discount with its code.
- **The totals card.** It fills from `/order/quote`'s `amounts`
  (subtotal, "الشحن إلى …", shipping or free, COD fee, total), and the
  cash choice's note gets its fee.
- **Thank-you.** The page's link may be shared, so it says:
  - the first name only, and where the order goes as city and
    governorate (never the street or the phone);
  - the payment chip, or pay now;
  - what happens next in honest words (no WhatsApp tracking promise
    before messaging exists; delivery days when the zone has them,
    asked through today's quote);
  - the summary with pictures, and a WhatsApp question with the order
    number when the store has a number.
- **Static files.** The script and stylesheets are gzipped once at
  start-up; fonts are already compressed.

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
- Static files are under `/assets/` with content-hashed names: the
  module scripts (`app.js` everywhere, `checkout.js` on checkout) and
  the self-hosted fonts.
- A page preloads both weights of the theme font in its script. Iftah
  Sans Arabic's Arabic files hold ASCII too, so its pages fetch two font
  files in either language (`assets/fonts/README.md`).
- Sources are in `internal/web/assets/src` and are minified by esbuild
  at `make gen`. Font provenance and licenses are in `assets/fonts`.

Ad pixels (ADR-0043, `internal/web/pixels.go`). The storefront reads the
store's pixels from `marketing.Pixels`, once per store view (dropped on
`marketing.pixels.changed`, which also purges the edge). If the read
fails, the view has no pixels; the pages still render.

On a store with pixels:
- **What every page carries:**
  - a JSON data block, `<script type="application/json" id="px">`,
    with each provider's public IDs, whether to ask first, and the
    page's conversions;
  - `px.js` (3.8 KB, from our origin).

  `px.js` loads each provider's official tag after `load`, when the
  browser is idle. Google Ads and GA4 share one gtag.
- **The CSP** adds exactly those providers' origins to `script-src`,
  `connect-src` and `frame-src`. `platform/csp` drops any source that
  is not a plain https origin.
- **Consent.** The page asks first when the mode is `required`, or
  `auto` in a Saudi store. A bottom banner (fixed, so it shifts nothing)
  stays hidden until `px.js` shows it, and the choice is kept on the
  device. Declined, no tag loads.
- **Conversions.** Each one's `event_id` is the one the server sends
  (6e-3). Item IDs are variant IDs.

  | Event | Page | `event_id` |
  |---|---|---|
  | ViewContent | product | none (browser only) |
  | AddToCart | the cart page after an add | a ULID the cart's form post made, kept in Redis under the cart's token hash (`sf:px-add:<store>:<hash>`, 60 s, read once) |
  | InitiateCheckout | checkout | `ic-` + a hash of the cart's token and lines: the same on a reload |
  | Purchase | thank-you (not for a cancelled order) | `purchase:<order ID>` |

  A reload sends no conversion twice: `px.js` remembers the last 20
  `event_id`s on the device. The server's copies are limited to 30 per
  address and 3,000 per store every 10 minutes.

A store without pixels carries none of this, and its CSP is unchanged.

**Where an order came from** (6e-2b). Pages are cached, so the server
never sees the URL a shopper landed on. Instead:
- `app.js` (every store) keeps the landing URL's UTM parameters and ad
  click IDs (`gclid`, `ttclid`, `ScCid`, `fbclid`, plus the click's time)
  in the `ift_src` cookie for 30 days; a later campaign's link replaces
  them.
- `px.js` writes the shopper's consent answer to the `px_c` cookie.

When an order is placed, `Visit` carries:
- **always**, the UTM parameters;
- **only on a store with pixels, and with consent where it asks for
  it**, the providers' cookies (`_fbp`, `_fbc`, `_ttp`), the click IDs,
  the IP address and the user agent. When Meta's tag has not written
  `_fbc` yet, `fbc` is built from the click.

Orders keep the UTM on the order and the rest in `order_signals`, for
one month.

**Product feeds** (ADR-0044, `internal/web/feed.go`).
`/feeds/google.xml?key=…` and `/feeds/meta.xml?key=…` serve the store's
products as RSS 2.0 with Google's `g:` elements, one item per variant,
for Google Merchant Center and Meta Commerce Manager:
- **The key.** It is checked with `marketing.Feeds.FeedAllowed` before
  anything is read. A wrong or missing key, feeds that are off, and an
  unknown feed are all a `no-store` 404, and so is any query parameter
  besides `key`. Requests are limited per IP (`feed:ip:`, 20 a minute)
  and per store (`feed:store:`, 120 a minute); with the limiter down, a
  `no-store` 503.
- **Streaming.** The feed streams a catalog page at a time
  (`StorefrontQuery.Feed`: the same products, with each picture's JPEG),
  gzipped when the client takes it. A failure on the first page is a
  503; a later one aborts the response.
- **Edge cache.** One hour (`s-maxage=3600`), tagged `s-`, `l-` and
  `f-<store>`. `marketing.feed.changed` purges `f-`.
- **The product page.** It opens on the variant `?variant=<id>` names
  (`app.js`): the feeds link each item there.

## Section types

Each type has a schema in `internal/sections/schemas/<type>.json`, with
shared definitions in `defs.json` (IDs, single lines, links). It also
has a Go type in `internal/sections/types.go` (`sections.Decode`).

The list is extensible within /v1 (ADR-0032). Clients must tolerate a
type they do not know. A new type adds its exact oasdiff findings to
`api/openapi-breaking-ignore.txt` in the same PR.

| Type | Settings |
|---|---|
| `hero` | `badge`, `heading`, `subheading`, `button_label` + `button_link` (a link alone shows "Shop now"), `image_product_id`, `align` |
| `product_grid` | `title`, `source` (newest / bestsellers / category + `category_id` / manual + `product_ids` ≤ 24), `limit` ≤ 24, `layout` (grid / carousel). Bestsellers show once 4 products for sale have sold in confirmed orders over 30 days; until then the newest, titled "وصل حديثاً" ([ADR-0033](../adr/0033-shop-search-and-bestsellers.md)) |
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
| `/draft`, `/draft/theme`, `/draft/theme/publish`, `/draft/home`, `/draft/home/publish` | `getStorefrontDraft`, `saveThemeDraft`, `discardThemeDraft`, `publishThemeDraft`, `saveHomeDraft`, `discardHomeDraft`, `publishHomeDraft` ([ADR-0036](../adr/0036-storefront-drafts-and-preview.md)): edits kept apart from what shoppers see until published over the version read |
| `/previews` | `createStorefrontPreview`: a 30-minute `?preview=<token>` link to the shop with the drafts (hash stored, ≤ 20 live per store) |
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
| Low-stock threshold (`low_stock`) | 0 (off) to 10, default 5; the catalog never reports more than 10 units left |
| Look (`look`) | `classic`, `modern` or `soft`, chosen in the setup wizard ([ADR-0034](../adr/0034-setup-wizard-and-starter-templates.md)); absent is the original layout. The body carries `data-look`, and `site.css` does the rest: classic centres the logo on its own row, modern shows larger product cards (fewer to a row), soft draws categories as circles on phones too |

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
