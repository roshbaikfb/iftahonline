# ADR-0082: Unpaid stores fall back to the free plan before they close

- Status: Accepted
- Date: 2026-11-04
- Amends: ADR-0051 §4 (suspending)

## Context

ADR-0051 closes a store the moment its 7-day grace runs out unpaid. The
store is suspended and held at once, and its shoppers see the
maintenance page (#168).

The owner decided (2026-11-04) that closing at once is too harsh. A
store that stops paying should keep selling as a free store for a while,
then close.

The free plan as built (ADR-0046, ADR-0047, ADR-0062) never hides or
deletes anything on a downgrade:
- products over the limit stay, but no new ones can be added;
- orders a month are counted, never refused;
- custom domains keep working;
- the store loses card payments, coupons, pixels and feeds, and shows
  the platform's ads.

A suspended store already gets the free plan's grants
(`PlanApplies` is false), and ads follow the `ad_free` grant. So a
suspended store that is not held already **is** an ordinary free store.

The owner approved option (A): an ordinary free store, with no stricter
rules for a lapsed store.

## Decision

1. **The lifecycle.**

   | Day | Step | Store |
   |---|---|---|
   | 0 | a charge fails (`PaymentFailed`) | past due: keeps its plan, retried on days 1, 3 and 5 |
   | 7 | the grace ends (`Suspend`) | suspended: the free plan's grants, **still open** |
   | 37 | 30 more days unpaid (`Hold`) | suspended and held: the maintenance page |
   | 97 | 90 days from the suspension (`Close`) | cancelled; held if it was not |

   A payment at any step settles the invoice and restores the store, as
   in ADR-0051 §5.
2. **The policy.** `Policy.FreeFor` is 30 days, next to `Grace` (7) and
   `Retention` (90). Like them, it is fixed in code and in
   `app.dunning_due`.
3. **When the store was held is billing's.**
   - `subscriptions.held_at` is when billing held the store.
   - `Held()` is now "suspended or cancelled, and `held_at` is set". It
     used to be "suspended or cancelled".
   - Tenancy's `stores.billing_hold` is still the switch the storefront
     reads (ADR-0051 §6). Billing sets it in the same transaction as
     `held_at`.
4. **A new hourly task.** `app.dunning_due` adds `hold`: suspended
   stores with no `held_at`, suspended at least 30 and less than 90 days
   ago. The `billing.hold` job takes the step and enqueues the `held`
   notice. A store already due for cancelling is held by its cancel.
5. **Migration 00097.**
   - It adds `held_at` and a partial index for the `hold` task.
   - Stores suspended or cancelled before it were held at once, so they
     get `held_at = suspended_at`.
6. **Notices.**
   - **Payment failed:** says when the store moves to the free plan
     (the grace's end) and when it closes (`closes_at`).
   - **Suspended:** "your store is on the free plan", with the closing
     day. Its WhatsApp template is the new `billing_store_free_v1`
     (store, closing day).
   - **Held:** the old "store closed" text. It uses the existing
     `billing_store_suspended_v1`.
   - The payment-failed template's `{{4}}` ("closes on") is now the
     day the storefront closes, so the text stays true unchanged.
7. **The API.** `Billing` gains two optional fields, `closes_at` and
   `held`. They are additive, so `/v1` does not break. The dashboard's
   billing banner tells the three states apart: past due, free until
   `closes_at`, and closed.

## Consequences

- **A lapsed store keeps selling for 30 days,** with the platform's
  ads. That is 30 more days for the merchant to pay or (F5) to switch
  to pay as you sell.
- **`billing_store_free_v1` must be submitted to Meta** before
  `BILLING_COLLECTION_ENABLED` is turned on. Until it is approved, that
  one WhatsApp notice fails and is logged (best effort, ADR-0054); the
  email still goes.
- **A rolling deploy.** An old binary running the hourly dunning would
  still hold a store at suspension. Collection is off by default
  (`BILLING_COLLECTION_ENABLED=false`), so no store is affected in
  practice.
- **Still to come** (the approved plan): collection settings and
  balances in the console (F2), the prepaid balance (F3), the card with
  a minimum (F4), and the one-click switch to pay as you sell during the
  grace and the free days (F5).
