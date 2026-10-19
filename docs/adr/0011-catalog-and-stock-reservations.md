# ADR-0011: Catalog data model and race-safe stock reservations

- Status: Accepted
- Date: 2026-10-19

## Context

P2 adds the catalog: products with options and variants, SKUs, stock,
categories, soft delete and per-store unique slugs. Checkout (P3) must be
able to hold stock while a shopper pays, without ever overselling, at
storefront peak (3,000 rps, flash sales on one product). The dashboard
lists products with their variants, stock and categories, and must not
degrade into N+1 queries as pages grow.

## Decision

1. **Tables** (all tenant-owned: `store_id`, RLS, ADR-0002):
   - `products`: `options` is a JSONB array of `{name, values}` (at most
     3 options, 50 values each). Options are always read with their
     product, so a separate table would only add a query.
   - `variants`: one row per sellable combination; `option_values
     text[]`, `price_minor bigint`, optional `compare_at_minor`, and
     `currency` copied from the store at creation, so reads never need
     the tenancy module.
   - `inventory_levels`: one row per variant (single location for now),
     kept apart from `variants` so hot stock updates do not rewrite the
     wider row.
   - `stock_reservations`: one row per (ref, variant).
   - `categories` (a tree, at most 3 levels) and `product_categories`.
2. **Composite foreign keys** `(store_id, parent_id) → parent(store_id,
   id)` on every child table. A row can never point at another store's
   product, variant or category, even if application code passed a
   foreign ID: PostgreSQL rejects it.
3. **Soft delete** for products and variants (`deleted_at`). Slug and SKU
   uniqueness are partial unique indexes over live rows, so deleting
   frees them. Orders (P3) can still reference deleted variants.
4. **Limits:** ≤ 3 options, ≤ 100 variants and ≤ 20 categories per
   product; ≤ 500 categories per store; one currency per store.
5. **Reservation is one conditional UPDATE per line:**

   ```sql
   UPDATE inventory_levels SET reserved = reserved + $qty
   WHERE store_id = $s AND variant_id = $v
     AND (NOT tracked OR on_hand - reserved >= $qty)
     AND <variant live and product active>
   ```

   - The row lock serializes concurrent reservations of one variant, and
     the condition is re-evaluated on the locked row (READ COMMITTED), so
     no interleaving can hold more than `on_hand`.
   - `CHECK (0 <= reserved <= on_hand)` is the last wall.
   - We rejected `SELECT … FOR UPDATE` + check + update (two round trips
     holding the lock) and `SERIALIZABLE` (retries under contention are
     exactly what a flash sale produces).
   - A multi-line reservation is all or nothing (one transaction), and
     lines are reserved in variant ID order, so two carts can never
     deadlock.
   - The caller's `ref` (checkout or order ID) is the idempotency key:
     the same ref and lines return the existing reservation.
   - `counted` records whether a line touched tracked stock, so release
     undoes exactly what the reservation did, even if tracking changed
     in between.
6. **Expiry is a River job scheduled at `expires_at`** (default TTL 15
   minutes), inserted in the reservation's transaction (outbox). The job
   is tenant-scoped; no cross-tenant sweeper is needed. A job that runs
   early (clock skew) snoozes until the expiry.
7. **Lists run in a constant number of queries:** products page,
   variants with stock for the page (`product_id = ANY($ids)`), and
   category links for the page. Tests assert the count (6 statements
   with BEGIN, set tenant and COMMIT) for 1 and 25 products.
8. **API surface for other modules** (`catalog/api.go`):
   `GetSellableVariant`, `ReserveStock`, `ReleaseStock`. Committing a
   reservation to an order (decrementing `on_hand`) arrives with orders
   in P3, as an expand-only migration (`committed` status).

## Consequences

- Overselling is impossible by construction, not by convention; the
  concurrency test (50 reservations against stock 10 → exactly 10) runs
  on real PostgreSQL with `-race`.
- One hot variant serializes on its inventory row. That is the intended
  cost: each reservation holds the lock for one short UPDATE.
- Stock held by an abandoned checkout returns within seconds of its TTL.
- Replacing a product (`PUT`) may hit a unique index when two variants
  swap SKUs or option values in one request; the merchant gets a
  validation error and can do it in two steps.
- Concurrent category moves are not serialized; the tree queries are
  bounded (depth 16), so a rare race cannot loop.
