# ADR-0086: The card with a minimum

- Status: Accepted
- Date: 2026-11-05

## Context

The second way for a pay-as-you-sell merchant to pay fees (approved
2026-11-04): the card on file is charged when the fees due reach the
currency's minimum, or with the month's invoice, whichever comes first.
A decline follows the dunning: grace, then free, then held (ADR-0051,
ADR-0082).

Two things already exist:
- **Monthly invoices** are one per account and period, but only for
  `purpose = 'monthly'` (`invoices_monthly_key`).
- **The hourly dunning** charges any open invoice whose attempt is due.

## Decision

1. **A fees invoice.** A new invoice purpose, `fees`, written when the
   fees due reach the minimum. An account may have several in a month.
2. **Checked on each new fee.**
   - `OrderMoved` records a delivered order's fee. In the same tenant
     transaction, when the store's own account pays by card
     (`collection = 'card'`), billing:
     - takes the account's row lock;
     - sums the store's due fees up to the current month, before tax;
     - if they reach the minimum, writes an invoice of those fees and
       their credits, with the account country's tax, and marks them
       invoiced.
   - The lock serializes the check with other deliveries and with the
     month's invoice, which now takes the same lock. A fee is never on
     two invoices.
   - The account's name is the line's store name, so the transaction
     reads no other module.
3. **Charged as any invoice.** It is due at once (`next_attempt_at =
   issued_at`), so the dunning charges the card within the hour. A
   decline puts the store past due, as for any invoice.
4. **The month's end.** The monthly run bills whatever fees are still due,
   as before. A fees invoice leaves none of its fees for it.
5. **Migration 00101** replaces the purpose check, adding `fees`, as
   `NOT VALID`.

## Consequences

- **A merchant with large sales pays during the month,** in minimum-sized
  steps, rather than one large charge at its end.
- **The minimum is compared before tax.** The invoice adds the tax, so
  the charge is a little more than the minimum.
- **A card account with no card** fails its first charge (`no_method`) and
  goes past due. F5 lets a merchant choose the card only with a card
  saved.
- **The dashboard's choice of the way to pay** comes with F5.
