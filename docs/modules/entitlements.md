# Module: entitlements

The entitlements module meters **what each store may use per month**.
Until plans arrive (P7), every store gets the same limits from the
environment. P7 changes where the limits come from, not the interface.
Design: [ADR-0038](../adr/0038-entitlements-platform-accounts-mailer.md).

## Public API (`internal/modules/entitlements/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Entitlements()` | Wiring; the interface metered modules use |
| `Entitlements.ConsumeTx(ctx, tx, feature, n)` | Counts `n` units this month in the caller's tenant transaction. Past the limit it counts nothing and returns `ErrQuotaExceeded` with the month's usage |
| `Entitlements.ReleaseTx(ctx, tx, feature, period, n)` | Gives back units counted in `period` (the consumption's `Usage.Period`), never below zero |
| `Entitlements.Usage(ctx, feature)` | The store's usage this month |
| `Usage` | `Feature`, `Period` (the month's first day, UTC), `Used`, `Limit`; `Remaining()`, `Level()`, `CrossedBy(n)` |
| `Messages` | The metered feature for customer messages; a WhatsApp message or an SMS is one unit |
| `Nearly` (80%), `Reached` (100%) | Warning thresholds. Warn the merchant when `CrossedBy(n)` returns one of them, so each threshold is warned once a month |
| `ErrQuotaExceeded`, `ErrInvalid` | Over the limit; a programming error (unknown feature, units outside 1..1000, a period that is not a month's first day) |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `ENTITLEMENTS_MESSAGES_PER_MONTH` | 500 | Customer messages per store per calendar month (UTC); 0 sends none |

Months are counted in UTC for every store. In Cairo or Riyadh time, a new
month starts two or three hours later than midnight local time.

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

## Tables

| Table | Notes |
|---|---|
| `usage_counters` | Tenant table under RLS. Primary key `(store_id, feature, period)`, which also covers the FK. `app_user` cannot delete; rows go with their store. One row per store, feature and month, kept. |

## Rules worth knowing

- **The order is unaffected by a quota.** Metered modules treat
  `ErrQuotaExceeded` as "do not send, warn the merchant", never as a
  failure of the order (P6 acceptance).
- **No HTTP API yet.** The dashboard shows usage once the messaging
  screens arrive (P6 6g); plans and billing come in P7.
- **Testing:** `internal/app` runs on real PostgreSQL. It covers the
  limit and its warnings, a new month, rollback, the race for the last
  unit, two stores counting apart, a zero limit, releases, and invalid
  requests.
