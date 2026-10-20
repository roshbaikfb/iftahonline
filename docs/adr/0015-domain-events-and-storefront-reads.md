# ADR-0015: Domain events between modules, and the catalog's storefront read side

- Status: Accepted
- Date: 2026-10-20

## Context

The storefront (P3) renders a store's pages from the catalog and must
show a price change within 60 seconds while serving most traffic from
caches: the edge, and rendered fragments in memory. Caches need to learn
that something changed, and the module that owns the change must not
know who caches it (CLAUDE.md rule 2).

Until now, modules called each other only through `api.go`. ADR-0003
chose River as the transactional outbox, and `platform/event` defined an
envelope, but nothing delivered events. Inside the catalog, the
`catalog.index_product` job was the only "product changed" signal, and
it is private to the search index.

The storefront also needs to read products in ways the merchant API does
not:

- only active, live products;
- lookup by slug;
- a merchant-picked list in a given order;
- signed image URLs;
- no stock levels.

These reads are on the hottest path we have.

## Decision

1. **`platform/eventbus` delivers events through the outbox.**
   - `PublishTx(ctx, tx, ev)` inserts one `event.deliver` River job per
     subscriber of the event's type, in the caller's transaction. An
     event exists exactly when its change commits, and each subscriber
     retries on its own.
   - Subscriptions (`Subscribe(type, subscriber, handler)`) are made at
     startup in `cmd/platform`. Every role builds the same bus: the
     publishing role needs the subscriber list, the worker needs the
     handlers. There is no global registry.
   - Handlers run with the event's store in context (the jobs
     middleware, ADR-0007). They must be idempotent and must not rely on
     order. Payloads carry IDs, not state; a handler that needs state
     reads it through the owner's `api.go`.
   - If a job reaches a worker that lacks the subscription (rolling
     deploy), it is retried. A payload that does not decode is
     cancelled.
   - The envelope's store must equal the tenant in context.
2. **The catalog publishes three events:**
   - `catalog.product.changed`, with a reason: `created`, `updated`,
     `images` or `stock`;
   - `catalog.product.deleted`;
   - `catalog.category.changed` (`created`, `updated` or `deleted`).

   They come from the same places that already reindexed search, plus
   stock. A stock event is published when the merchant sets on-hand, and
   when a reservation, release or expiry makes a variant sell out or come
   back. It is never published per unit, so checkout traffic does not
   flood caches with purges.
3. **`catalog.Storefront` is the read side.**
   - Methods: `ProductBySlug`, `ProductsByIDs` (order kept, at most 50),
     `ListProducts` (newest first, cursor, at most 48) and `Categories`.
   - It returns only active, live products, with `Available` per variant
     instead of stock numbers, and avif/webp renditions at the exact
     pixel size imgproxy serves (never enlarged).
   - Each call runs a constant number of queries: BEGIN, tenant, products,
     variants, images, COMMIT. Tests assert this.
   - `catalog.NewStorefront` needs only the pool and the image delivery
     settings, so the storefront role does not hold S3 credentials or
     the search key.
4. **Slugs are `text`, not `citext`** (migration 00018).
   - Under RLS, PostgreSQL puts only LEAKPROOF operators into index
     conditions. `citext`'s `=` is not leakproof. So `app_user`'s slug
     lookups could not use the `(store_id, slug)` index and filtered the
     whole store: 44 ms on a 200k-product store.
   - Slugs are always lower-case under the domain rules, so `citext`
     bought nothing. A `CHECK (slug = lower(slug))` keeps uniqueness
     case-insensitive in effect. The column change is binary coercible
     (no table rewrite).
   - `TestSlugLookupUsesIndexUnderRLS` captures the real query and
     asserts the index condition as `app_user`.
   - The other `citext` columns are not looked up under RLS:
     - `variants.sku` is used only by its unique index;
     - `users.email` and `platform_admins.email` have no RLS;
     - host resolution runs as the owner, whose policy is `USING (true)`.

## Consequences

- **Positive:**
  - A module can react to another's changes without either importing the
    other's internals, with exactly-once-or-retried delivery tied to
    commit.
  - The storefront's reads never see drafts, other stores' rows or stock
    numbers.
  - Product lookup by slug is an index probe, whatever the store's size.
- **Negative:**
  - One job per subscriber per event adds rows to `river_job`. That is
    fine for catalog edits. High-volume streams (analytics) must not use
    this bus (ADR-0003).
  - A subscriber renamed in code strands jobs queued under the old name
    until their retries run out.
  - A category page whose category holds many drafts and few active
    products walks the drafts before it finds a page of active ones. The
    page is edge- and fragment-cached (P3), so this costs one origin
    query per cache fill. If it shows up in production, denormalize
    `active` into `product_categories`.
- **Measured** (generic plans, `app_user`, 240k products in one store
  plus 2,000 small stores):

  | Query | Time |
  |---|---|
  | slug lookup | 0.19 ms |
  | 50 products by ID | 0.27 ms |
  | 25-product active page | 0.10 ms |
  | 25-product category page (mixed category) | 0.20 ms |
  | reservation UPDATE with product in RETURNING | 0.09 ms |
