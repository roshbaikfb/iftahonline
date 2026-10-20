# ADR-0014: Catalog importers

- Status: Accepted
- Date: 2026-10-20

## Context

Merchants move to us with catalogs of hundreds or thousands of products:
spreadsheets (exported as CSV, in various locales) or an existing
WooCommerce shop. Imports must run in the background with
visible progress, survive a worker restart, report bad rows without
failing the whole run, and never let a merchant point our servers at
internal addresses.

## Decision

1. **One `Source` interface** (`catalog/internal/importer`): `Total`,
   `Next` (a product, or a row error) and `Skip`. It has three
   implementations:
   - **CSV**: UTF-8 (a BOM is ignored); comma, semicolon or tab detected
     from the header line. The column logic lives in a tabular layer, so
     another spreadsheet format can be added without changing it.
   - **WooCommerce REST v3**: products oldest first, with their
     variations.

   CSV uses one column layout, with Arabic header aliases:
   - `handle`, `title`, `description`, `status`, `categories`;
   - `option1..3_name/value`;
   - `sku`, `price`, `compare_at_price`, `stock`, `tracked`.

   Consecutive rows with the same handle are one product. Prices are
   major units, parsed by `platform/money` (Arabic digits accepted).
2. **Limits:** 10 MiB per CSV file, 50,000 rows, 10,000 products per import.
   The report keeps the first 100 row errors; the counters keep counting.
3. **An import is a row (`catalog_imports`) plus one River job**
   (`catalog.run_import`).
   - Files are uploaded straight to S3 with a presigned POST policy (as
     for images, ADR-0012) and deleted when the import finishes.
   - The job checkpoints a `cursor` every 25 records. A retry (up to 5
     attempts, 30 minutes each) skips what was done instead of starting
     over.
   - A source that cannot be read ends the import as `failed` with a
     stable reason code (`bad_header`, `csv_malformed`, `woo_auth`, …).
     A network error is retried.
4. **Upsert by slug, through the normal use cases.**
   - Each record goes through `ValidateProduct`, then `CreateProduct` or,
     when a live product has that slug, `UpdateProduct`. Variants are
     matched by SKU, so they keep their identity and stock.
   - Re-running an import updates instead of duplicating.
   - Categories are found by slug or created as root categories.
   - Audit entries and search-index jobs come with each product for free.
5. **WooCommerce safety.**
   - The shop URL must be https. It is checked with
     `safehttp.ValidateURL` when the import is created, and every request
     goes through a `platform/safehttp` client: private, loopback and
     metadata addresses are refused after DNS, and a refused address ends
     the import as `woo_blocked_address`.
   - The consumer key and secret are sealed with the platform keyring
     (`secretbox`, bound to the import ID) and never returned by the API.
   - Requests retry with backoff (`platform/retry`); 401/403 is a final
     `woo_auth`.
6. **Permission:** `imports.manage` (owner, manager).

## Consequences

- 1,000 products import as one job in about 7 s under `-race` in CI; the
  acceptance test runs it twice and checks that the second run updates
  instead of duplicating.
- Imports run product by product (one transaction each): a crash never
  leaves a half-written product, and the checkpoint loses at most 25
  records of progress, which are then upserted again (idempotent).
- Not imported yet:
  - WooCommerce images (they need fetching through safehttp into the
    image pipeline);
  - grouped and external WooCommerce products, and variations with
    "any" attributes (reported as row errors).
- **XLSX is deferred.** The approved library, `excelize`, has four
  vulnerabilities in its latest release (v2.11.0) that govulncheck reports
  on our call paths (GO-2026-6684/6685/6688/6689: panics, memory
  exhaustion and near-endless loops on crafted workbooks), fixed only in
  untagged commits. Merchants export CSV from Excel or Google Sheets
  instead. To add XLSX later, feed the first sheet's rows to
  `importer.NewTabular` once a tagged, clean release exists (or with a
  small stdlib reader) and add `xlsx` to the `kind` enum and CHECK.
- Re-importing does not change the stock of existing variants; stock is
  owned by the inventory endpoint (and later by orders).
