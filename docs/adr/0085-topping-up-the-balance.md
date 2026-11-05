# ADR-0085: Topping up the prepaid balance on Paymob's page

- Status: Accepted
- Date: 2026-11-05

## Context

ADR-0084 takes pay-as-you-sell fees from an account's balance. The
merchant tops the balance up:
- by card on Paymob's page;
- by a transfer the platform confirms, which the console's credit
  already covers (ADR-0083).

The minimum per currency is the least top-up (approved 2026-11-04).

Paying an invoice on Paymob's page already exists (ADR-0053):
- a `billing_checkouts` row with our reference `bc_<ULID>`;
- Paymob's callbacks placed by that reference;
- re-querying Paymob before anything counts as paid.

## Decision

1. **A top-up is a checkout with no invoice.**
   - `billing_checkouts.invoice_id` becomes nullable.
   - `purpose` (`invoice` or `topup`) is checked against it.
   - The same callback URL, placing and inquiry serve both purposes.
2. **`POST /v1/stores/{id}/billing/balance/topups`** (`billing.manage`,
   so the owner) takes `amount_minor`.
   - It opens the store's own account if it has none. An agency's store
     is 409 `agency_managed`, and a store with no billing row 409
     `no_billing`.
   - It is refused with 409 `prepaid_off` unless the platform offers the
     balance or the account already chose it.
   - The amount must be between the currency's minimum and
     1,000,000.00 (422 otherwise).
   - The answer is `{checkout_url}`. Paymob sends the merchant back to
     `/billing/balance`.
3. **A confirmed top-up is added once.**
   - The transaction callback is a hint: Paymob is asked first, as for
     invoices.
   - In one transaction:
     - the checkout is marked paid, unless it is paid already. A
       checkout that an earlier callback found unpaid can still be paid;
     - the `topup` entry is written under the balance's lock, with
       Paymob's transaction as its reference and the checkout as
       `checkout_id`, which has a unique index;
     - the `topped_up` notice is sent by email.
   - A repeated callback finds the checkout paid and adds nothing (its
     receipt's outcome is `duplicate`).
   - The move goes through ADR-0084's follow: a top-up that covers a
     debt restores the store.
4. **`GET /v1/stores/{id}/billing/balance`** (`billing.manage`) shows:
   - the balance, its level and minimum;
   - the way the store pays;
   - the ways offered now;
   - the latest 50 entries, without who added them.

   The entry kind is an extensible list (ADR-0032).
5. **Migration 00100** adds:
   - the checkout's `purpose`;
   - `balance_entries.checkout_id`.

   It also validates 00099's checks.

## Consequences

- **One callback path** serves both invoices and top-ups. The gateway
  adapter is unchanged.
- **Unverified against Paymob.** As with ADR-0053, the flow is tested
  against the stand-in. Run it in Paymob's test mode before launch.
- **The dashboard's balance page** (top-up amounts, the entries) comes
  with F5, from the approved mockup.
