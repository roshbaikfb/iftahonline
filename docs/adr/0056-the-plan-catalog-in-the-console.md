# ADR-0056: The plan catalog in the console

- Status: Accepted
- Date: 2026-10-31

## Context

Plans are data (ADR-0045). The spec asks the super admin to manage
plans, and the user asked to be able to add features of their own. The
seeded prices and limits are placeholders that must be set before
launch.

ADR-0045 fixed three rules:

- A change to a plan is a new version.
- A subscription keeps the version it bought until it renews.
- Merchant code (`app_user`) only reads plans.

Until now nothing wrote the catalog, and a renewal charged the version
the store already had, so a new version would never have reached an
existing store.

## Decision

1. **The catalog lives in billing and runs as the console's role.**
   - **Where it is.** `billing.NewCatalog(adminPool, ids)` returns
     `billing.Catalog`, which lists features and plans, saves a feature,
     publishes a version and retires a plan.
   - **Grants.** Migration 00063 gives `app_admin` INSERT and UPDATE on
     `features` and `plans`, and INSERT on `plan_prices`,
     `plan_entitlements` and `plan_fees`. It gives no DELETE: a version a
     store bought stays as it was sold.
   - **Who calls it.** The console calls it through billing's `api.go`.
     It never touches billing's tables itself.
2. **Features.**
   - The console may add a key or rename and reorder one.
   - A key's kind never changes (409).
   - `entitlements.Enforced(key)` tells the console which keys the code
     checks. A key the console adds is shown on plans but enforced by
     nothing until a module asks for it.
3. **Publishing a version.** Publishing writes the next version of a
   code, or version 1 for a new code, under a per-code advisory lock. In
   one transaction it:
   - retires the version on sale;
   - inserts the new version with its prices, grants and fees.

   The rules it enforces:

   | Rule | Answer when broken |
   |---|---|
   | A code keeps its kind | 409 |
   | One free plan is on sale at a time (the existing unique index) | 409 |
   | Grants use catalog keys, and a flag is 0 or 1 | 422 |
   | A paid plan sells a monthly price in every currency | 422 |
   | Pay-as-you-sell has a fee in every currency and no price | 422 |
   | The free plan sells nothing | 422 |
   | A trial is 0 to 90 days, and only on a paid plan | 422 |
4. **Retiring a plan.**
   - Retiring takes a code off sale. Its stores keep their version and
     renew into it.
   - The free plan cannot be retired, because stores fall back to it. It
     can only be replaced by a new version.
5. **Renewal moves to the newest version.**
   - **On the invoice.** `RenewalDue` prices a renewal at the newest
     active version of the subscription's code that sells its cycle in
     the account's currency. When there is none, it uses the
     subscription's own version.
   - **Remembering the version.** The invoice records that version in
     `subscriptions.renewal_plan_id`.
   - **On payment.** Paying the invoice moves the subscription to that
     version. Caches follow through `SubscriptionChanged` (ADR-0050).
   - **Result:** the price paid and the grants received always match.
6. **Audit first.** Every catalog change goes to the admin log with a
   reason before billing is called, as suspension does:
   - `feature.saved`;
   - `plan.published`;
   - `plan.retired`.

   A refused change still shows as an attempt.

## Consequences

- **Free stores change within 30 s.** They have no subscription and read
  the active free version. A new free version reaches them when their
  entitlements cache expires (30 s); their storefront pages follow on
  their own TTL.
- **Paid stores change at renewal.** They see a new version, price
  included, at their next renewal. They are not told in advance yet;
  that notice is for the billing page (7f).
- **No fixing mistakes in place.** A wrong version is fixed by publishing
  another one; versions are never edited or deleted.
- **Agency renewals.** An agency's renewal follows the agency plan's
  newest version like any other.
