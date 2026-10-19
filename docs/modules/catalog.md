# Module: catalog

The catalog module owns **products**, their **variants** (option
combinations with SKU and price), **stock** and **reservations**, and the
store's **category tree**. Design and stock semantics:
[ADR-0011](../adr/0011-catalog-and-stock-reservations.md). Isolation:
[ADR-0002](../adr/0002-shared-db-store-id-rls.md).

Images, search indexing and importers follow in later P2 pull requests.

## Public API (`internal/modules/catalog/api.go`)

| Symbol | Purpose |
|---|---|
| `Catalog.GetSellableVariant(ctx, id)` | A variant of an active, live product with price and stock, or `ErrVariantNotFound` |
| `Catalog.ReserveStock(ctx, req)` | Holds stock for every line of `req` (all or nothing) until release or TTL; idempotent by `req.Ref` |
| `Catalog.ReleaseStock(ctx, ref)` | Gives the held stock back; idempotent; unknown ref is `ErrReservationNotFound` |
| `ErrInsufficientStock`, `ErrVariantNotSellable` | Wrapped in a `*StockError` that names the variant |
| `ErrReservationRefUsed` | The ref already holds a different set of lines |
| `CurrencySource`, `CurrencyFunc` | How the module learns the store currency (cmd/platform adapts tenancy) |
| `Module.Mount`, `Module.RegisterWorkers` | HTTP routes (behind identity's membership middleware) and the expiry job |

Every method acts on the store in `ctx`.

## Events and jobs

| Job | When | Effect |
|---|---|---|
| `catalog.expire_reservation` | Scheduled at the reservation's `expires_at`, inserted in the reservation's transaction | Ends the ref's active lines as `expired` and gives back counted units; snoozes if it runs early |

Product events for search indexing arrive with the search pull request.

## Tables

All tenant-owned, with `store_id`, RLS and composite foreign keys to their
parent (`db/migrations/00015_catalog.sql`).

| Table | Notes |
|---|---|
| `products` | `slug` unique per store among live rows; `options` JSONB; `status` draft/active/archived; soft delete |
| `variants` | `sku` unique per store among live rows; `option_values text[]` unique per product; prices in minor units + currency; soft delete |
| `inventory_levels` | `tracked`, `on_hand`, `reserved`; `CHECK (0 <= reserved <= on_hand)` |
| `stock_reservations` | One row per (ref, variant): `qty`, `counted`, `status`, `expires_at` |
| `categories` | Tree via `parent_id` (≤ 3 levels, ≤ 500 per store); `slug` unique per store; a parent with children cannot be deleted |
| `product_categories` | Product ↔ category links (≤ 20 per product) |

## HTTP API (tag `catalog`)

| Operation | Permission |
|---|---|
| `listProducts`, `getProduct`, `listCategories` | `catalog.read` |
| `createProduct`, `updateProduct`, `deleteProduct` | `catalog.manage` |
| `createCategory`, `updateCategory`, `deleteCategory` | `catalog.manage` |
| `setInventory` | `inventory.manage` |

- `updateProduct` replaces the product. Variants sent with `id` keep their
  identity and stock; variants without one are created with `on_hand`;
  live variants left out are deleted. Existing stock changes only through
  `setInventory`.
- Slugs are derived from the title or name when empty. Arabic letters are
  kept; diacritics and tatweel are dropped; Arabic-Indic digits become
  ASCII.
- A malformed or foreign ID in the path is 404; in the body it is a
  validation error.

## Performance

- `listProducts` runs a constant number of queries: products, variants
  with stock, and category links for the page (asserted by
  `TestListProductsConstantQueries`).
- A reservation is one UPDATE per line on the variant's inventory row.

## Tests

- `domain`: table-driven rules; `FuzzSlugify`.
- `app` (real PostgreSQL): lifecycle, uniqueness, constant query count,
  50 concurrent reservations on stock 10, multi-line deadlock freedom,
  idempotency, expiry, cross-tenant isolation.
- `httpapi`: happy path, validation, authorization and cross-tenant for
  every operation; `TestMain` fails if an operation has no cross-tenant
  test.
