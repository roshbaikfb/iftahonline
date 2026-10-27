# Module: catalog

The catalog module owns **products**, their **variants** (option
combinations with SKU and price), **stock** and **reservations**, and the
store's **category tree**. Design and stock semantics:
[ADR-0011](../adr/0011-catalog-and-stock-reservations.md). Isolation:
[ADR-0002](../adr/0002-shared-db-store-id-rls.md).

Image pipeline: [ADR-0012](../adr/0012-image-pipeline.md). Search:
[ADR-0013](../adr/0013-tenant-safe-search.md). Importers:
[ADR-0014](../adr/0014-catalog-importers.md). Events and the storefront
read side: [ADR-0015](../adr/0015-domain-events-and-storefront-reads.md).

## Public API (`internal/modules/catalog/api.go`)

| Symbol | Purpose |
|---|---|
| `Catalog.GetSellableVariant(ctx, id)` | A variant of an active, live product with price and stock, or `ErrVariantNotFound` |
| `Catalog.ReserveStock(ctx, req)` | Holds stock for every line of `req` (all or nothing) until release or TTL; idempotent by `req.Ref` |
| `Catalog.ReleaseStock(ctx, ref)` | Gives the held stock back; idempotent; unknown ref is `ErrReservationNotFound` |
| `ErrInsufficientStock`, `ErrVariantNotSellable` | Wrapped in a `*StockError` that names the variant |
| `ErrReservationRefUsed` | The ref already holds a different set of lines |
| `Storefront.ProductBySlug(ctx, slug)` | The active product at `slug` with variants (`Available`, and `Left`: the units left only when there are 1 to `MaxShownLeft` (10), never higher counts) and signed image renditions, or `ErrProductNotFound`; a malformed slug costs no query |
| `Storefront.ProductsByIDs(ctx, ids)` | Active products among `ids` in the given order (≤ `MaxStorefrontIDs` = 50), others skipped |
| `Storefront.ListProducts(ctx, q)` | Active products newest first, optional category, cursor, ≤ `MaxStorefrontLimit` = 48 |
| `Storefront.Categories(ctx)` | The category tree, by position |
| `Storefront.Search(ctx, q)` | Shoppers' search: active products matching the text (≤ 100 characters, normalized), 24 a page, ≤ 20 pages, in the index's order, read from the database in one query; `ErrSearchUnavailable` without a search key (ADR-0033) |
| `Storefront.SellableVariants(ctx, ids)` | A cart's variants in one query, with the product's first ready picture (`ImageKey`) for thumbnails |
| `NewStorefront(StorefrontDeps)` | The read side alone (pool + image delivery settings), for the storefront role |
| `ProductChanged`, `ProductDeleted`, `CategoryChanged`, `Event*` | Events the module publishes (below) |
| `CurrencySource`, `CurrencyFunc` | How the module learns the store currency (cmd/platform adapts tenancy) |
| `Module.Mount`, `Module.RegisterWorkers` | HTTP routes (behind identity's membership middleware) and the expiry job |
| `Module.Starter()` → `Starter`, `SampleProduct`, `CategoryInput`, `ProductInput`, `VariantInput` | What the setup wizard's starter template uses ([ADR-0034](../adr/0034-setup-wizard-and-starter-templates.md)): `EnsureCategory` and `EnsureSampleProduct`, idempotent by slug (a merchant's own category or product with that slug is left alone) |

Every method acts on the store in `ctx`.

## Events and jobs

Events go through `platform/eventbus` (ADR-0015), in the transaction of
the change. Payloads carry IDs; subscribers read current state.

| Event | Published when |
|---|---|
| `catalog.product.changed` (`reason`) | `created`, `updated` (fields, variants, prices, categories), `images` (ready, updated, deleted), `stock` (on-hand set, or a reservation/release/expiry changed what shops show: a variant sold out, came back, or its "only N left" count, i.e. 10 or fewer available before or after; changes among plenty publish nothing) |
| `catalog.product.deleted` | A product is deleted |
| `catalog.category.changed` (`reason`) | A category is created, updated or deleted |

| Job | When | Effect |
|---|---|---|
| `catalog.expire_reservation` | Scheduled at the reservation's `expires_at`, inserted in the reservation's transaction | Ends the ref's active lines as `expired` and gives back counted units; snoozes if it runs early |
| `catalog.process_image` (queue `media`) | `completeImageUpload` | Sniffs, bounds, orients and re-encodes the original into a master; `ready` or `failed` with a reason |
| `catalog.expire_image_upload` | One hour after `createImageUpload` | Deletes the image if it is still `pending`, and any partial upload |
| `catalog.delete_objects` | After processing, image deletion and product deletion | Removes objects; refuses keys outside the store's prefix |
| `catalog.index_product` | Product create/replace/delete; image ready/updated/deleted | Upserts the product's search document from its current state, or deletes it |
| `catalog.search_settings` (global, periodic) | Worker start, then every 6 hours | Creates the index and applies its settings |
| `catalog.run_import` | `startImport` | Imports the source's records after the saved cursor; checkpoints every 25; `succeeded` or `failed` with a reason |

## Tables

All tenant-owned, with `store_id`, RLS and composite foreign keys to their
parent (`db/migrations/00015_catalog.sql`).

