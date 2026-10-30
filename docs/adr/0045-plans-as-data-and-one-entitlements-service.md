# ADR-0045: Plans as data, and one entitlements service

- Status: Accepted
- Date: 2026-10-30

## Context

P7 brings plans: free (with ads on the store), pay as you sell,
merchant, pro, brand and agency. Each plan grants:

- features that are on or off (card payments, coupons, pixels, feeds,
  no ads);
- counts a store may hold (products, team members, custom domains);
- monthly quotas (orders, messages).

The owner approved the P7 plan (2026-10-30) with these rules:

- **Plans are data.** Prices, limits and features change from the
  console, not in code.
- **The console may add features of its own.** It does so to show them
  on plans.
- **One service answers every limit and feature check.**
- **No plan-name checks outside billing.** This is an acceptance criterion.
- **Every new store starts on the free plan,** with no subscription and
  no card. It may try a paid plan once, for 14 days, without a card.
  When the trial ends unpaid, the store goes back to free; it is not
  suspended.
- **Existing stores get a 14-day trial of pro** from the deploy.

ADR-0038 built the `entitlements` module with an interim limit from the
environment, and said P7 would change where limits come from but not
the interface.

## Decision

1. **A `billing` module owns plans.** Its tables (migration 00056) are
   platform data, with no `store_id` and no RLS:

   | Table | Holds |
   |---|---|
   | `features` | The catalog of feature keys, each with a kind (`flag`, `count`, `monthly`) |
   | `plans` | One row per plan version (code, kind, names, trial days) |
   | `plan_prices` | One price per currency (EGP, SAR, AED) and cycle (monthly, annual) |
   | `plan_entitlements` | One row per plan and feature. `NULL` is unlimited; a missing row is not granted |

   - `app_user` may only read these tables. The console will write them (7e).
   - A change to a plan is a new **version** of it. A subscription keeps
     the version it bought until it renews.
   - Annual pricing is a separate stored price; no discount is computed
     in code.
   - The seeded prices and limits are placeholders, to be set before
     launch.
2. **`subscriptions` is a tenant table.** It has one row per store, and a
   store without a row is on the free plan. `trial_used` allows one trial
   per store.

   The lifecycle is pure domain code (`billing/internal/domain`):

   | Status | Meaning | Plan applies |
   |---|---|---|
   | `trialing` | A paid plan's trial | Until `trial_ends_at` |
   | `active` | Free, pay as you sell, or a paid period | Yes |
   | `past_due` | A renewal failed | Yes, until `grace_until` (7 days) |
   | `suspended` | Not paid after grace: the store is closed, its data kept | No |
   | `cancelled` | Suspended for 90 days: the data may be deleted | No |

   - When the plan does not apply, the store gets the free plan's grants.
   - A merchant who cancels keeps the plan to the period's end, then
     moves to free.
   - A payment brings a past-due, suspended or cancelled store back.
   - Dunning (7d) drives these steps from jobs. Its scenario (fail,
     retries, grace, suspend, restore) is already a domain test.
3. **`entitlements` is the one place that answers what a store may do.**

   | Call | For |
   |---|---|
   | `Allowed(ctx, flag)` | Flag features |
   | `CheckCount(ctx, count, after)` | Counted features; returns `ErrLimitReached` |
   | `ConsumeTx`, `ReleaseTx`, `Usage` | Monthly quotas, unchanged from ADR-0038 |

   - **Enforced features are listed in code** (`domain.Features`). A test
     holds that list to the `features` catalog.
   - **Display-only keys.** A key the console adds is shown on plans.
     `entitlements` leaves it out, and nothing enforces it until a module
     asks for it. The console will mark which keys are enforced.
   - **Where grants come from.** They come from billing through its
     `api.go` (`GrantSource.GrantsTx`): feature keys and numbers, never a
     plan's code or name.
   - **A cache miss reads in the caller's transaction,** so a pool of one
     connection does not deadlock inside `ConsumeTx`.
4. **Grants are cached per instance.**
   - The cache is `platform/storecache`: bounded (10,000 stores) and
     expiring (30 s), with generation-based invalidation.
   - It was extracted from the shipping module's config cache, its first
     user.
   - A cached check costs no query and no transaction.
   - `Module.Invalidate` drops a store's grants. Events will call it when
     a subscription changes (7d, 7e). Until then, nothing changes a
     subscription at run time except time itself, and time is evaluated
     on every read.
5. **No plan-name checks, enforced by a test.**
   - `internal/tools/noplannames` reads the plan codes from the
     migrations that seed them.
   - Outside `internal/modules/billing`, it fails on:
     - a Go comparison (`==`, `!=`, a `switch` case, `strings.EqualFold`
       and the like) with such a literal;
     - any comparison of a value named for a plan's code or name;
     - the same in the dashboard's TypeScript.
   - A line that compares such a word for another reason ends with
     `noplannames:allow <reason>`.
   - Billing's `api.go` exports no plan codes.
6. **The interim limit is removed.** `ENTITLEMENTS_MESSAGES_PER_MONTH`
   no longer exists; the store's plan gives its message quota.

## Consequences

- **Pricing is data.** Changing a price, a limit or a plan's features
  needs no deploy. Renaming a plan needs no code change.
- **The free plan sends no customer messages** (its seeded `messages`
  is 0). New stores do not get WhatsApp order confirmations until they
  try or buy a plan. Orders are never blocked by it (ADR-0038).
- **Grant changes take effect within 30 s.** Until the invalidation
  events arrive, another instance may apply an old plan's grants for up
  to 30 s after a change.
- **The trial of existing stores starts at the migration.** It is
  14 days from the deploy, not from the store's creation.
- **The migration runs as `app_owner`,** which reads `stores` through its
  `directory_lookup` policy for the backfill. A test re-applies 00056
  over an existing store.
- **What 7b to 7g add:**
  - enforcement in each module (7b);
  - fees and invoices (7c);
  - payment and dunning (7d);
  - the console (7e);
  - the dashboard (7f);
  - ads on free stores (7g).
