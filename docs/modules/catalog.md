# Module: catalog

The catalog module owns **products**, their **variants** (option
combinations with SKU and price), **stock** and **reservations**, and the
store's **category tree**. Design and stock semantics:
[ADR-0011](../adr/0011-catalog-and-stock-reservations.md). Isolation:
[ADR-0002](../adr/0002-shared-db-store-id-rls.md).

Image pipeline: [ADR-0012](../adr/0012-image-pipeline.md). Search:
[ADR-0013](../adr/0013-tenant-safe-search.md). Importers follow in a later
P2 pull request.

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
| `catalog.process_image` (queue `media`) | `completeImageUpload` | Sniffs, bounds, orients and re-encodes the original into a master; `ready` or `failed` with a reason |
| `catalog.expire_image_upload` | One hour after `createImageUpload` | Deletes the image if it is still `pending`, and any partial upload |
| `catalog.delete_objects` | After processing, image deletion and product deletion | Removes objects; refuses keys outside the store's prefix |
| `catalog.index_product` | Product create/replace/delete; image ready/updated/deleted | Upserts the product's search document from its current state, or deletes it |
| `catalog.search_settings` (global, periodic) | Worker start, then every 6 hours | Creates the index and applies its settings |

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
| `product_images` | `pending` → `processing` → `ready` / `failed`; `upload_key`, master `object_key`, size, `alt`, `position` (≤ 20 per product) |

## HTTP API (tag `catalog`)

| Operation | Permission |
|---|---|
| `listProducts`, `getProduct`, `listCategories`, `searchProducts` | `catalog.read` |
| `createProduct`, `updateProduct`, `deleteProduct` | `catalog.manage` |
| `createCategory`, `updateCategory`, `deleteCategory` | `catalog.manage` |
| `setInventory` | `inventory.manage` |
| `createImageUpload`, `completeImageUpload`, `updateImage`, `deleteImage` | `catalog.manage` |

- `updateProduct` replaces the product. Variants sent with `id` keep their
  identity and stock; variants without one are created with `on_hand`;
  live variants left out are deleted. Existing stock changes only through
  `setInventory`.
- Slugs are derived from the title or name when empty. Arabic letters are
  kept; diacritics and tatweel are dropped; Arabic-Indic digits become
  ASCII.
- A malformed or foreign ID in the path is 404; in the body it is a
  validation error.
- Images: `createImageUpload` returns a presigned POST (`upload.url` +
  `upload.fields`, file last as `file`); after the upload, call
  `completeImageUpload`. `ready` images carry signed imgproxy `urls` for
  four sizes in AVIF and WebP.

## Search

`searchProducts` goes to Meilisearch only (no database query). The search
client adds the store filter from the context; the text is normalized
like the documents (`domain.NormalizeSearch`).

## Performance

- `listProducts` runs three queries whatever the page size: products with
  their category IDs, variants with stock, and images for the page (asserted by
  `TestListProductsConstantQueries`). A category filter has its own
  query, driven by the category's links. Plans: ADR-0011 §7.
- A reservation is one UPDATE per line on the variant's inventory row.

## Tests

- `domain`: table-driven rules; `FuzzSlugify`.
- `search` (real Meilisearch): store A never sees store B's products,
  whatever the query; foreign upserts fail; foreign deletes are no-ops;
  Arabic forms match. `domain`: `FuzzNormalizeSearch`.
- `media`: type sniffing, bombs, orientation, metadata stripping;
  `FuzzProcess`, `FuzzJPEGOrientation`; an integration test against real
  SeaweedFS and imgproxy (presigned POST policy, signed and tampered URLs).
- `app` (real PostgreSQL): lifecycle, uniqueness, constant query count,
  50 concurrent reservations on stock 10, multi-line deadlock freedom,
  idempotency, expiry, cross-tenant isolation.
- `httpapi`: happy path, validation, authorization and cross-tenant for
  every operation; `TestMain` fails if an operation has no cross-tenant
  test.
