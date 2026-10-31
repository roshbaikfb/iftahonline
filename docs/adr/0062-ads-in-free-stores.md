# ADR-0062: Ads in free stores

- Status: Accepted
- Date: 2026-10-31

## Context

The free plan is paid for by ads shown in free stores. The approved
mockups (7g, 2026-10-31) and decisions set the rules:
- the platform's own banners come first, then Google AdSense;
- a store with the `ad_free` feature (pays and every paid plan) shows
  none, at once when it starts paying;
- ads never appear on the cart, checkout, order, tracking or thank-you
  pages, nor in a preview;
- four placements, at most two a page;
- AdSense is non-personalized and lazy-loaded, with no consent dialog;
- house banners count clicks only, per day, with no tracking of shoppers;
- the CSP is widened only on pages that show an AdSense unit;
- each slot reserves its box, so nothing moves when the ad loads;
- free stores carry a privacy line in their footer;
- a change of banners reaches stores within the caches' lifetime (no mass
  purge).

## Decision

1. **A new `ads` module owns the platform's ad data.** It has no store
   column and no RLS, because ads are platform data, like the plans
   catalog:
   - `ad_settings`: one row holding AdSense's on/off switch, publisher and
     a unit per placement;
   - `ad_banners`: title, a re-encoded image under `platform/ads/`, an
     https link, the placement, a country (or every country), a start and
     an optional end, a weight from 1 to 100, and whether it is active;
   - `ad_clicks(banner_id, day, clicks)`, counted with an upsert.

   `app_user` (the storefront) may read the settings and banners and
   count clicks, nothing more. `app_admin` (the console) writes them.
2. **The placements** and their reserved sizes:
   - `home_top`: under the home's first section, 320×100;
   - `grid`: under the home's second section, and after a category's
     fourth card when more follow, 300×250;
   - `category_top`: above a category's products, 320×100;
   - `product_bottom`: under a product's description, 300×250.

   A page asks only for the placements it actually renders, so a short
   page loads no AdSense script and keeps its strict CSP. An empty
   placement keeps no space.
3. **Choosing.** `domain.Choose` picks, per placement, a live banner by
   weight. When there is none, it uses AdSense's unit, if AdSense is on
   and the placement has one. Otherwise the placement shows nothing.
4. **Hot path.** `ads.Shop.Slots(country)` keeps the settings and live
   banners in memory for `CacheTTL` (one minute) per country. Every free
   store of a country shares one read, so the cost per page is a map copy
   and a weighted draw. The draw is made when the store's view is built,
   so it rotates with the view and page caches. The storefront asks
   entitlements `Allowed(ad_free)` once per view. Paying already
   invalidates views (ADR-0050), so the ads go at once. A failure to read
   the plan shows no ads, the safe side.
5. **AdSense.** Pages with a unit load `ads.js` (ours, embedded and
   hashed). It loads `adsbygoogle.js` with `requestNonPersonalizedAds=1`
   when the first slot nears the viewport, and fills each `<ins>`. Only
   these pages add AdSense's origins to `script-src`, `connect-src` and
   `frame-src`. Free stores serve `/ads.txt` naming the publisher. Paid
   stores answer 404 there.
6. **House banners** link to `/ad/{id}` on the store's own host, with
   `rel="sponsored nofollow"`. That route counts the day's click and
   redirects (302, `no-store`, `Referrer-Policy: origin`) to the banner's
   link. A banner no longer on show redirects to the store's home.
   Images go through the media pipeline: a presigned POST of at most
   2 MiB (JPEG, PNG or WebP, never SVG), then `media.Ingest` re-encodes
   them. They are served through imgproxy like product images.
7. **The console** manages it all from `/admin/v1/ads` (settings, an
   image upload, banners, show or stop), each change with a reason in
   the admin log. Its screen is `/console/ads`.
8. **The dashboard's home** tells the owner of a free store that its
   pages show ads, with a link to the plans («أزل الإعلانات»). It reads
   `ad_free` from the usage meters (`billing.manage`), so a member who
   cannot change the plan is not shown it.

## Consequences

- A change of banners or settings shows within a minute, plus the page
  and edge cache lifetime. Stopping a banner takes effect for clicks at
  once.
- Banners are drawn per view build, not per request. Rotation therefore
  follows the caches, which is enough for house ads and keeps pages
  cacheable.
- The storefront benchmark (`BenchmarkProductPage/free-*`) shows that a
  free store's page costs about the same as a paid one.
- AdSense's own terms and its cookie for measurement are disclosed by the
  footer line. A consent dialog would be needed if personalized ads were
  ever turned on, and they are not.
