# ADR-0083: Collecting pay-as-you-sell fees: settings and prepaid balances

- Status: Accepted
- Date: 2026-11-04

## Context

ADR-0048 earns a fee on each delivered order, and ADR-0049 invoices the
month's fees, which ADR-0051 charges to the card on file. The owner
approved (2026-11-04) two ways for a pay-as-you-sell merchant to pay:
- **A prepaid balance.** The merchant tops it up, and each delivered
  order's fee is taken from it.
- **The card on file, with a minimum.** It is charged when the fees due
  reach the minimum, or at the month's end, whichever comes first.

Each way is switched on or off from the console. The approved points:
- **Both on:** the merchant chooses.
- **One minimum per currency:** it is both the least top-up and the
  card's threshold. It starts at EGP 300, SAR 100 and AED 100, and the
  console can change it.
- **Low balance:** the notice is at half the minimum.
- **Turning a way off** changes nothing for merchants who already chose
  it.
- **The balance may go below zero** through delivered orders, because an
  order is never refused. The next top-up covers the debt.
- **Platform staff can add to a balance by hand,** with a reason, and it
  is audited.
- **Agencies have no balance:** one invoice pays for their stores.

This ADR covers the data and the console (F2). Taking fees from the
balance and topping it up are F3; the card's threshold is F4; the
merchant's choice is F5.

## Decision

1. **The settings are platform data.**
   - They live in one `billing_settings` row: `prepaid_enabled`,
     `card_enabled`, and a minimum per currency (1.00 to 1,000,000.00).
   - Only `app_admin` may change them; merchants' code reads them.
   - Both ways start **off**.
   - The console uses them through `billing.NewCollection(adminPool)`.
2. **The balance belongs to the account** (ADR-0049's wall).
   - `billing_accounts.balance_minor` holds it. `billing_accounts.collection`
     holds the way the account chose, `NULL` until F5.
   - Every change takes the account row with `FOR UPDATE` and writes a
     `balance_entries` row with the amount (signed) and the balance after
     it. Concurrent changes therefore add up exactly.
   - Entries are append-only, and stamped with `clock_timestamp()` under
     the lock, so they sort in the order they changed the balance.
   - The entry kinds are `topup`, `manual`, `fee` and `fee_refund`.
3. **Levels.** A balance is `ok`, `low` (under half the minimum) or
   `debt` (zero or less).
4. **The console.**
   - `GET/PUT /admin/v1/billing/collection`: reading needs
     `billing.read`, saving needs `billing.manage` and a reason. The save
     is recorded in the admin log first.
   - `GET /admin/v1/stores/{id}/balance`: the balance and its latest 50
     entries (`billing.read`).
   - `POST /admin/v1/stores/{id}/balance/credits`: adds to the balance
     (`billing.manage`). It takes an amount, a reason (`transfer`,
     `compensation` or `correction`), a reference and a note.
     - It is recorded in the admin log first, and the entry keeps the
       admin's ID.
     - It opens the store's own account if the store has none.
     - A store with no billing row is `409 no_billing`. An agency's store
       is `409 agency_managed`.

## Consequences

- **A debt is a negative balance,** not a separate ledger. A top-up or a
  credit covers it first, simply by adding to it.
- **Nothing is collected yet.** With both ways off, and no account
  choosing a way, merchants see no change.
- **F3 must reconcile the balance with the monthly invoice:** the
  invoice is the tax document of fees already taken from the balance, so
  it must not charge them again.
