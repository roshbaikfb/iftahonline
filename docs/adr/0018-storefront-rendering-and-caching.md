# ADR-0018: Storefront rendering, caching and freshness

- Status: Accepted
- Date: 2026-10-20

## Context

The storefront serves 3,000 rps at peak, 90% from the edge cache. That
leaves about 300 rps at the origin, spread over 5,000 stores. The
targets:
- LCP under 1.5 s on simulated 4G;
- server render p95 under 20 ms with warm caches;
- JavaScript under 30 KB gzipped;
- a strict CSP;
- Arabic RTL first, English too;
- a price edit visible within 60 s.

Merchants supply no HTML (ADR-0016), so every page is built from their
theme, their page sections and the catalog's read side (ADR-0015).

## Decision

1. **Server-rendered HTML with templ**, in
   `storefront/internal/web`. It reads through ports:
   - `catalog.Storefront`;
   - `tenancy.Service.GetStore`;
   - the storefront's own `ShopContent`, `GetHome`, `PublishedLanding`
     and `PublishedPolicy`.

   Everything runs in the same binary, with no HTTP hop. Routes:
   - `/` home;
   - `/p/{slug}` product;
   - `/c/{slug}` category, with an `?after=` cursor;
   - `/pages/{slug}` landing;
   - `/policies/{slug}` policy.

   An upper-case slug is a 301 to its lower-case form. Anything else is
   the store's 404 page.
2. **Languages.**
   - The store's locale is served without a prefix and the other
     language under `/en` or `/ar`, with `hreflang` and canonical links
     on every page.
   - Interface strings come from one table holding Arabic and English
     side by side. Merchant text is shown as written; empty defaults
     (hero heading, badge titles, policy titles) are filled in the
     shopper's language.
   - Prices use Western digits with the currency in each language's
     usual form ("1,250 ج.م", "EGP 1,250").
3. **Structured data:** schema.org `Product` (`Offer` or
   `AggregateOffer`, with availability) and `BreadcrumbList` on product
   pages. It is JSON from `encoding/json`, which escapes `<` and `>`.
4. **CSS, JS and fonts.**
   - The store's palette (validated hex values) and our base CSS (5 KB
     minified, 1.6 KB gzipped) are inlined in one `<style>` element. The
     CSP allows it by its `sha256`, computed per store view, not by a
     nonce: the same page always has the same hash, so it stays
     cacheable at the edge. `platform/csp` builds both the default and
     the per-page policy.
   - One ES module of 1 KB (533 B gzipped) adds the countdown tick and
     the variant picker's price. Every page works without it. FAQ uses
     `<details>`.
   - Fonts are self-hosted WOFF2 files (Cairo, Tajawal, IBM Plex Sans
     Arabic, Noto Kufi Arabic; OFL), in Arabic and Latin subsets. Only
     the theme's font is declared, and its regular weight in the
     shopper's script is preloaded. The `system` font downloads nothing.
     *Amended 2026-10-29:* both weights in the shopper's script are
     preloaded (headings and labels are bold, and a face found only in
     the stylesheet cost the largest paint a round trip). Iftah Sans
     Arabic's Arabic files also hold ASCII and the common symbols, and
     their face is declared after the Latin one, so a page in either
     language fetches two font files, not four. Late Latin files had
     made the Lighthouse LCP swing between 1.35 s and 1.5 s.
   - Scripts and fonts are embedded in the binary under content-hashed
     names and served with `immutable` caching. esbuild (a Go tool,
     build time only) minifies the sources at `make gen`.
   - The first two images of a page load eagerly with
     `fetchpriority="high"`; the rest load lazily. Images use
     `<picture>` with AVIF and WebP srcsets at the sizes imgproxy really
     serves.
5. **Caching at the origin.**
   - Pages carry no per-shopper state, so a whole rendered response
     (body, style hash, cache tags) is cached in memory.
   - The store view (store, theme CSS, logo, categories, published
     policies) is cached separately and shared by every page and
     language.
   - Both caches are `expirable.LRU` instances, bounded by entry count
     (2,000 pages of at most 256 KiB, 5,000 views), with a 30 s TTL.
   - Keys include the store's **generation**. `Invalidate(store)` bumps
     it, so the store's old entries are never served again and age out.
6. **Freshness.**
   - Every role subscribes `storefront.invalidate_pages` to
     `catalog.product.changed`, `catalog.product.deleted`,
     `catalog.category.changed`, `storefront.theme.changed` and
     `storefront.page.changed` (ADR-0015).
   - The worker's delivery publishes the store ID on the
     `storefront:invalidate` Redis channel. Every storefront instance
     listens (`redisx.Subscribe`) and invalidates that store.
   - A lost message costs at most the TTL (30 s), which keeps the 60 s
     promise without the event path. In the acceptance test the event
     path takes under 100 ms.
7. **The edge.**
   - Pages send `Cache-Tag: s-<store>,p-<product>…,pg-<page>,c-<category>`.
   - `Cache-Control: public, max-age=0, s-maxage=60` until edge purges
     exist, so even the edge stays within 60 s. A 404 is cached for
     30 s; a failure is `no-store` with `Retry-After`.
   - Purging by tag on the same events, with a longer `s-maxage`, is
     the next change (P3 PR 3d).
8. **Isolation and safety.**
   - The tenant comes only from host resolution (ADR-0004).
   - Every read is tenant-scoped under RLS.
   - Merchant text is always escaped by templ.
   - Button links render only if they are store paths or `https://`;
     anything else becomes `#`.
   - Raw HTML comes only from our own constants (badge icons) and from
     the two elements built from validated data (style, structured
     data).

## Consequences

- **Positive:**
  - A product page renders in about 25 µs from memory and about 30 µs
    from scratch over the fakes (`BenchmarkProductPage`: p95 0.05 ms
    warm, 0.04 ms cold). The 20 ms budget is left almost entirely to
    the database on a cold miss.
  - No third-party request from a shopper's browser, and no inline
    script.
  - One process serves any number of stores: everything per store is
    keyed by tenant.
- **Negative:**
  - An instance's caches hold up to about 64 MiB of pages; size the
    instance's `GOMEMLIMIT` with that in mind.
  - Edits show on other instances within the event delay. Without
    Redis they show within the TTL, which applies to every change,
    including the store's name (tenancy publishes no events yet).
  - Stock shown on cached pages can be up to one cache period old
    between sell-out events. The cart checks real stock (ADR-0011).
  - The buy form posts to `/cart`, which arrives with the cart (3d).
