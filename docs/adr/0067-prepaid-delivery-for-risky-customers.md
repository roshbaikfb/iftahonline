# ADR-0067: Prepaid delivery for risky customers

- Status: Accepted; amended by ADR-0070 (the link to pay goes to the phone)
- Date: 2026-11-01

## Context

P8 asks for "prepaid shipping for high risk". When a cash-on-delivery
customer refuses the parcel, the merchant loses the delivery both ways.
If the customer has paid for the delivery upfront, that loss is covered.
The customer also gains a reason to take the parcel.

The owner approved (2026-11-01) a partial payment rather than a card-only
order: the shopper pays the shipping and the COD fee by card, and the rest
on delivery.

Constraints:

- **Card payments exist** (ADR-0029, ADR-0030). Each store has its own
  gateway. The shopper pays on the gateway's page, reached through the API
  host. A payment changes state only on the gateway's word, and unpaid
  card orders are cancelled after an hour.
- **No breaking changes in /v1.** `payment_status` and `risk_action` are
  response enums, so neither can gain a value.
- **The courier collects** `shipments.cod_minor`. The COD reconciliation
  (ADR-0028) compares settlements with that amount.

## Decision

1. **A third rule, `prepay_from`** (migration 00077, off by default).
   - The rules apply in this order: hold, then prepay, then verify.
   - In `updateRiskSettings` the field is optional; when omitted, it is
     off. `getRiskSettings` always returns it. *Amended 2026-11-01 (P8
     review, the owner's decision):* omitted keeps the current value, so a
     client that does not know the field cannot turn it off.
2. **The deposit** (migration 00076, expand only):
   - **Columns.** `orders.deposit_minor` is the shipping plus the COD fee.
     `orders.deposit_status` is `awaiting`, `paid`,
     `partially_refunded` or `refunded`, and null without a deposit.
   - **The order stays cash on delivery.** `payment_method = cod` and
     `payment_status = unpaid` until the courier collects.
   - **Recording.** The order records `risk_action = prepay`. The API
     shows the deposit fields instead, because `risk_action` cannot gain a
     value.
   - **When it is taken.** Only when the store can take cards now: a
     gateway connected and the plan's `card_payments`, through the new
     `orders.CardCheck` port, which `payments.NewCardCheck` fills.
   - **When it is not.** A store without cards, or an order with nothing
     to prepay (free delivery and no COD fee), holds the order instead
     (ADR-0066). The rules asked for more than a plain order.
3. **Payments treat a deposit like a card order's total.**
   - `Order.OnlineDue()` names what is due online: a card order's total
     while awaiting, or a cash order's deposit while awaiting. Checkout,
     the expiry watch and the unpaid cancellation all use it.
   - The gateway's page charges the deposit, described as
     "Delivery of order …".
   - `SetPaymentStatusTx` moves `deposit_status` for a cash order with a
     deposit (audit `order.deposit_*`). `orders.order.payment_changed`
     carries `deposit: true`.
   - **A paid deposit confirms the order**, as a card payment does. An
     unpaid one is cancelled within the hour, and its stock is released.
   - Refunds work as for card orders, and the dashboard shows the
     deposit's payments.
4. **The courier collects the rest.** `Order.OnDelivery()` is the total
   less a paid deposit, or zero for a card order. `cmd/platform`'s
   shipping adapter passes it as the shipment's COD, so the COD
   reconciliation expects the right amount.
5. **Confirmation.** Messaging sends no confirmation message for a
   prepaid order: paying confirms it.
6. **Storefront.**
   - **Redirect.** `web.Orders.PlaceOrder` now returns `Placed{Ref,
     PayOnline}`, and a prepaid order goes on to pay (`?pay=1`), like a
     card order.
   - **The thank-you page** says what is paid now and what on delivery:
     "لإتمام طلبك، ادفع مصاريف التوصيل مقدمًا بالبطاقة: X. وتدفع الباقي
     (Y) عند الاستلام". Once paid it shows a "paid" chip; once cancelled,
     the expiry message.
   - **Neutral wording.** The shopper is never told they are risky.

## Consequences

- **Positive:**
  - A refusal no longer costs the merchant the delivery.
  - Paying the deposit is also the confirmation, so no message is needed.
  - The card checkout's guarantees apply unchanged: verified payments,
    replays changing state once, and expiry.
- **Negative:**
  - **Some shoppers will not pay** and their orders are cancelled. That
    is the point, but merchants should set the rule from `high`.
  - **Each prepaid order costs a gateway fee** on a small amount.
  - **The deposit is not refunded automatically** if the merchant cancels
    later. Refunding is a merchant action, as for card orders.
  - **`risk_action` hides the prepay value** in the API for compatibility.
    Clients read `deposit_minor` instead.
