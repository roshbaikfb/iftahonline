# ADR-0013: Tenant-safe product search with Arabic normalization

- Status: Accepted
- Date: 2026-10-19

## Context

Merchants search their catalog in the dashboard, and shoppers will search
the storefront. Meilisearch has no row-level security, so isolation
between stores is entirely our code's job. Arabic text is typed
inconsistently: أحذية, احذيه and إحذية mean the same thing, and so do
مستشفى/مستشفي and مكتبة/مكتبه. Diacritics and tatweel appear in titles
but rarely in queries.

## Decision

1. **One index (`MEILI_INDEX`, default `products`) for every store.**
   - Each document carries `store_id`.
   - We rejected an index per store: 5,000+ indexes multiply settings
     tasks, memory and operations for no gain at our size.
2. **One tenant-safe client is the only code that talks to Meilisearch**
   (`catalog/internal/search`). A depguard rule fails lint on any other
   import of `meilisearch-go`.
   - It takes the store from `tenant.FromContext` (never from arguments).
   - Every search starts its filter with `store_id = "<store>"`; callers
     pass typed fields (status, category), never filter syntax.
   - Deletions are by filter, `store_id = … AND id IN [...]`, so a
     foreign ID is a no-op.
   - Upserting a document of another store fails the whole batch.
   - Second wall: a hit whose `store_id` differs from the context is
     treated as an error, not returned.
3. **Normalization on both sides** (`domain.NormalizeSearch`, applied to
   documents at indexing and to queries):
   - lower case;
   - diacritics, tatweel and zero-width characters dropped;
   - أ إ آ ٱ → ا, ى → ي, ة → ه, ؤ → و, ئ → ي, Farsi ی/ک;
   - Arabic-Indic digits → ASCII.

   Normalized text lives in `search_title` and `search_text` (the only
   searchable fields). Display fields keep the merchant's spelling.
4. **Indexing is a job per change** (`catalog.index_product`):
   - It is inserted in the same transaction as the change (product
     create, replace or delete; image ready, updated or deleted).
   - It reads the product's current state when it runs, so repeated or
     out-of-order jobs converge, and a deleted product is removed.
   - It waits for Meilisearch's task, so failures are retried.
5. **Settings are code.** A global periodic job
   (`catalog.search_settings`, on start and every six hours) creates the
   index and applies searchable, filterable, sortable and displayed
   attributes idempotently.
6. **Keys:**
   - The api's key needs `search` and `documents.*` on the index.
   - The worker's key also needs `indexes.create`, `settings.update` and
     `tasks.get`.
   - The master key is never deployed.

## Consequences

- The acceptance test (store A never sees store B's products, whatever
  the query, including filter-injection text) runs against a real
  Meilisearch container.
- Search results lag writes by about a second (one job plus one index
  task).
- Deleting a category does not reindex its products; their stale
  category ID matches nothing.
- A future storefront search reuses the same client and normalization.
