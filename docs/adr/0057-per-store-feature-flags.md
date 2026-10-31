# ADR-0057: Per-store feature flags

- Status: Accepted
- Date: 2026-10-31

## Context

The P7 spec gives the super admin "per-store feature flags". Support
needs two things a plan cannot express:
- **Giving one store something:** card payments for a pilot merchant, a
  higher product limit for a migration, unlimited orders for a launch
  week.
- **Taking something away:** coupons from a store that abuses them.

Plans are shared data (ADR-0045, ADR-0056). Editing one plan for one
store is not possible, and inventing a plan per store would bury the
catalog.

## Decision

1. **Entitlements owns the overrides, not billing.**
   - **The table.** Migration 00064 adds `entitlement_overrides`, a
     tenant table with RLS and one row per (store, feature):
     - `value` (NULL is unlimited);
     - `expires_at` (NULL means until it is cleared);
     - the admin's `reason`.
   - **Why entitlements.** An override is not about plans. It changes
     the final answer to "may this store do X", which is entitlements'
     job. Billing keeps answering only "what does the plan grant".
2. **How overrides combine with the plan.**
   - `grantsTx` reads the plan's grants, then the store's overrides (one
     more query on a cache miss).
   - It applies the overrides active now in place of the plan's grants
     (`Grants.With`).
   - The result is cached as before.
3. **Only enforced features.**
   - An override must name a feature the code checks. A display-only
     key would change nothing, so it is refused with 422.
   - This is also why the table has no FK to the catalog, which billing
     owns.
   - A flag override is 0 or 1. A deadline must be in the future.
4. **Changes reach every cache.**
   - Setting or clearing an override publishes
     `entitlements.grants.changed` in the same transaction (the outbox).
     The instance that made the change also drops the store from its
     cache at once.
   - In `cmd/platform`, the event goes to the same three handlers as
     `billing.subscription.changed` (ADR-0050):
     - the entitlements invalidation over Redis;
     - the storefront's cache invalidation;
     - the edge purge of the store's pages.
5. **The console** (`/admin/v1/stores/{id}/entitlements`):
   - **Reading** shows the plan's grants, the overrides (expired ones
     included, marked inactive) and the effective grants.
   - **`PUT …/{feature}`** sets an override; **`POST …/{feature}/clear`**
     removes it.
   - **Audit first.** Each change goes to the admin log with its reason
     before entitlements is called:
     - `store.feature_overridden`;
     - `store.feature_override_cleared`.

## Consequences

- **Expiry is not instant.** No event fires when an override expires.
  Checks follow within the entitlements cache's 30 s, and the storefront
  pages within their edge TTL.
- **Unlimited monthly quotas.** An override can lift a monthly quota
  (`orders_per_month`, `messages`) to unlimited. The usage counters keep
  counting, so removing the override applies the plan's limit to the
  month already counted.
- **Overrides outlive plan changes.** An override stays when the store
  changes plan. The console shows both the plan's grant and the override.
