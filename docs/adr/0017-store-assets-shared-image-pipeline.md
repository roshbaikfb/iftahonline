# ADR-0017: Store assets (the logo) share the product image pipeline

- Status: Accepted
- Date: 2026-10-20

## Context

ADR-0016 deferred the merchant's logo. A logo is an image a merchant
uploads, so it carries the same risks as a product image:
- disguised types and SVG with script;
- decompression bombs;
- EXIF metadata;
- a partly uploaded file.

It also needs the same delivery: signed imgproxy URLs in AVIF and WebP,
behind the edge cache. The pipeline that handles all of this
(ADR-0012) lived inside the catalog module, where no other module could
use it.

## Decision

1. **`media` moves to `internal/platform/media`, unchanged.** Two
   helpers are added so modules share logic instead of copying it:
   - `Objects` is the bucket interface.
   - `Ingest` reads an upload, re-encodes it with `Process` and stores
     the master. A missing upload or a bad image is a final
     `RejectError`; storage errors are retryable.
   - `DeleteStoreObjects` deletes a store's objects and refuses the
     whole batch if any key is outside the store's prefix
     (`tenant.StoreID.ObjectPrefix`).
2. **The storefront owns `store_assets`.** Each row has a `kind`; the
   only kind today is the logo, and later look images can reuse the
   table. The lifecycle is the product image one:
   `pending` → `processing` → `ready` or `failed`.
   - A partial unique index allows at most one `ready` asset per store
     and kind.
3. **Replacing a logo is atomic.** The job deletes the old ready row and
   marks the new one ready in one transaction, then queues deletion of
   the old master.
   - A rejected upload keeps the live logo and records why.
   - Starting a new upload abandons earlier unfinished ones.
   - An upload that never completes expires after an hour.
4. **Accepted types are PNG, JPEG and WebP, at most 2 MiB.** SVG is
   refused (it can carry script) and GIF too (animation has no place in
   a header). Masters stay PNG when they have transparency.
5. **The logo is not part of the versioned theme settings.**
   - Uploading is asynchronous, so a job finishing must not bump the
     version a merchant is editing.
   - The logo has its own endpoints.
   - Going live or being deleted publishes `storefront.theme.changed`, so
     rendered pages refresh.
6. **Jobs keep their module's kind names.** `storefront.process_asset`,
   `storefront.expire_asset_upload` and `storefront.delete_objects` run
   on the `media` queue that the catalog already sizes. The catalog's
   `catalog.delete_objects` keeps its name, so queued jobs are not
   stranded.

## Consequences

- **Positive:**
  - One audited path handles every merchant image.
  - The two modules share the upload logic and the prefix guard instead
    of carrying copies.
  - Merchants never see a half-processed logo.
- **Negative:**
  - The logo is not in the theme's optimistic version, so two editors
    who upload logos at the same time end with whichever finishes last.
    That is acceptable for a single image.
