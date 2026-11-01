# ADR-0065: Risk bands from every store's delivery outcomes

- Status: Accepted
- Date: 2026-11-01

## Context

Cash on delivery is how most of our merchants sell. Its cost is the
customer who orders and does not take the parcel: the merchant pays for
the shipping both ways and gets nothing. One store sees too few of a
customer's orders to know them. The platform sees many stores.

P8 asks for:

- a per-phone score from platform-wide delivery outcomes (delivered,
  returned, refused, no-answer), weighted towards recent outcomes;
- merchants see only a band (low, medium, high), plus their own history;
- another store's order details are never exposed;
- deterministic scoring, unit-tested with fixtures;
- a privacy test proving the merchant API never returns another store's
  data.

The owner approved these choices on 2026-11-01:

- a fourth band, `new`, for too little history;
- `high` needs failures from at least two stores;
- a half-life of 90 days, and retention of 24 months.

Constraints:

- Modules never query other modules' tables (CLAUDE.md rule 2). Every
  tenant table is behind RLS, and FORCE RLS binds the owner too
  (ADR-0021).
- Phone numbers are personal data, and logs never hold them in full.
- **Carriers give no refusal reason.** Bosta's states 46/47 are "failed
  attempt", whatever happened. Only people know why: the merchant who
  called the customer, or the courier who knocked.

## Decision

1. **Orders record why an order did not reach its customer** (migration
   00072, expand only).
   - `transitionOrder` and `bulkTransitionOrders` take an optional
     `reason` (`refused`, `no_answer`). Only moves to cancelled,
     failed_delivery and returned may carry one.
   - Messaging's confirmation deadline cancels with `unconfirmed`,
     through the new `Fulfilment.TransitionSystemReasonTx`.
   - The order keeps its latest move's reason, so a failed delivery that
     is delivered on the next attempt loses it.
2. **What an order teaches** (`domain.OutcomeOf`) depends on its current
   status and reason:

   | Status | Reason | Outcome |
   |---|---|---|
   | delivered | any | delivered |
   | returned | refused | refused |
   | returned | other | returned |
   | failed_delivery | refused | refused |
   | failed_delivery | other | no_answer (a failed attempt is nearly always an unanswered phone) |
   | cancelled | refused, no_answer, unconfirmed | that outcome |
   | cancelled | none | nothing (the merchant's or customer's own choice) |
   | any other | any | nothing yet |

3. **`risk_orders`: one row per order** (migration 00073, tenant table
   under RLS).
   - Each row holds the phone key, when the order was placed, and its
     outcome with its time.
   - **The phone key** is HMAC-SHA256 of the E.164 number, keyed by the
     new `RISK_PHONE_KEY` secret (api, storefront, worker). The same
     customer has the same key in every store. The table holds no
     number, and without the key nobody can rebuild the numbers by
     trying them all.
   - **Learning.** The module subscribes to `orders.order.placed` and
     `orders.order.status_changed` as `risk.learn`. Each event re-reads
     the order through `orders.Outcomes` and upserts its row. That makes
     it idempotent and indifferent to the order events arrive in, as
     ADR-0015 asks. A row that would not change is not written.
   - **Backfill.** A store's first learnt order inserts its
     `risk_backfills` mark and queues `risk.backfill`, in the same
     transaction. The job pages through the store's past orders
     (`Outcomes.Before`, 100 per transaction).
   - **Purge.** A daily `risk.purge` deletes rows placed more than 760
     days ago, in batches of 5,000, through `app.risk_purge`. The
     function sets the cut-off itself, so a caller cannot delete younger
     rows.
   - **Not partitioned.** Partitions are made from the current month
     forward, but the backfill writes rows placed long ago.
4. **The only read across stores is `app.risk_signals(keys)`.**
   - It is a SECURITY DEFINER function, reading through an owner-only
     SELECT policy, the pattern of `app.billable_stores`.
   - For each of at most 100 keys, it returns the latest 200 outcomes
     from the last 730 days. Each outcome comes with an opaque store
     number, the same for one store's outcomes of that phone. It never
     returns a store ID, an order ID, or a count of open orders.
   - It reads `risk_orders_phone_outcome` (phone_key, outcome_at DESC,
     partial). The plan is tested.
