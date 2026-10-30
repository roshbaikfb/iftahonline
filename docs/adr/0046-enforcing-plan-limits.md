# ADR-0046: Enforcing plan limits

- Status: Accepted
- Date: 2026-10-30

## Context

ADR-0045 made `entitlements` the one service that answers what a store's
plan allows. P7 7b puts it to work in the modules that create what plans
count:
- products (catalog);
- team members (identity);
- custom domains (tenancy);
- orders a month (orders).

The P7 plan approved (2026-10-30) that a shopper's order is **never**
refused: past the plan's monthly orders, the merchant is warned at 80%
and 100% and asked to upgrade.

Two things needed deciding:
- how a hard limit stays exact when requests arrive together;
- how a soft limit reaches the merchant.

The flag features (card payments, coupons, pixels, feeds) are enforced
in 7b-2 and ads in 7g.

## Decision

1. **Hard limits (counted features) use `AdmitTx(ctx, tx, f, n, count)`.**
   - It runs in the creating module's transaction.
   - It takes a transaction-scoped advisory lock on (store, feature),
     calls the module's own count, and refuses when `count + n` passes
     the grant.
   - The lock serializes one store's creations of one feature, so 20
     concurrent requests against a limit of 3 make exactly 3.
   - An unlimited grant takes no lock and counts nothing.
   - It replaces ADR-0045's `CheckCount`. A check made outside the
     creating transaction could not be exact.
   - Each module counts its own rows, so no module reads another's tables:

     | Feature | Counted by | What counts |
     |---|---|---|
     | `products` | catalog, `createTx` | Every product not deleted, of any status. This covers the API, CSV and WooCommerce imports, and starter samples |
     | `staff` | identity, `Invite` | Members (the owner included) and open invitations |
     | `custom_domains` | tenancy, `AddCustomDomain` | Custom domains. The fixed maximum of 5 stays |

2. **One refusal for every hard limit.**
   - It is an `errs.Error` with kind Forbidden and code `plan_limit`
     (`entitlements.PlanLimitCode`), which renders as **403** problem+json.
   - It wraps `ErrLimitReached`, so callers match it with `errors.Is`.
   - The message names the feature and the grant. An import row shows it
     as that row's error.
   - The dashboard will offer an upgrade on this code (7f).
3. **Soft limits (orders a month) use `CountTx(ctx, tx, f, n)`.**
   - It counts in the caller's transaction and never refuses.
   - When a count crosses 80% or 100% of the grant, it publishes
     `entitlements.usage.crossed` in that transaction, through the outbox.
   - Orders call it after the order's insert. A replayed or duplicate
     submit aborts earlier and is never counted twice.
   - The usage keeps counting past the limit, so 7c can bill overage.
4. **Messaging warns the merchant.**
   - It subscribes to the event (`messaging.plan_limits`) and adds a
     `plan_limit_nearly` or `plan_limit_reached` notice.
   - The notice is kept once per feature, month and threshold, carries the
     feature, and is emailed to the staff with a link to `/billing`.
   - Messaging's own message quota keeps its own warnings (ADR-0038).
5. **Wiring.**
   - `cmd/platform` builds **one** entitlements service per process. It
     passes it to every module, so they share one cache.
   - A module built without it (its own tests) gets
     `entitlements.Unrestricted()`: everything allowed, nothing counted,
     no database.
6. **No import cycles.** `entitlements` imports `billing`, and `billing`
   must not import a module that imports `entitlements` (orders, catalog,
   identity, tenancy). When billing needs one of them (7c: delivered
   orders), it receives a small interface wired in `cmd/platform`.

## Consequences

- **Exact limits, at a small cost.** The cost is one advisory lock per
  creation, for stores whose grant is not unlimited. Creations of other
  features, and of other stores, do not wait.
- **A downgrade does not delete anything.** A store already past a limit
  keeps what it has and cannot add more until it is under the limit.
  Updating existing rows, including import rows that match existing
  products, is unaffected.
- **Grant changes take up to 30 s** on another instance (ADR-0045 §4),
  until the plan-change events of 7d and 7e invalidate the cache.
- **New notice kinds** (`plan_limit_nearly`, `plan_limit_reached`) and a
  `feature` field are added to the API's `Notification`. Old clients see
  unknown kinds; the dashboard shows them.
