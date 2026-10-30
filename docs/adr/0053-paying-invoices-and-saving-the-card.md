# ADR-0053: Paying an invoice on Paymob's page, and saving the card

- Status: Accepted
- Date: 2026-10-30

## Context

ADR-0051's dunning charges an account's card on file, and ADR-0052
charges it with Paymob MOTO. Something must put a card on file first,
and a merchant must be able to pay, for instance to bring a suspended
store back.

Paymob's "Pay with saved card" collection describes how a card is
saved:
- a 3DS Intention is paid on the Unified Checkout with "save card";
- two callbacks reach the Intention's `notification_url`:
  - the transaction, which carries our `special_reference` as
    `order.merchant_order_id`;
  - the card **token**, signed over its 8 fields, which carries only
    Paymob's `order_id`.

Both callbacks come to **one** platform URL for every store
(ADR-0038's platform webhooks). CLAUDE.md says to re-query the provider
before marking anything paid.

## Decision

1. **`payInvoice`** (`POST /v1/stores/{id}/billing/invoices/{invoice}/pay`,
   permission `billing.manage`, the owner's alone) opens a checkout.
   - It inserts a `billing_checkouts` row (tenant) with our reference,
     `bc_<ULID>`, for an open invoice of the store's own account.
   - It creates the Intention on the region's 3DS card integration, with
     `notification_url` = `/v1/webhooks/paymob_billing` and the
     dashboard's billing page to come back to.
   - It records Paymob's order (`intention_order_id`) when the answer
     carries it.
   - The answer is `{checkout_url}`.
   - Invoices that are not open, invoices of another store, and stores
     never invoiced are 404. A currency without a checkout is 503.
2. **`getBilling`** returns what the dashboard (7f) shows: the plan's
   names, the status and its dates, the card on file's label, and the
   open invoices.
3. **The callbacks** reach billing's `PlatformProvider`, `paymob_billing`.
   - It verifies the signature with any of the platform's regions'
     HMAC secrets, over the callback type's field list.
   - It places the callback in its store through
     `app.billing_checkout_store(reference, order)`: `SECURITY DEFINER`,
     returning the store only, by our reference (transactions) or
     Paymob's order (tokens).
   - A callback of no checkout is dropped, with a log line.
4. **`billing.process_callback`** acts on the callback.
   - **A token:** it becomes the account's card on file, sealed, with the
     label "brand •••• last four".
   - **A transaction:** the transaction callback first records Paymob's
     order on the checkout. Then billing **asks Paymob**, by inquiry of
     the reference: the full amount, not pending, not voided.
     - Only a confirmed payment is recorded, as an approved attempt that
       settles the invoice (renewal, restore, release: ADR-0051).
     - Anything else marks the checkout `failed` and does not count as a
       dunning failure.
5. **Configuration.** A region's checkout is
   `BILLING_PAYMOB_<CUR>_{PUBLIC_KEY,CARD_INTEGRATION_ID,HMAC_SECRET}`.
   - All three are required, or none, and only on a region that has the
     account's keys.
   - Without it, there is no checkout and no callback provider.

## Consequences

- **The card saved by the first payment pays the next invoices** without
  the merchant (MOTO). `BILLING_COLLECTION_ENABLED` can be turned on once
  a region has both its account and its checkout.
- **The token callback depends on knowing Paymob's order.** That order
  is read from the Intention's answer (`intention_order_id`, to confirm
  in Paymob's test mode) or from the transaction callback.
  - A token callback that arrives before both is dropped. The card is
    then not saved, the payment still is, and the merchant saves the card
    on the next payment.
  - The test-mode run should confirm the field and the order of the two
    callbacks.
- **Merchant code never chooses an account.** The account comes from the
  store's own subscription, behind the tenant wall.