| Table | Notes |
|---|---|
| `products` | `slug` (text, lower-case; 00018) unique per store among live rows; `options` JSONB; `status` draft/active/archived; soft delete |
| `variants` | `sku` unique per store among live rows; `option_values text[]` unique per product; prices in minor units + currency; soft delete |
| `inventory_levels` | `tracked`, `on_hand`, `reserved`; `CHECK (0 <= reserved <= on_hand)` |
| `stock_reservations` | One row per (ref, variant): `qty`, `counted`, `status`, `expires_at` |
| `categories` | Tree via `parent_id` (≤ 3 levels, ≤ 500 per store); `slug` unique per store; a parent with children cannot be deleted |
| `product_categories` | Product ↔ category links (≤ 20 per product) |
| `catalog_imports` | `kind`, `status`, counters, `cursor` (resume point), first 100 `row_errors`; sealed WooCommerce credentials |
| `product_images` | `pending` → `processing` → `ready` / `failed`; `upload_key`, master `object_key`, size, `alt`, `position` (≤ 20 per product) |

## HTTP API (tag `catalog`)

| Operation | Permission |
|---|---|
| `listProducts`, `getProduct`, `listCategories`, `searchProducts` | `catalog.read` |
| `createProduct`, `updateProduct`, `deleteProduct` | `catalog.manage` |
| `deleteSampleProducts` (`DELETE /products/samples`, returns `deleted`) | `catalog.manage` |
| `listLowStock` (`GET /inventory/low?threshold&limit`: tracked variants of active, non-sample products with at most `threshold` units free, fewest first, and the total; the dashboard passes the storefront theme's `low_stock`) | `catalog.read` |
| `createCategory`, `updateCategory`, `deleteCategory` | `catalog.manage` |
| `setInventory` | `inventory.manage` |
| `createImageUpload`, `completeImageUpload`, `updateImage`, `deleteImage` | `catalog.manage` |
| `createImport`, `startImport`, `getImport`, `listImports` | `imports.manage` |

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

### Sample products

The setup wizard's starter template adds products marked `sample`
(`products.is_sample`, ADR-0034). They are listed (`sample: true`) and
shown on the storefront with a "تجريبي" tag and a note, but never sold:
the sellable-variant reads and the stock reservation skip them, so a cart
cannot hold one and an order cannot reserve one. Their pictures are
written to the upload key and processed like a merchant's upload.
`deleteSampleProducts` deletes them as `deleteProduct` deletes one.

## Imports

CSV columns (header names are case-insensitive; Arabic aliases such
as الاسم، السعر، الكمية، التصنيف are accepted):

| Column | Meaning |
|---|---|
| `handle` | Product slug; consecutive rows with the same handle are variants of one product |
| `title`, `description`, `status`, `categories` | From the product's first row; categories separated by `\|`, `;`, `،` or `؛` |
| `option1_name` … `option3_name` | From the first row |
| `option1_value` … `option3_value`, `sku`, `price`, `compare_at_price`, `stock`, `tracked` | Per variant row; prices in major units |

Existing products (same slug) are updated, with variants matched by SKU;
others are created. Bad rows are reported and skipped. Excel workbooks are
not accepted yet: save as "CSV UTF-8" (ADR-0014).

## Search

`searchProducts` goes to Meilisearch only (no database query). The search
client adds the store filter from the context; the text is normalized
like the documents (`arabic.NormalizeSearch`, in platform/arabic since P4).

## Performance

- `listProducts` runs three queries whatever the page size: products with
  their category IDs, variants with stock, and images for the page (asserted by
  `TestListProductsConstantQueries`). A category filter has its own
  query, driven by the category's links. Plans: ADR-0011 §7.
- A reservation is one UPDATE per line on the variant's inventory row.
- Storefront reads run six queries each whatever the size (BEGIN, tenant,
  products, variants, images, COMMIT), asserted in `storefront_test.go`.
  Slug lookups are index conditions under RLS
  (`TestSlugLookupUsesIndexUnderRLS`); plans in ADR-0015.

## Tests

- `domain`: table-driven rules (slug rules and `FuzzMake` live in `platform/slug`).
- `search` (real Meilisearch): store A never sees store B's products,
  whatever the query; foreign upserts fail; foreign deletes are no-ops;
  Arabic forms match. `platform/arabic`: `FuzzNormalizeSearch`.
- `importer`: CSV dialects, Arabic headers, grouping, row errors, source
  errors, a fake WooCommerce server (retry, auth, resume, loopback
  refused); `FuzzCSV`, `FuzzStripHTML`.
- `app` imports: 1,000-product CSV import as a job (and a second run that
  updates), report with capped row errors, resume from a checkpoint,
  WooCommerce with sealed keys, cross-tenant.
- `platform/media` (shared with storefront): `Ingest`, type sniffing, bombs, orientation, metadata stripping;
  `FuzzProcess`, `FuzzJPEGOrientation`; an integration test against real
  SeaweedFS and imgproxy (presigned POST policy, signed and tampered URLs).
- `app` events: each write publishes the right event and reason; stock
  events only when shown stock changes (sell-out, back in stock, a count of
  10 or fewer), none among plenty; nothing on failed writes.
- `app` storefront: active-only, order kept, repeats and foreign IDs
  skipped, constant queries, renditions, cross-tenant, slug plan under RLS.
- `app` (real PostgreSQL): lifecycle, uniqueness, constant query count,
  50 concurrent reservations on stock 10, multi-line deadlock freedom,
  idempotency, expiry, cross-tenant isolation.
- `httpapi`: happy path, validation, authorization and cross-tenant for
  every operation; `TestMain` fails if an operation has no cross-tenant
  test.
