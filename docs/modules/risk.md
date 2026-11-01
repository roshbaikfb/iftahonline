# Module: risk

The risk module tells merchants how likely an order's customer is to take
it. It learns from every store's delivered, returned, refused and
unanswered orders, keyed by a pseudonym of the customer's phone. Merchants
see only a **band** (new, low, medium, high) and their own store's history
of the phone, never another store's orders.

Design, and the privacy notes:
[ADR-0065](../adr/0065-risk-bands-from-delivery-outcomes.md).

## Public API (`internal/modules/risk/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount` | Wiring, and the merchant HTTP API (behind identity's membership middleware) |
| `Band`, `Band…` constants | What merchants see of a phone's score |
| `Module.LearnHandler`, `LearnSubscriber` (`risk.learn`) | Learns from an order on `orders.order.placed` and `orders.order.status_changed` (worker role) |
| `Module.RegisterWorkers`, `Module.PeriodicJobs` | `risk.backfill` (a store's past orders, once) and the daily `risk.purge` |
| `Checker`, `NewChecker(CheckerDeps)`, `Verdict`, `Action` (`Hold`, `Verify`) | The store's rules at placement ([ADR-0066](../adr/0066-risk-rules-hold-and-verify.md)); `cmd/platform` adapts it to `orders.RiskCheck` |

Deps:
- `orders.Outcomes`: `Get`, and `Before` for the backfill;
- `RISK_PHONE_KEY` (`config.Secrets.RiskPhoneKey`);
- a River inserter.

## Events consumed

| Event | Subscriber | Effect |
|---|---|---|
| `orders.order.placed` | `risk.learn` | The order's row: phone key and placed time; queues the store's backfill the first time |
| `orders.order.status_changed` | `risk.learn` | The order's outcome, from its current status and reason (`domain.OutcomeOf`) |

Both re-read the order, so they are idempotent and the order events
arrive in does not matter.

## Tables

| Table | Holds |
|---|---|
| `risk_orders` | One row per order: `phone_key` (HMAC-SHA256, 32 bytes), `placed_at`, `outcome`, `outcome_at`. Tenant table under RLS. Indexes: PK `(store_id, order_id)`, `(store_id, phone_key)`, partial `(phone_key, outcome_at DESC)`, `(placed_at)` |
| `risk_backfills` | The stores whose past was queued for learning |
| `risk_settings` | The store's rules: `hold_from`, `verify_from` (`off`, `medium`, `high`); no row is both off |

Functions (SECURITY DEFINER, owner-only policies):
- `app.risk_signals(keys)`: up to 100 keys' latest 200 outcomes of the
  last 730 days, with an opaque store number;
- `app.risk_purge(limit)`: deletes rows placed more than 760 days ago.

## Scoring (`domain.Assess`)

- **Recency.** An outcome weighs 2^(−age/90 days).
- **Failures.** Returned and refused weigh 1.0, no_answer 0.6,
  unconfirmed 0.3.
- **Per store.** Only each store's 3 most recent outcomes count.
- **Prior.** A phone starts as 1 success and 0.25 of a failure.
- **Bands.** Too little evidence is `new`. Then the score: below 300 is
  `low`, below 550 is `medium`, and 550 or more is `high`, but only with
  failures from two or more stores.

The fixtures in `internal/domain/testdata/assess.json` pin 15 cases
exactly.

## HTTP API (tag `risk`)

| Operation | Permission | Notes |
|---|---|---|
| `getOrderRisk` | orders.read | The band, and the phone's history in this store; learns the order first if needed |
| `listRiskBands` (≤ 100 order IDs) | orders.read | Bands of a page of orders, three queries whatever the count; unknown and other stores' orders are left out |
| `getRiskSettings` | orders.read | The rules, and `available` (the plan's `risk_rules`) |
| `updateRiskSettings` | orders.manage | Turning a rule on needs `risk_rules` (403 `plan_limit`); audited |

There is no lookup by phone number.

## Dashboard

- **Messages page.** A "حماية الدفع عند الاستلام" card with the two
  rules, disabled, with a link to the plans, when the plan lacks them.
- **Held order.** The order page says why it is held and has "فك الحجز".

- **Order page.** A "تقييم العميل" card shows the band and the store's
  own history.
- **Moves.** Cancelling, a failed delivery and a return ask why:
  "العميل رفض الطلب", "العميل لم يرد", or "سبب آخر".
- **Order list.** It shows a pill for medium and high customers only,
  read with one `listRiskBands` per page.

## Rules worth knowing

- **Reasons.** A failed delivery without a reason counts as `no_answer`.
  A cancellation without one counts for nothing.
- **The key.** `RISK_PHONE_KEY` is never rotated (see docs/go-live.md).
- **Rules** (ADR-0066). A storefront cash order whose customer's band
  reaches `hold_from` is held: no confirmation message, and it cannot be
  confirmed until released. One that reaches `verify_from` is asked to
  confirm even if the store does not confirm by message, but the
  customer's yes leaves it pending for a call. New customers are never
  held or verified. Prepaid shipping comes in 8d.
