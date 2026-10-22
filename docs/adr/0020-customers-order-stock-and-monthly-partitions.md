# ADR-0020: Customers, stock held by orders, and one monthly partition job

- Status: Accepted
- Date: 2026-10-21

## Context

P4 brings orders. Most shoppers in Egypt and the Gulf order cash on
delivery: they fill one form as a guest, and the merchant calls them to
confirm before shipping. Three foundations come first:

- **Who ordered.** A store needs a customer record per shopper (order
  count, history, a block list against fake orders) without accounts or
  passwords.
- **Stock for an order.** ADR-0011's reservations expire after at most 24
  hours. A pending COD order can wait days for confirmation. Committing a
  reservation was left for orders (ADR-0011 §8).
- **Partitioned tables.** `orders`, `order_items` and order events are
  partitioned by month (CLAUDE.md). `audit_log` already was, through its
  own function and identity's job.

## Decision

1. **A customers module.** A customer is one phone number in one store.
   - **Phone as natural key.** `customers` holds a unique
     `(store_id, phone)` in canonical E.164 (`platform/phone`). The same
     person in two stores is two unrelated customers: no cross-store
     profile.
   - **Created by the first order.** Orders call `RecordOrderTx` in their
     own transaction (ADR-0007). It upserts the customer, keeps the latest
     non-empty name, and counts the order.
   - **The block list is a flag on the customer** (`blocked_at`, reason).
     - A merchant can block a number that never ordered; the customer is
       created for it.
     - `RecordOrderTx` refuses a blocked customer, so the caller's
       transaction rolls back and nothing is recorded. `Blocked` lets
       orders refuse before any other work.
     - **Shoppers never learn they are blocked.** Storefront callers show
       a generic refusal.
   - **Lookups.** A phone in any common format matches exactly. A name
     matches by word prefix in its search form: `NormalizeSearch` moved
     from the catalog to `platform/arabic`, so "احمد" finds "أحمد".
   - **Privacy.** The full phone is stored, because merchants call it and
     couriers need it. It is never logged, and audit entries carry only
     the customer ID (CLAUDE.md security rules).
   - **Permissions.** `customers.read` and `customers.manage`, for owner,
     manager and support. Support staff are the ones who spot fake orders.
     Shipping gets neither.
2. **Orders hold, commit and restock stock in their transaction**
   (`catalog.Catalog`'s new `…Tx` methods).
   - **`ReserveStockTx` with `Hold`.** The stock is held with no expiry
     (`expires_at` NULL), and no expiry job is scheduled. The order is
     created in the same transaction, so a failed order holds nothing.
   - **`CommitStockTx` when the order ships.** `on_hand` and `reserved`
     drop together; availability does not change.
   - **`RestockTx` when it is returned.** The committed units go back on
     hand. A "back in stock" event fires if the variant was sold out.
   - **`ReleaseStockTx` when it is cancelled before shipping.**
   - **A reservation's life is one table of steps**:
     active → released | expired | committed, and committed → returned.
     - Each step is idempotent: repeating a done step is a no-op.
     - A step out of order is `ErrReservationState`.
     - One `AdjustInventory` query applies each step's on-hand and
       reserved deltas.
   - Migration 00023 is expand-only: a nullable expiry and two new
     statuses.
3. **One monthly partition job for every partitioned table.**
   - `app.monthly_partitioned` registers each parent table with its key
     column and whether it is append-only. A migration that creates such
     a table registers it.
   - `app.ensure_monthly_partitions` (SECURITY DEFINER) builds missing
     partitions for registered tables only, the 00013 way: standalone,
     then ATTACH, with no lock that conflicts with inserts. Append-only
     tables lose UPDATE and DELETE on their partitions.
   - The platform job `platform.partitions` keeps every table three
     months ahead, daily and at worker start. It replaces identity's
     audit job. `ensure_audit_log_partitions` stays as a wrapper until
     nothing calls it.
4. **`platform/geo`.**
   - Governorates and regions for Egypt and the GCC, with ISO 3166-2
     codes and Arabic and English names. The order form and orders use
     the same list.
   - A store's country follows its currency.
   - Cities are free text.
5. **Plan tests are shared** (`testkit.CapturingPool`, `GenericPlan`,
   `AssertIndexCond`). They EXPLAIN the exact SQL the code sent, as
   app_user under a generic plan. The customer list uses a keyset bound
   that is always an index condition (`id < before_id`, the largest UUID
   on the first page). An optional `IS NULL OR` cursor filtered every
   earlier row instead.

## Consequences

- **Positive:**
  - Orders can create a customer, refuse a blocked one and hold stock
    atomically, through `api.go` only.
  - A pending order never loses its stock to an expiry, and stock can
    never be shipped twice or restocked before it shipped.
  - A new partitioned table costs one `INSERT` in its migration.
- **Negative:**
  - **Held stock stays held until the merchant acts.** A store that
    never confirms or cancels pending orders sees its available stock
    shrink. The orders module (ADR-0021) must make pending orders visible
    and cancellable in bulk.
  - **Name search scans within the store.** A name prefix is a filter
    over the store's newest-first index walk, bounded by the page size.
    A rare name in a very large store reads many rows; a trigram index
    can come later if needed.
  - **The partition function is SECURITY DEFINER and builds DDL.** It
    takes no names from its caller and acts only on registry rows, which
    app_user cannot write.
