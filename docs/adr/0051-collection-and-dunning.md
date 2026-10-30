# ADR-0051: Collecting invoices, dunning, and the billing hold

- Status: Accepted
- Date: 2026-10-30

## Context

P7 asks for this lifecycle, with an acceptance test:
- **past_due:** grace and dunning by WhatsApp and email;
- **suspended:** a maintenance page on the storefront, data kept 90 days;
- **cancelled.**

The approved schedule (2026-10-30) retries on days 1, 3 and 5, gives 7
days of grace, then suspends. The approved payment methods are a card
on file through Paymob, a pay link, and a payment the platform confirms
by hand.

Invoices are per account (ADR-0049). The lifecycle is per store, and
`tenancy` owns whether a storefront is served (ADR-0004).

## Decision

1. **Charging.**
   - An invoice is due at once when written (`next_attempt_at =
     issued_at`).
   - The hourly `billing.dunning` lists, through `app.dunning_due(now)`
     (`SECURITY DEFINER`, IDs only):
     - open invoices whose attempt is due;
     - past-due stores whose grace ended;
     - stores suspended for 90 days.
   - It enqueues `billing.charge_invoice` (system), `billing.suspend` or
     `billing.cancel` (tenant), each unique by args.
2. **A charge is an external call.**
   - It is made outside any transaction, with the card token opened from
     `billing_payment_methods`. The token is sealed with
     `platform/envelope`, AAD `billing-method:<account>`.
   - An error means the outcome is unknown: nothing is recorded and the
     job retries.
   - Otherwise one transaction, with the invoice locked:
     - records the `payment_attempts` row (approved, declined,
       `no_method`, manual);
     - then **settles** or **fails** every store of the account.
   - No card on file is a failure like a decline: the merchant is told
     to pay.
3. **Failing** counts the attempt.
   - The next retry is 1, 3 or 5 days after the first failure; there is
     none after the third.
   - Each active store of a billed kind goes past due, with its grace
     counted from the **first** failure. Pay as you sell can now fall
     past due too (`Kind.Billed`).
   - The owners are told when the retry is and when the store will
     close.
4. **Suspending** (the grace ran out unpaid):
   - the store goes `suspended` and is **held for billing**;
   - its grants become the free plan's;
   - its owners are told.

   **Cancelling** (90 days suspended): the store goes `cancelled`, its
   data due for deletion. P7 deletes nothing (approved).
5. **Settling** (paid by card, or a transfer confirmed through
   `SettleInvoice`):
   - the invoice is `paid`;
   - each store that the invoice renewed gets its next calendar period
     (`Settle`), and the renewal mark is cleared;
   - every store is back to `active` and released from the hold;
   - its owners are told.
6. **The billing hold is tenancy's.**
   - It is a new column, `stores.billing_hold`, set through
     `tenancy.Service.SetBillingHoldTx` in billing's transaction.
     `cmd/platform` adapts it (billing imports no module).
   - It is separate from the platform's suspension (`status`), so
     releasing one never lifts the other.
   - `app.resolve_host` keeps its signature and reports a held active
     store as `held`. Host caches decode it.
   - The storefront answers `held` with a 503 maintenance page that says
     nothing about why.
7. **Every lifecycle step is written through `saveTx`** (ADR-0050), so
   grants, pages and the edge follow.
8. **Notices.**
   - `billing.notify` emails the store's owners in the store's language,
     with a link to its billing page. It is enqueued in the transaction
     of the change it is about.
   - The WhatsApp message to the owner needs a template approved for
     the platform's number. It is sent from 7d-2.
9. **Off until cards can be saved.**
   - `BILLING_COLLECTION_ENABLED` (default `false`) turns the hourly
     dunning on.
   - Until 7d-2 lets merchants save a card, no store can be suspended
     for an invoice it had no way to pay.

## Consequences

- **The acceptance scenario is one test** (`TestDunningScenario`):
  - the day-0 charge fails; the store is past due and keeps its plan;
  - the day 1, 3 and 5 retries fail; nothing happens between them;
  - day 7: suspended, held, on the free plan's grants;
  - day 10: the merchant pays; the store is active, released, with its
    next period;
  - three subscription events, and nothing charged again.
- **One hour.** A charge or a suspension happens within the hour after
  it is due.
- **A rolling deploy.** During it, an old binary resolving a held store
  sees an unknown status `held` until it is replaced.
- **A rare double charge.** A card charge racing a manual settlement of
  the same invoice would charge a paid invoice: the second outcome finds
  the invoice no longer open and records nothing. The refund is then by
  hand. This is an accepted edge case; the console (7e) should settle
  only invoices with no charge in flight.
