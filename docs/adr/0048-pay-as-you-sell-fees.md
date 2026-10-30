# ADR-0048: Pay-as-you-sell fees

- Status: Accepted
- Date: 2026-10-30

## Context

The pay-as-you-sell plan has no subscription price. The store pays a fee
on each order it **delivers**, invoiced monthly (P7 spec). The owner
approved (2026-10-30):

- **The fee's base** is the order's goods after their discount: no
  shipping, no cash-on-delivery fee.
- **A return** before the month's invoice takes the fee back. A return
  after it is credited on the next invoice.

Orders publish `orders.order.status_changed` on every move. Billing may
not import the orders module: orders import entitlements, which import
billing (ADR-0046 §6).

## Decision

1. **The rate is data:** `plan_fees (plan_id, currency, rate_bps,
   min_minor, max_minor)`.
   - A plan without a row charges no fee. The seed charges only the
     pay-as-you-sell plan, 2.5% as a placeholder.
   - The fee is the rate rounded half up, within the bounds, and never
     more than its base.
2. **One fee row per delivered order:** `sell_fees`, a tenant table.
   - The row is written once (`ON CONFLICT DO NOTHING`).
   - It snapshots the plan in effect at the delivery, the rate, the base
     and the fee. A later price change does not touch fees already
     earned.
   - Its period is the delivery's month in UTC, as quotas count.
3. **The event is followed by the order's current status,** not by the
   event's `to`, because deliveries can repeat and arrive out of order
   (ADR-0015).
   - Billing's `OrderMoved` reads the order in the store's transaction.
   - A delivered order gets its fee.
   - A returned order's fee moves `due → cancelled` or
     `invoiced → credit_due`; `credit_due` is credited by 7c-2.
   - Anything else does nothing.
4. **No import of orders.**
   - Billing declares the reader it needs: `billing.Orders`, which returns
     status, base and when.
   - `cmd/platform` adapts `orders.Fulfilment.GetTx` to it, and builds
     the event handler from `orders.OrderStatusChanged`.
   - The handler is subscribed as `billing.sell_fees` and set in the
     worker.

## Consequences

- **A fee is earned when the cash is.** Cash on delivery is the norm, so
  delivery is when the store has been paid. Cancelled and failed
  deliveries never cost anything.
- **Fees are kept per row, not as counters.** An invoice (7c-2) sums a
  month's `due` and `credit_due` rows with `sell_fees_store_period`, and
  every line can be traced to its order.
- **The fee is the plan in effect at delivery.** A store that left the
  pay-as-you-sell plan mid-month still pays for what it delivered while
  on it.
