# ADR-0081: The console's growth page

- Status: Accepted
- Date: 2026-11-04

## Context

The console showed only today's figures: the recurring revenue and the
last 30 days' new stores (ADR-0075). The owner wants the trend over a
year. The plan and the mockup were approved on 2026-11-04.

Some figures can be rebuilt from the past, and some cannot:

- **Can be rebuilt:** paid invoices have `paid_at` and amounts, and
  stores have `created_at`. The collected revenue and the stores per
  month can be computed for any month already gone.
- **Cannot be rebuilt:** subscriptions keep only their current status
  and its last change. The recurring revenue of a past day, and who paid
  as a past month began, are lost.

### The owner's decisions

1. The recurring revenue and churn start on the day this ships. Nothing
   is estimated for the months before.
2. The collected revenue is before VAT, like the recurring revenue.
3. A store has converted when it has any paid invoice with a
   subscription line for it, in any month after it was created.

## Decision

### The page

`GET /admin/v1/growth` (permission `billing.read`) covers the last 12
Cairo months, this one included. Its sections:

- **collected:** the revenue collected per month and currency. It sums
  the `subtotal_minor` of paid invoices, so it is before VAT.
- **stores:** the stores created each month, and how many of them have
  converted since.
- **mrr:** the recurring revenue per Cairo day and currency, from the
  first reading on.
- **churn:** for each complete month, the paying accounts as it began,
  those that joined by the next month's start, those lost by then, and
  the rate in basis points.
- **plans and paying:** each plan's paying accounts and recurring
  revenue per currency today, and the paying accounts in all.

Currencies are never converted. Like `/metrics`, each section has its
own timeout, and a section that fails is named in `errors` while the
rest still answers.

### Where the work happens

- **Billing:**
  - `Today` is `Revenue`'s figure by currency and by plan, with the
    paying accounts. It is one read of `app.billing_paying()`, and
    `domain.Today` uses the same counting as `domain.MRR`, which is now
    built on it.
  - `Collected` and `Converted` read two new `SECURITY DEFINER`
    functions, `app.billing_collected()` and `app.billing_converted()`.
    Like `app.billing_summary()`, they return sums and store IDs only.
  - `invoice_lines` gains a read-only policy for its owner, as
    `invoices` already has (00060), so the function can read every
    account's lines.
- **Tenancy:** `Directory.StoresByMonth` counts the stores per Cairo
  month. In the same query, it counts how many of them are in billing's
  list of stores that converted. The console never reads billing's
  tables.
- **Admin:** a River job, `admin.growth_snapshot`, runs every hour in
  the worker (and when the worker starts):
  - It saves today's recurring revenue for each currency in
    `admin_growth_mrr`. The day keeps its latest reading, and a currency
    with nothing is saved as 0.
  - On the month's first reading, it saves the paying accounts in
    `admin_growth_paying`, and the reading's date in
    `admin_growth_months`.
  - A month counts for churn only when it and the next month were both
    read in their first 3 days (`ChurnGraceDays`). A worker down on the
    1st is caught up; a set read later in the month would misstate
    churn.
- **The console:** `/console/growth` («النمو», under الإيراد, for
  `billing.read`) follows the approved mockup:
  - one currency at a time, switched only among those with figures;
  - four numbers for this month;
  - bars per month, the month under way lighter, each with its figures
    in a table under «عرض الأرقام»;
  - the recurring revenue as a line since recording began;
  - conversion for the six months before this one;
  - churn as a table once a month is complete, and until then the
    month it starts;
  - each plan's share of the currency's revenue.

  The bars are HTML and the line is SVG, with no chart library. The
  home's growth block links to the page.

### Cost

Measured on 5,000 stores with 48,000 paid invoices, a year at 4,000
paying accounts. The data was seeded in a transaction and rolled back.

| Query | Time | Plan |
|---|---|---|
| `app.billing_collected()` | 17 ms | seq scan of `invoices`; the window covers most of it |
| `app.billing_converted()` | 12 ms | hash join of `invoices` and `invoice_lines` |
| stores per month, with 4,000 IDs that converted | 24 ms | seq scan of 4,073 stores |

The page is read on demand, by the console only. The hourly job does
one `app.billing_paying()` read (about 16 ms, ADR-0075) and a few
writes.

## Consequences

- The recurring revenue line starts on the day this ships. Churn starts
  with the first complete month after that: December 2026's figure
  appears on 1 January 2027.
- A store under an agency converts only if one of the agency's paid
  invoices has a subscription line for that store.
- The monthly sets keep about 4,000 rows per month at today's size. If
  they grow too large, the old months can be summed into counts.
