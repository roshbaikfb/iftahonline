# Module: entitlements

The entitlements module is **the one place that answers what a store may
do**:
- whether it has a feature;
- how many of a thing it may hold;
- how much of a monthly quota it has used.

Grants come from the store's plan, which [billing](billing.md) owns. No
other module looks at plans.

Design: [ADR-0038](../adr/0038-entitlements-platform-accounts-mailer.md)
(quotas), [ADR-0045](../adr/0045-plans-as-data-and-one-entitlements-service.md)
(plans, the one service) and [ADR-0046](../adr/0046-enforcing-plan-limits.md)
(how modules enforce them).

## Public API (`internal/modules/entitlements/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Entitlements()` | Wiring; the interface other modules use |
| `FromBilling(billing.GrantSource)` | The production source of grants. `Fixed{...}` grants every store the same (tests, demo tools) |
| `Module.Invalidate(store)` | Drops this instance's cached grants of a store after its plan changed |
| `Unrestricted()` | Allows everything and counts nothing, without a database: the default of a module built without entitlements (its own tests) |
| `Enforced(key)` | Whether the code checks a catalog key; the console marks display-only keys with it (ADR-0056) |
| `Entitlements.Allowed(ctx, f)` | Whether the store has the flag feature `f` |
| `Entitlements.Require(ctx, f)` | `Allowed` as an error for a merchant's request: a 403 with code `plan_limit` (`NotInPlan`) |
| `Entitlements.AdmitTx(ctx, tx, f, n, count)` | Admits `n` more of the counted feature `f` in the caller's transaction. `count` (the module's own count) runs under an advisory lock on (store, `f`) held until the transaction ends. Past the grant it returns a 403 with code `plan_limit` (`errors.Is ErrLimitReached`) |
| `Entitlements.CountTx(ctx, tx, f, n)` | Counts `n` units of a monthly feature and never refuses (a soft limit: orders). Publishes `UsageCrossed` (`entitlements.usage.crossed`) at 80% and 100% |
| `Entitlements.ConsumeTx(ctx, tx, f, n)` | Counts `n` units of the monthly feature `f` in the caller's tenant transaction. Past the limit it counts nothing and returns `ErrQuotaExceeded` with the month's usage |
| `Entitlements.ReleaseTx(ctx, tx, f, period, n)` | Gives back units counted in `period` (the consumption's `Usage.Period`), never below zero |
| `Entitlements.Usage(ctx, f)` | The store's usage this month |
| `Usage` | `Feature`, `Period` (the month's first day, UTC), `Used`, `Limit`; `Remaining()`, `Level()`, `CrossedBy(n)`, `Unlimited()` |
| `Grant`, `Grants` | `Value` or `Unlimited`; a feature not listed is not granted |
| `Nearly` (80%), `Reached` (100%) | Warning thresholds. Warn the merchant when `CrossedBy(n)` returns one of them, so each threshold is warned once a month. An unlimited feature never warns |
| `PlanLimitCode` | `plan_limit`: the problem code of every hard-limit refusal |
| `ErrQuotaExceeded`, `ErrLimitReached`, `ErrInvalid` | Over a monthly quota; at a counted limit; a programming error (a feature unknown or of the wrong kind for the call, units outside 1..1000, a period that is not a month's first day) |

## Features

| Kind | Features | Call |
|---|---|---|
| flag | `card_payments`, `coupons`, `pixels`, `feeds`, `ad_free` | `Allowed` |
| count | `products`, `staff`, `custom_domains` | `AdmitTx` |
| monthly | `orders_per_month` (soft), `messages` (hard) | `CountTx`, or `ConsumeTx`/`ReleaseTx`; `Usage` |

- **Listed in code.** These are the features the code enforces
  (`domain.Features`). `TestFeaturesMatchTheCatalog` holds them to the
  `features` table.
- **Display-only keys.** The console may add other keys to plans. They
  are shown, but left out here, and enforced by nothing until a module
  asks for them.
- **Who enforces what:**

  | Feature | Enforced by | How |
  |---|---|---|
  | `products` | catalog, every creation (API, imports, starter samples) | `AdmitTx`: products not deleted |
  | `staff` | identity, invitations | `AdmitTx`: members (the owner included) and open invitations |
  | `custom_domains` | tenancy, `AddCustomDomain` | `AdmitTx`: custom domains |
  | `orders_per_month` | orders, every placement | `CountTx`: never refused; messaging warns the merchant |
  | `messages` | messaging | `ConsumeTx` (ADR-0038) |

  | `card_payments` | payments: `connectPaymentGateway`; `CardEnabled`, `StartCheckout` | `Require` to connect; `Allowed` at checkout (ADR-0047) |
  | `coupons` | promotions: `createCoupon`; `Preview` | `Require` to create; a code is `not_found` for the shopper without it |
  | `pixels` | marketing: `savePixel`; `Browser`, `Track` | `Require` to save; pages load none and no conversion is sent without it |
  | `feeds` | marketing: `renewProductFeedKey`; `FeedAllowed` | `Require` to turn on; the feed URL opens nothing without it |

  `ad_free` is enforced from 7g.

## The cache

Grants are cached in memory per instance (`platform/storecache`):

| Setting | Value |
|---|---|
| Stores kept | 10,000 |
| Expiry | 30 s |
| Invalidation | by generation, through `Module.Invalidate` |

- A cached check costs no query and no transaction.
- On a miss, `AdmitTx`, `CountTx`, `ConsumeTx` and `ReleaseTx` read in
  the **caller's** transaction, so they never need a second connection.
  `Allowed` and `Usage` open one tenant transaction.

| Benchmark (`go test -bench AllowedCached ./internal/modules/entitlements/internal/app/`) | Result |
|---|---|
| `Allowed`, cached, 28 goroutines (28-core dev machine) | ~205 ns/op, 0 allocs |

### Across instances (ADR-0050)

`billing.subscription.changed` reaches `InvalidateHandler` (subscriber
`entitlements.invalidate_grants`) on every role's bus. The handler
publishes the store on Redis (`entitlements:invalidate`), and each
instance's `Module.Listen` drops its cached grants. The api, storefront
and worker roles all listen. A lost message costs at most the 30 s TTL.

## How a consumption works

`usage_counters` has one row per store, feature and month. `ConsumeTx`
runs one conditional upsert:

```sql
INSERT … ON CONFLICT (store_id, feature, period)
DO UPDATE SET used = used + n WHERE used + n <= limit RETURNING used
```

- **Concurrent senders of one store** wait on the row's lock, so the last
  unit is spent once (tested with 20 concurrent senders and a limit of 5).
- **It is the caller's transaction.** When a send is rolled back (for
  example, its job could not be enqueued), its units never count.
- **No row returned means over the limit.** Nothing changes, and
  `ConsumeTx` reads the month's count to return it.
- **An unlimited feature** is still counted, with a limit of
  `math.MaxInt64`, so usage can be shown.

Months are counted in UTC for every store. In Cairo or Riyadh time, a new
month starts two or three hours later than midnight local time.

## Tables

| Table | Notes |
|---|---|
| `usage_counters` | Tenant table under RLS. Primary key `(store_id, feature, period)`, which also covers the FK. `app_user` cannot delete; rows go with their store. One row per store, feature and month, kept. |

## Rules worth knowing

- **The order is unaffected by a quota.** Metered modules treat
  `ErrQuotaExceeded` as "do not send, warn the merchant", never as a
  failure of the order (P6 acceptance).
- **Never ask for a plan.** Ask for a feature. `internal/tools/noplannames`
  fails the build on a plan-name comparison outside billing.
- **Testing:**
  - `internal/app` runs on real PostgreSQL. It covers quotas and their
    warnings, a new month, rollback, the race for the last unit, two
    stores counting apart, zero and unlimited limits, releases, flags,
    counts, the cache (hits, TTL, invalidation), reads on a one-connection
    pool, and invalid requests.
  - The module test runs against the real billing module and the seeded
    plans.
