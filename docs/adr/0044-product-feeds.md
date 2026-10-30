# ADR-0044: Product feeds for Google and Meta

- Status: Accepted
- Date: 2026-10-29

## Context

P6 asks for product feeds that Google Merchant Center and Meta Commerce
Manager fetch, so a merchant can run shopping and catalog ads. The
approved plan (decision 9) chose a feed streamed on request and kept an
hour at the edge, over a file generated on a schedule to object
storage, with a secret key per store in the URL.

What the feeds list is already public: the active products and their
prices, as the storefront shows them. The key keeps strangers and
crawlers from pulling the whole catalog in one request. It does not
guard a secret.

## Decision

1. **The storefront serves the feeds; marketing owns the key.** Each
   store has `/feeds/google.xml?key=…` and `/feeds/meta.xml?key=…` on its
   own hosts. The feed is rendered next to the product pages: same
   catalog read, URLs, store name and language. The marketing module
   keeps the key (`product_feeds`, one row per store; no row means the
   feeds are off). Its API turns the feeds on, replaces the key, or
   turns them off. The storefront only asks `Feeds.FeedAllowed(ctx,
   key)`, which compares in constant time.

2. **The key is stored as it is**: 160 random bits, base32. The
   dashboard must show the URLs at any time, and the feeds expose
   nothing the pages do not. Hashing it would only force the merchant to
   paste a new URL into both platforms after every visit. A merchant who
   leaks it replaces it.

3. **One RSS 2.0 file with Google's `g:` elements, in two dialects.**
   Meta reads the same format. The only differences:
   - availability is `in_stock` for Google and `in stock` for Meta;
   - Google's feed adds `identifier_exists=no`, since we have no GTIN.

4. **One item per variant**:
   - `g:id` is the variant ID; `item_group_id` is the product's.
   - The title carries the variant's values.
   - The link opens the product page on that variant (`?variant=`; the
     page's script selects it, and the server's render and cache key
     ignore the parameter).
   - A sale is `price` = compare-at with `sale_price` = price.
   - `brand` is the store's name.
   - `size` and `color` come from options with those names (Arabic or
     English).
   - Left out: samples (never sellable) and products without a ready
     picture (both platforms refuse an item without one).
   - At most eleven pictures per item, and at most 24,000 products per
     feed (500 catalog pages).

5. **Pictures are JPEG.** Pages use AVIF and WebP. The ad platforms'
   catalogs take JPEG and PNG for certain, so the catalog signs one
   more imgproxy URL per picture, at the largest preset (1600 px) as
   `jpg`, only on a feed's pages (`StorefrontQuery.Feed`). It adds
   nothing to the pages' hot path.

6. **Streaming, with no cut feed cached anywhere.**
   - The first catalog page is read before anything is sent, so a
     database failure is a plain 503.
   - Each page is then written through a buffer, gzipped when the
     client takes it. Memory stays flat for any catalog size: one
     query per 48 products.
   - A failure after the headers aborts the response
     (`http.ErrAbortHandler`). Neither the edge nor the platform keeps
     a truncated file; the platform refetches later.

7. **Edge cache for an hour, purged on change.** The response is
   `s-maxage=3600` under the tags `s-`, `l-` (every product change
   purges the listings, so the feed too) and `f-<store>`. A new key or
   turning the feeds off publishes `marketing.feed.changed`, which
   purges `f-`, so old URLs stop working at the edge too.

8. **Refusals look alike.** These are all a `no-store` 404, so a
   prober learns nothing:
   - a wrong or missing key;
   - feeds that are off;
   - an unknown feed;
   - any query parameter besides `key` (P6 review): extra parameters
     would make URLs the edge has no copy of, and the origin would
     stream the catalog again for each.

   Requests that reach the origin are limited per IP (20 a minute) and
   per store (120 a minute), through the storefront's shared limiter
   (`allowGuarded`). With the limiter down, a feed is a `no-store` 503,
   never served unguarded. The right key is served from the edge.

## Consequences

- A merchant turns the feeds on in the dashboard (6g) and pastes two URLs
  into Merchant Center and Commerce Manager. Nothing runs on a schedule.
- Large catalogs cost one streamed read per hour per platform, at most.
- The same RSS could serve TikTok's and Snap's catalogs later (both take
  it); not built until asked.
- Unverified against a live Merchant Center or Commerce Manager account
  until the owner has one; built from both platforms' specifications.
