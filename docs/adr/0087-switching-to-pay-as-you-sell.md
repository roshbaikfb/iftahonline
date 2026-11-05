# ADR-0087: Switching an unpaid store to pay as you sell, and choosing the way to pay

- Status: Accepted
- Date: 2026-11-05

## Context

The owner approved (2026-11-04) what an unpaid store is offered:
- **A one-click move to «ادفع حسب مبيعاتك».** No subscription, a fee on
  delivered orders, and the unpaid renewal invoice is voided.
- **When:** during the 7-day grace and the 30 free days (ADR-0082),
  until the storefront is held.
- **Agencies' stores** do not switch themselves: platform staff switch
  them from the console (F5c).
- **The way to pay the fees** is the balance or the card (ADR-0083,
  ADR-0084, ADR-0086), and the merchant chooses one when both are
  offered.

## Decision

1. **The rule is the domain's.**
   - `CanSwitchToPays` is true for a store past due, or suspended and
     not held, that is neither on pay as you sell nor an agency's.
   - `SwitchToPays(p)` makes the store active on `p` with nothing else
     carried over except whether the trial was used.
2. **`POST /v1/stores/{id}/billing/switch-to-pays`** (`billing.manage`)
   takes `collection`. In one tenant transaction it:
   - opens the store's own account if it has none;
   - checks the way (point 4);
   - locks and voids the account's open invoices, and makes their fees
     due again and their credits waiting again, so they go on the next
     invoice under the new way;
   - clears the renewal mark;
   - saves the new subscription (`saveTx`: grants, caches, events);
   - releases the hold;
   - records the way.

   Refusals:
   - 409 `not_switchable` once held, or when not unpaid;
   - 409 `agency_managed` for an agency's store.
3. **`PUT /v1/stores/{id}/billing/collection`** (`billing.manage`)
   changes the way for an active pay-as-you-sell store (409 `not_pays`
   otherwise).
4. **The way is checked in this order:**
   - **Leaving the balance** waits while fees taken from it have no
     invoice yet (409 `prepaid_pending`). Their invoice, paid from the
     balance, still has to take their tax (ADR-0084).
   - **Offered:** the way must be offered now, or already be the
     account's own (409 `collection_not_offered`).
   - **The card** needs one saved (409 `card_required`).
5. **The billing summary** (`GET /v1/stores/{id}/billing`) gains:
   - `agency`;
   - `collection`;
   - `switch_offer` (`rate_bps` in the store's currency, and the ways
     `offered`).

   `switch_offer` is present only while the store may switch, pay as you
   sell is on sale with a fee in that currency, and a way is offered.
   The dashboard shows its banner and dialog from these fields (F5b).
6. **An agency's store is sent to the platform's WhatsApp.** The summary
   carries `support_whatsapp`, the number the console sets on the site's
   settings, for an agency's store only.
   - Billing does not read the site's tables: `cmd/platform` gives it a
     `Support` reader adapted from `site.NewContacts`.
   - The read is best effort. Without a number, the banner shows its text
     alone.
   - The dashboard's button opens WhatsApp with a message ready to send,
     naming the open invoice.

## Consequences

- **The switch costs the merchant nothing it owed for the plan.** Fees
  already earned stay due and are paid the new way.
- **No new migration.** The switch uses the voiding and collection
  columns that exist.
- **Founding membership** is untouched by the switch. A founding member
  who stops paying already loses it on the unpaid renewal (ADR-0074).
