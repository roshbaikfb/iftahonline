# ADR-0033: Shoppers' search key, and how bestsellers are counted

- Status: Accepted
- Date: 2026-10-25

## Context

U2d gives the storefront three things the approved mockups show:

- a search box;
- order tracking;
- a "bestsellers" grid.

Two of them raise design questions.

**Search.** Product search exists since P2 (ADR-0013): one Meilisearch
index for every store, written by the worker and searched by the API.
The client adds the store filter itself. The storefront role had no
search at all. It is also the role most exposed to the internet, so we
do not want it to hold the API's key, which can write documents.

**Bestsellers.** The storefront has to know what sold most. The owner
decided the rule:

- units in **confirmed** orders over the last **30 days**;
- shown only once **4** products have sold;
- until then, the newest products, titled "وصل حديثاً".

Summing `order_items` over a month on every home page render would read
many rows per store, across monthly partitions. And the orders tables
belong to the orders module, so the storefront must not query them
(CLAUDE.md rule 2).

## Decision

### Search

1. **The storefront role reads `MEILI_SEARCH_KEY`.** In production this
   is a key limited to the `search` action on the products index; it is
   never the API's `MEILI_API_KEY`. Without the key, the storefront
   offers no search: no box, and `/search` is 404. A key without
   `MEILI_URL` fails at start-up.
2. **`catalog.Storefront.Search`.**
   - It searches active products only. The tenant-safe client adds the
     store filter, and refuses a hit of another store.
   - It reads the hits' products from the database in one query, in the
     index's order. A stale document never shows a product that is gone
     or no longer active.
   - Text is 1 to 100 characters, normalized like the documents; 24
     results a page, at most 20 pages; a 2 s timeout.
3. **`/search` is a public page cached like the others (60 s).**
   - The cache key holds only the normalized text and the page, so
     spellings and junk parameters share one entry.
   - Only a cache miss reaches the index. It is limited to 30 searches a
     minute per IP and 600 per store; a limiter outage lets it through,
     since a search changes nothing.

### Bestsellers

4. **The orders module keeps a counter.** `product_sales` holds units per
   product and day, where the day is the order's creation date (UTC).
   `order_sales_counted` is a ledger of the orders counted.
5. **A subscriber keeps it in line.** `orders.count_sales` subscribes to
   `orders.order.status_changed`. That event is published in the
   transition's own transaction (the outbox), so no change is missed.
   The job:
   - locks the order;
   - compares its *current* status with the ledger;
   - adds or takes back the order's units, and updates the ledger.

   An order counts while it is confirmed, ready to ship, shipped or
   delivered; never while pending, cancelled, failed or returned.
   Because the job reads the current state, a retry or an event seen out
   of order changes nothing more. A cancellation takes its units back
   from the day it added them to.
6. **`orders.Shop.Bestsellers(ctx, limit)`.** It returns product IDs by
   units over the last 30 days, in one indexed query
   (`product_sales_store_day`). Days older than 40 are pruned as the
   counter runs.
7. **The storefront's `bestsellers` grid source.**
   - It asks for these IDs and adds them to the page's single catalog
     call by ID.
   - It keeps those still for sale. With fewer than 4, it shows the
     newest, titled "وصل حديثاً", without an extra query.
   - If the orders call fails, the grid falls back the same way instead
     of failing the page.

## Consequences

- **Search depends on Meilisearch at request time,** but only on cache
  misses, under limits, with a timeout. An outage shows the store's
  "unavailable" page for searches only.
- **Production needs a second Meilisearch key** with the `search` action
  only. In development the master key serves both.
- **Bestsellers lag a little.** A confirmation reaches the counter when
  its job runs, and a cached page shows it within the cache's TTL.
  Nothing purges pages on a sale, so sales never churn the cache.
- **No dashboard switch yet.** `product_grid` accepts the new `source`
  value inside its settings schema; the API's enums are unchanged. The
  section editor (U6) will offer it.
