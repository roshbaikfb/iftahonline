# ADR-0021: Orders: state machine, idempotent placement, monthly partitions

- Status: Accepted
- Date: 2026-10-22

## Context

P4 needs orders for cash-on-delivery commerce in Egypt and the Gulf:

- **Placing.** Shoppers order as guests in one form. Merchants call to
  confirm, then pack, ship and record deliveries and returns.
- **Double submits must not double orders.** Phones on weak networks
  resubmit forms; clients retry requests.
- **Stock.** It must follow the order (ADR-0020).
- **Accountability.** Every change needs a timeline entry, an audit row
  and a domain event.
- **Scale.** Orders are the largest tenant tables (CLAUDE.md: partitioned
  by month). The acceptance asks for 50 orders/s with p95 < 150 ms.

## Decision

1. **An explicit state machine** (`domain.Transition`), one table of
   allowed edges:

   | From | To |
   |---|---|
   | pending | confirmed, cancelled |
   | confirmed | ready_to_ship, cancelled |
   | ready_to_ship | shipped, cancelled |
   | shipped | delivered, failed_delivery |
   | failed_delivery | shipped (retry), returned |
   | delivered | returned |

   - `cancelled` and `returned` are final.
   - A forbidden move is a typed `*TransitionError` (409
     `invalid_transition`).
   - Each edge carries its stock effect: release on cancel, commit on
     shipping (a retry's second commit is a no-op), restock on return.
   - Each edge also carries its permission. Confirming and cancelling
     need `orders.manage` (support makes the calls); goods moves need
     `orders.fulfil` (shipping).
   - A test lists all 64 (from, to) pairs by hand.
2. **Each transition is one transaction.** It locks the order row, checks
   the optional `version`, then:
   - checks the step's permission;
   - applies the stock effect through the catalog's `…Tx` methods;
   - writes the timeline row (`order_events`), the audit entry and the
     outbox event (`orders.order.status_changed`).
   - The audit entry and the event carry IDs and statuses, never contact
     details.
3. **Placing an order** (`PlaceOrder`):
   - **Before the transaction:** the order is priced from the catalog's
     current prices, and a blocked phone is refused.
   - **One transaction then:**
     - records the customer (`RecordOrderTx`);
     - holds the stock (`ReserveStockTx` with `Hold`);
     - inserts the order with copied titles and prices;
     - writes the timeline, audit and event.
   - **A refusal leaves nothing behind**: no order, held stock,
     customer count or key.
4. **Double submits.**
   - **`Idempotency-Key`** (`order_idempotency`, unique per store, kept a
     week).
     - The key is inserted first in the transaction, so a concurrent
       repeat waits on the unique index and then replays the winner's
       order.
     - The same key with another request (a SHA-256 fingerprint of
       every submitted field, length-prefixed) is 409
       `idempotency_key_reused`.
     - Each keyed order deletes a few of its store's expired keys.
       FORCE RLS binds the table owner too, so a global purge would see
       no rows.
   - **The duplicate window**, for storefront orders without a key.
     - If the same phone orders the same variants and quantities within
       30 minutes, the earlier order is returned (`Duplicate`).
       Cancelled orders do not count.
     - A transaction-scoped advisory lock on (store, phone) closes the
       check-then-insert race.
     - Staff entering phone orders are not deduplicated.
   - **Tested:** 20 concurrent submits, with and without a key, create
     one order and hold stock once.
5. **Monthly partitions keyed by the ULID.**
   - `orders.created_at` is the time the order's ULID encodes, so a
     lookup by ID computes its partition key and touches one partition.
   - `order_items` and `order_events` are partitioned on the order's
     `created_at`, so an order's lines and timeline live in its month.
   - Composite foreign keys tie items and events to their order.
   - The tables register with the platform partition job (ADR-0020).
6. **Lists are driven by the most selective filter's index.**
   - The driving index is customer, then phone, then status, then the
     primary key. The other filters apply inside that walk.
   - The created-at range and the keyset bound (`id <`) are always set,
     so partitions are pruned at run time and each page is an index
     range.
   - Under a generic plan, PostgreSQL walks the partitions newest first
     with backward index scans and no Sort. This was checked on 40,000
     orders as app_user.
   - A cursor's own millisecond is kept in range, because ULIDs within
     one millisecond are not ordered by time.
7. **CSV export.**
   - Export validates the query and audits itself before the response
     starts, then streams pages of 500 orders with their lines (one
     LATERAL query per page).
   - At most 100,000 rows; narrow the dates to export more.
   - The file has a UTF-8 BOM (Arabic in spreadsheets), times in the
     store's zone, and plain decimal amounts.
   - Cells a spreadsheet would evaluate (`= + - @`, tab, CR) start with
     an apostrophe, because shoppers type names, addresses and notes.
8. **Shipping fees are 0** (`shipping_minor`) until shipping rates
   exist. The column and the total's CHECK are already in place.
9. **Order codes.** The code shown to people is the ULID's last 8
   characters (random); nothing sequential is exposed (CLAUDE.md
   rule 6).

## Consequences

- **Positive:**
  - Placement is atomic and safe to retry. Stock, customer and order
    always agree.
  - The state machine and its permissions live in one table, tested
    cell by cell.
  - Order lookups and lists stay index ranges as the tables grow by
    months.
- **Negative:**
  - **Pending orders hold stock until someone acts** (ADR-0020). Bulk
    confirm and cancel exist; an automatic expiry of stale pending
    orders may come later.
  - **The duplicate window can merge two genuine orders**: the same
    phone, the same items, within 30 minutes, without a key. The
    storefront sends a key, so this only affects shoppers whose browser
    runs no script.
  - **Bulk transitions run one transaction per order** (up to 100), so
    a large bulk takes longer than one statement would.
  - **The export is synchronous and capped.** Very large stores export
    by date ranges; a job-based export to object storage can come later.
