# ADR-0084: Paying pay-as-you-sell fees from the prepaid balance

- Status: Accepted
- Date: 2026-11-05

## Context

ADR-0083 gave each account a prepaid balance and a way of paying
(`collection`). For an account that pays from the balance, the approved
rules (2026-11-04 and 2026-11-05) are:
- **Each delivered order's fee** is taken from the balance, and a return
  puts it back. The order is never refused, so the balance may go below
  zero.
- **The tax is taken once,** when the month's invoice is written, not
  with each order. The balance then always equals the invoices, with no
  rounding left over: taxing each order and rounding each would differ
  from the invoice's tax by a few piasters.
- **A return after the invoice** puts the fee back at once. The next
  invoice still shows it as a credit, and it is never given back twice.
- **A balance that runs out** puts the store past due for 7 days, then
  on ADR-0082's path (free, then held). A balance that comes back
  restores the store.
- **Notices:** a low balance (under half the minimum) and a balance that
  ran out are told.

## Decision

1. **The fee is taken when it is recorded.**
   - `OrderMoved` records the fee as before (ADR-0048). When the row is
     new and the account pays from its balance, the same tenant
     transaction takes the account's lock and writes a `fee` entry of
     minus the fee.
   - The fee is marked `sell_fees.prepaid`.
   - A fee of 0 takes nothing.
2. **Each entry is written once per order and kind, and once per
   invoice.** Two partial unique indexes on `balance_entries` enforce
   it, and the insert is `ON CONFLICT DO NOTHING`. A repeated or late
   event writes nothing and leaves the balance as it was.
3. **A return.** When a prepaid fee's order comes back:
   - the fee moves `due → cancelled` or `invoiced → credit_due`, as
     before;
   - a `fee_refund` entry puts the fee back at once, even when the
     account no longer pays from its balance.
4. **The invoice is paid from the balance.**
   - It stays the tax document, written by `billing.invoice_account` as
     before.
   - When it is a store account that pays from its balance, and its
     lines are only fees and their credits, the same transaction:
     - marks it paid, with an attempt through gateway `balance`
       (outcome `manual`);
     - writes one `invoice` entry: minus (the total less what the
       invoice holds of prepaid fees, net of prepaid credits). That is
       its tax, and also whatever the balance has not given yet (fees
       from before the account chose the balance);
     - tells the owners it is paid.
   - Anything else is left open and charged to the card, as before.
5. **Following the balance.** Every move (a fee, a refund, an invoice, a
   credit, and in F3b a top-up) runs under the lock, then:
   - **at or below zero, with the store active:** `PaymentFailed`
     (past due for 7 days, then ADR-0082's path), and the
     `balance_empty` notice;
   - **back above zero from at or below it**, with the store past due,
     suspended or cancelled, and no open invoice for the account:
     `Settle` (active, released), and the `balance_restored` notice;
   - **crossing under half the minimum:** the `balance_low` notice.
6. **WhatsApp.**
   - `billing_balance_low_v1` (store, balance) and
     `billing_balance_empty_v1` (store, owed, free-plan date) are new
     templates.
   - `balance_restored` goes by email only.
   - A notice without a template sends no WhatsApp.
7. **Migration 00099.** It adds:
   - `sell_fees.prepaid`;
   - `balance_entries.order_id` and `invoice_id`, with their unique
     indexes;
   - the `invoice` kind.

   Its checks are `NOT VALID`; a later migration validates them.
8. **The console's entry kind is an extensible list** (ADR-0032). The
   new `invoice` kind is accepted as two exact findings in
   `api/openapi-breaking-ignore.txt`, and the console shows a kind it does
   not know as a plain entry.

## Consequences

- **The balance and the invoices agree to the piaster.** A test checks
  that the entries sum to the balance, and that the invoice entry is
  exactly the invoice's tax.
- **A merchant in debt keeps selling.** Their fees deepen the debt. It is
  covered first by the next top-up or credit, which then restores the
  store.
- **Still to come:**
  - **F3b:** topping up on Paymob, and the merchant's balance API.
  - **F4:** the card with a minimum.
  - **F5:** choosing the way, and the dashboard's screens.

  Until F5, no account pays from its balance, so nothing changes for
  merchants. The two new WhatsApp templates must be submitted to Meta
  before collection is turned on.
