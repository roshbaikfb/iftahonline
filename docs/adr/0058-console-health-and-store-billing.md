# ADR-0058: The console's health page and a store's billing

- Status: Accepted
- Date: 2026-10-31

## Context

The P7 spec asks for a health page in the super-admin console.
ADR-0051 promised that the platform could confirm a payment made outside
the card, such as a bank transfer, and that the console would do it.
Support also needs to see what a merchant sees on their billing page.

The console's role (`app_admin`) has three limits:
- it reads the store directory and the plan catalog, nothing else;
- billing's tables sit behind the tenant and account walls;
- River's job table belongs to the platform's owner.

## Decision

1. **The health page is counts only.** `GET /admin/v1/health` returns:

   | Section | What | Read by |
   |---|---|---|
   | `database` | The console's database answers (the store counts query succeeded) | admin pool |
   | `redis` | A ping | admin's Redis |
   | `stores` | Active, held for billing, suspended, closed, total | `tenancy.Directory.Counts` (its `admin_read` policy) |
   | `billing` | Subscriptions by status; open and overdue invoices | `billing.Console.Health` through `app.billing_health()` |
   | `jobs` | Waiting, running and retryable jobs, discarded in the last 24 h, and how long the oldest due job has waited | `app.job_health()` |

   - **The two functions** (migration 00065) are `SECURITY DEFINER` and
     return numbers only.
     - `app.billing_health()` is granted to `app_user` and reads through
       the existing owner `billing_lookup` policies.
     - `app.job_health()` is granted to `app_admin`.
   - **No role gains read access to rows**, and each module still counts
     its own tables.
   - **Failures.** Each check has a 3 s timeout. A check that fails is
     logged, its section is left out and named in `errors`, and the page
     still answers 200: it is for when things are broken.
2. **A store's billing.** `GET /admin/v1/stores/{id}/billing` returns
   the merchant's own `Billing` view (`billing.Console.Summary`): plan,
   status, period, card and open invoices.
3. **Settling an invoice.**
   - **The call.** `POST /admin/v1/stores/{id}/invoices/{invoice}/settle`
     takes a transfer reference and a reason.
   - **In billing.** `SettleStoreInvoice` checks that the invoice is open
     in the store's account (404 otherwise). It then records a `manual`
     attempt, so the stores settle as on any payment (ADR-0051): the next
     period when the invoice renewed them, back to active, and released
     from the hold.
   - **Audit first.** The console writes `invoice.settled` to its log
     first, with the reason and the reference.
   - **Wiring.** The API role's billing module now has the holds and the
     event bus, which settling needs.

## Consequences

- **The page is built on demand.** It runs a handful of counts on each
  request. At 5,000 stores the scans are small, and the page is for
  platform staff only.
- **Agencies come next.** Their stores share one account, so settling
  from any of them pays the agency's invoice. Setting up an agency comes
  in 7e-3b.
