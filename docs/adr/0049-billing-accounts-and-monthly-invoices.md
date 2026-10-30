# ADR-0049: Billing accounts and monthly invoices

- Status: Accepted
- Date: 2026-10-30

## Context

P7 asks for monthly invoices, and for agency accounts: many stores, one
invoice. The owner approved (2026-10-30) these rules:
- **One currency per agency.** Only stores of that currency join it.
- **VAT as data per country:** Egypt 14%, Saudi Arabia 15%, the UAE 5%.
  E-invoicing (ETA, ZATCA) is not part of P7.
- **Gapless invoice numbers.** The number is a legal document number;
  API IDs stay ULIDs.

An invoice belongs to whoever pays, not to one store. Tenant RLS
(ADR-0002) keys every row on `store_id`, so an agency invoice cannot
live in one store's wall.

## Decision

1. **Accounts have their own wall.**
   - The account tables are `billing_accounts`,
     `billing_account_stores` (a store is in one account), `invoices` and
     `invoice_lines`.
   - They have no `store_id`. Their RLS is forced, with an
     `account_isolation` policy on `app.current_account_id()`. That reads
     `app.account_id`, which the billing module sets per transaction.
   - The account ID comes only from the store's own subscription
     (`subscriptions.account_id`, behind the tenant wall) or from a
     system job's args. It never comes from a request.
   - A transaction may set both GUCs, and switch `app.store_id` from one
     member store to the next. This is how an agency's invoice is written
     in the account's wall while each store's fees are marked in its own,
     atomically.
2. **Every billed store has an account.**
   - A store gets its own (`kind = store`) the first time it has
     something to bill.
   - An agency's stores share the agency's account, which the console
     sets up (7e).
3. **Paid periods are calendar months** (or twelve of them for annual),
   starting on the 1st, so one invoice a month can hold everything:
   - **renewals:** each store's paid plan whose period ends by the next
     month's first day, at its plan version's price for its cycle. An
     agency's plan is billed once per account. A billed renewal is marked
     (`subscriptions.renewal_invoice_id`), so it is never billed twice
     while unpaid. 7d clears the mark when the period is paid.
   - **fees:** every `due` sell fee up to the month, so a fee recorded
     after its month's invoice (a late event) goes on the next one.
   - **credits:** fees of orders returned after they were invoiced, as
     negative lines.
   - **tax:** the account country's VAT, rounded half up.
   - **When nothing is billed:** with nothing to bill, or with credits as
     large as the charges, no invoice is written and the credits wait.
4. **Jobs.**

   | Job | Scope | What it does |
   |---|---|---|
   | `billing.invoice_month` | daily, and at worker start | Lists the billable stores of the previous month through `app.billable_stores(period)`, a `SECURITY DEFINER` function that returns store IDs only. It is read through read-only `app_owner` policies, as `app.confirmation_store` does |
   | `billing.invoice_store` | tenant | Gives the store its account if it has none, and asks for that account's invoice |
   | `billing.invoice_account` | system | Writes the invoice once (`UNIQUE (account_id, period)`) |

   - Jobs are unique by args, and every step is idempotent, so a daily
     run after the 1st does nothing new.
   - Store names are read before the invoice's transaction, so a job
     never holds two connections.
5. **Numbers** are `INV-<year>-<n>`, from `invoice_counters`. The year's
   row is updated in the invoice's own transaction, so a rolled-back
   invoice leaves no gap.
6. **Due dates.** An invoice is `open`, due three days after issue
   (`PaymentTerm`). 7d charges it and runs dunning.

## Consequences

- **Agencies:** one invoice per account and month, each store on its own
  lines. The cross-tenant reach of the monthly run is limited to store
  IDs.
- **Two walls,** with the same rule: the key comes from the server,
  never from the client. A test checks that every account table forces
  its policy.
- **Mid-month starts.** A paid plan bought mid-month needs its first,
  partial period billed when it is bought. That is part of 7d's checkout,
  not of this monthly run.
- **Overage fees** (approved, default 0) are not billed while no plan
  sets a price for them.
