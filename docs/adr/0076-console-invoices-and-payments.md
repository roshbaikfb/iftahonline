# ADR-0076: The console's invoices and payments

- Status: Accepted
- Date: 2026-11-03

## Context

The console's home counts overdue invoices and duplicate payments, but
had nowhere to send the admin. Invoices could be read and settled only
from one store's page, so an agency's invoice could not be settled at
all, and payment attempts other than duplicates were invisible. The
owner approved the plan and its mockup on 2026-11-03, with two
decisions:

- **Collected this month** is what was actually paid, VAT included, with
  the VAT shown on its own under it (what is owed to the tax authority).
- **CSV export** of the invoices list, with the list's filters, for the
  accountant. Each export is recorded in the admin log.

## Decision

### Reading every account

Billing tables are behind the account's wall (RLS, ADR-0049); the API
role reads one account at a time. Three `SECURITY DEFINER` functions
(migration 00090) read across accounts, in the style of
`app.billing_health()`:

- `app.billing_invoices(...)`: invoices newest first, by status (open,
  overdue = open past due, paid, void), currency, and a number prefix or
  a part of the account's name, after a keyset `(issued_at, id)`. Given
  an ID, it returns that one invoice; the page's header and the list
  share one shape. Each row names its account, the account's kind, and
  its store: the invoice's own, else the account's first.
- `app.billing_attempts(...)`: payment attempts newest first, by outcome
  and gateway, or one invoice's attempts; keyset `(created_at, id)`.
- `app.billing_summary(...)`: open and overdue invoices per currency,
  those paid since the Cairo month began (total with VAT, and the VAT),
  and the last 7 days' failed charges per outcome and reason.

An invoice's lines are read in its account's transaction
(`store.UseAccount`), not through a function.

### Planned per call

The list functions take optional filters (`p IS NULL OR ...`). A
`LANGUAGE sql` function that is not inlined (`SECURITY DEFINER` is never
inlined) is planned once with its parameters unknown. On 120,000
invoices, the first page then scanned and sorted every invoice: 111 ms.
The list functions are therefore `plpgsql` with
`SET plan_cache_mode = force_custom_plan`. Each call is planned with its
values, the unused filters fold away, and a page walks the new index:

| Query (121,200 invoices, 5,050 accounts) | Before | After |
|---|---|---|
| First page, no filter | 111 ms | 0.7 ms |
| Overdue (partial index on `due_at`) | 10.8 ms | 1.9 ms |
| One invoice | 11 ms | 0.04 ms |
| First page of payments | 1.3 ms | 0.5 ms |
| Summary | 23 ms | 9.6 ms |

New indexes: invoices `(issued_at DESC, id DESC)`, `(due_at)` where open,
`(paid_at)` where paid; payment attempts `(created_at DESC, id DESC)`.

### In the admin module

- **Names:** a store account's invoices show the store's name now,
  through `tenancy.Directory.Names` (one more query a page). An agency
  shows its own name.
- **Settle any invoice:** `POST /admin/v1/invoices/{id}/settle` settles
  an open invoice of any account, store or agency, through billing's
  existing `SettleInvoice` (a `manual` attempt). It is recorded first in
  the admin log as `invoice.settled`, with the reason, the reference, the
  account and the number. It also names the store, but only for a store's
  account, so an agency's invoice is not filed under one of its stores.
  The store-scoped endpoint stays as it is.
- **Export:** `GET /admin/v1/invoices/export` takes the list's filters.
  The filters are checked and the export is recorded
  (`billing.invoices_exported`, with the filters) before the file
  streams. The file holds at most 50,000 rows, newest first, in pages of
  500. It is UTF-8 with a BOM, times are in Cairo, and amounts are plain
  decimals. A name that a spreadsheet would run as a formula starts with
  an apostrophe, as in the orders export.

## Consequences

- An agency's invoice can be settled. Every attempt, its gateway's
  reason and its reference can be seen from the console.
- **Name search is a scan:** a search by name cannot use an index (its
  match is inside the name). It took 75 ms on 121,200 invoices. That is
  fine for an admin-only page. If it grows slow, the fix is a trigram
  index on `billing_accounts.name`.
- **Totals per currency:** totals are never added across currencies.
- **Failed counts:** failed payments carry no currency, so they are
  counted, not summed.