5. **The score** (`domain.Assess`) is deterministic: integer arithmetic,
   independent of input order, from the time given.
   - **Recency.** Each outcome weighs 2^(−age/90 days), in millionths,
     by whole days. Outcomes 730 days old or more count for nothing.
   - **Failure weights.** Returned and refused weigh 1.0, no_answer
     0.6, unconfirmed 0.3. A delivery is a success (1.0).
   - **No single store can sink a phone.** Only each store's 3 most
     recent outcomes count. `high` needs failures from at least two
     stores; otherwise the phone is at most `medium`.
   - **Prior.** A phone starts as 1 success and 0.25 of a failure, a
     failure share of 20%:
     `score = (bad + 0.25) / (good + bad + 1.25)`, in thousandths.
   - **Bands:**

     | Band | Rule |
     |---|---|
     | new | evidence below 0.5 of a fresh outcome |
     | low | score < 300 |
     | medium | 300 ≤ score < 550, or high from one store only |
     | high | score ≥ 550, with failures from ≥ 2 stores |

   - The fixtures in `internal/modules/risk/internal/domain/testdata/assess.json`
     pin the band, score and evidence of 15 cases. Changing the model
     means changing them, in an ADR.
6. **What merchants see** (tag `risk`, permission `orders.read`):
   - `getOrderRisk`: the band of an order's customer, and the phone's
     history in this store only: orders, delivered, returned, refused,
     no_answer, unconfirmed, the last outcome's time. An order not
     learnt yet is learnt on the spot.
   - `listRiskBands`: the bands of up to 100 of the store's orders (a
     page of the list), in three queries whatever the count. Unknown
     orders and other stores' orders are left out.
   - **There is no lookup by phone number.** A band is only ever asked
     for through one of the store's own orders, so the endpoint cannot
     test arbitrary numbers.
   - **Dashboard:** a "customer rating" card on the order page; a
     medium or high pill in the order list; and a reason prompt when
     moving an order to cancelled, failed_delivery or returned.

### Privacy notes

- **Purpose.** Lowering failed cash-on-delivery deliveries, the cost
  every merchant shares. The data is the outcome of each order (its
  status and reason), keyed by a pseudonym of the customer's phone.
- **Minimisation.**
  - No number, name or address leaves the orders module for the risk
    tables.
  - Across stores, only outcomes and their times travel, never which
    store or which order.
  - Merchants get a band, not a score or counts from elsewhere.
- **Isolation.**
  - `risk_orders` is under RLS like every tenant table.
  - The cross-store read is one SECURITY DEFINER function that returns
    no identifiers.
  - `TestPrivacy` (httpapi) proves the merchant API's answers hold none
    of another store's order IDs, store ID, the phone, or counts. The
    cross-tenant tests prove 404 for another store's orders.
- **Retention.** Rows go 760 days after their order was placed. A
  deleted store's rows go with it (`ON DELETE CASCADE`), so its outcomes
  stop counting for every other store at once.
- **Erasure.** A customer's request to be forgotten removes the rows of
  their phone key in every store. That is a support procedure today
  (operator SQL as the table owner), not an API.
- **The key.**
  - `RISK_PHONE_KEY` lives in the secret manager, the same value for
    api, storefront and worker.
  - **It is never rotated.** A new key forgets every phone's history.
    A leaked key lets its holder test numbers against the table, so it
    is treated like the database password.
- **Terms.** Merchants' terms of service and the shoppers' privacy policy
  must say that delivery outcomes are shared, in this form, to rate
  orders. This must be in place before go-live (docs/go-live.md). It is a
  product and legal step, not code.

## Consequences

- **Positive:**
  - Merchants see, at the moment of confirming, whether a customer
    usually takes their orders, from evidence no single store has.
  - A new store gets the platform's knowledge on day one.
  - The model is small, deterministic, and pinned by fixtures.
- **Negative:**
  - **Reasons depend on merchants.** Without a reason, a failed
    delivery counts as no_answer, and a plain cancellation counts for
    nothing.
  - **Lag.** A band lags its order's events by the time the worker takes
    to deliver them (seconds). The order list shows no band for an order
    placed a moment ago.
  - **A phone shared by a household or an office** carries everyone's
    outcomes. The prior and the two-store rule soften this; the merchant
    sees their own history beside the band.
  - **No erasure endpoint yet.**
  - **`risk_orders` grows with every order** (about 100 bytes plus two
    indexes per row) until the purge. At 1.5 M orders a month that is
    about 36 M rows.
