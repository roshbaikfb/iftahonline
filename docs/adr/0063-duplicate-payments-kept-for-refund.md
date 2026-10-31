# ADR-0063: Duplicate payments are kept for a refund

- Status: Accepted
- Date: 2026-10-31

## Context

The P7 review found that a payment could reach an invoice that was no
longer open, and that it was then dropped without a trace. Three ways
lead there:
- the owner opens the gateway's page twice and pays on both;
- the owner pays on the page while the hourly run charges the card on
  file;
- the owner pays after the console settled a transfer.

`recordAttempt` saw the invoice was not open and returned, recording
nothing. The gateway still took the money, and there was no attempt, no
alert and no refund.

The owner decided (2026-10-31) that such a payment is shown in the
console for an admin to refund by hand. It is not refunded
automatically.

## Decision

1. **A payment the gateway took for an invoice no longer open is a
   `duplicate` attempt.**
   - `recordAttempt` writes it, under the invoice's lock, with the
     gateway's transaction as its reference.
   - It is written once per (invoice, gateway, transaction), so a
     callback delivered again adds nothing.
   - It logs an error, and the callback's receipt is marked `duplicate`.
   - The invoice stays paid once.
   - A declined charge of such an invoice records nothing, as before.
2. **A transfer settled on an invoice no longer open is refused** with
   404. Only money the gateway actually took is a duplicate.
3. **The console lists duplicates and records refunds.**
   - `GET /admin/v1/billing/duplicates` lists them, those to refund
     first, at most 200.
   - Each entry gives the store, the invoice number, the amount, and the
     gateway's transaction to refund.
   - The admin refunds it on the gateway, then calls
     `POST …/{id}/refunded` with the refund's reference and a reason. The
     admin log records it first (`payment.refunded`), and it is recorded
     once.
   - The health page shows the section only while one waits for a
     refund.
4. **Migration 00069.**
   - The outcome CHECK accepts `duplicate` (NOT VALID).
   - New columns `refunded_at` and `refund_reference`. `app_user` may
     update only these two.
   - The console reads every account's duplicates through
     `app.duplicate_payments()`.
   - It finds one's account through `app.payment_attempt_account(id)`,
     then marks it refunded inside that account's wall.

## Consequences

- No payment is lost. The worst case is now a refund an admin does by
  hand, with the transaction in front of them.
- Two pages can still be opened for one invoice. Reusing an open page
  would need its URL stored with the checkout. That can follow if
  duplicates turn out to be frequent.
- A refund is not checked against the gateway. The reference is what the
  admin typed, and the admin log keeps who typed it and why.
