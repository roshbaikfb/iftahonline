# ADR-0050: Plan changes reach every cache

- Status: Accepted
- Date: 2026-10-30

## Context

What a store may do is cached in three places:
- the grants in each instance's entitlements cache (ADR-0045 §4, 30 s);
- the storefront's page caches;
- the edge (ADR-0018).

Those pages carry the card option, the pixels and, from 7g, the ads.
ADR-0047 §4 left one gap open: a plan change was not an event. The
first plan changes that happen without anyone acting are time-based:
- a trial ends;
- a cancelled period ends.

Both move the store to the free plan. Until now nothing recorded that
move: grants were right because `GrantsTx` checks the time, but
`subscriptions` still said `trialing`, and cached pages still showed the
old plan.

## Decision

1. **`billing.subscription.changed`** is published in the transaction
   that writes any lifecycle step (`saveTx`).
   - Its payload is the new status, never a plan name.
   - Every later step (payments, dunning, suspension, the console) writes
     through the same `saveTx`.
2. **Every role subscribes to it:**
   - `entitlements.invalidate_grants` publishes the store on Redis
     (`entitlements:invalidate`), and every instance's
     `entitlements.Module.Listen` drops that store's cached grants;
   - `storefront.invalidate_pages` and `storefront.purge_edge` refresh
     the store's pages, as a theme change does.
   - The pattern is shipping's and the storefront's: a lost message costs
     at most the cache's TTL.
3. **Periods end by job.**
   - `billing.end_periods` runs hourly and at worker start. It lists,
     through `app.periods_ended(now)` (`SECURITY DEFINER`, store IDs
     only), the stores whose trial or cancelled period ended.
   - For each, `billing.end_period` applies the domain's `EndPeriod`
     (to the free plan, keeping `trial_used`) and publishes the event.
   - A store that paid or renewed in the meantime is left alone, and a
     retried job does nothing.

## Consequences

- **The gap in ADR-0047 §4 is closed.** After any plan change, every
  cache follows within seconds.
- **Within the hour.** A trial stops within the hour after it ends.
  Grants already follow the clock, so entitlements are right to the
  second; the hour bounds only how long pages show the old plan.
- **7d-1b and the console** need no wiring of their own for caches: they
  write through `saveTx`.
