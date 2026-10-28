# ADR-0040: Order confirmation by message

- Status: Accepted
- Date: 2026-10-28

## Context

Cash-on-delivery orders are often fake, or refused at the door. A
refused parcel costs the merchant shipping both ways. P6 asks for:
- a confirmation message with Confirm and Cancel when a cash-on-delivery
  order is placed;
- the customer's reply moving the order;
- an automatic cancellation after N hours without a reply;
- the merchant being told.

When the store's quota is used up, no message is sent, the merchant is
warned, and the order is unaffected (P6 acceptance). The owner approved
(2026-10-28):
- confirmation for cash-on-delivery orders only, because card orders
  are confirmed by the gateway (ADR-0029);
- 24 hours by default, 1 to 72 chosen per store;
- notices in the dashboard and by email.

Messages go out through the messaging module (ADR-0039).

## Decision

1. **Where it lives.** The confirmation is part of the messaging module:
   it sends the message and receives the reply.
   - It reads and moves orders only through `orders.Fulfilment`:
     `Get`, and `TransitionSystemTx` in its own transaction.
   - It subscribes to `orders.order.placed` as `messaging.confirm_orders`
     (the worker role).
   - It learns the store's name, locale and primary host through a
     `StoreSource`, and staff emails through `identity.StaffEmails`.
     `cmd/platform` adapts both.
2. **On `orders.order.placed`:** the order must be cash on delivery and
   still pending, and the store must confirm (`messaging_settings`,
   default on). Then one transaction:
   - opens the order's confirmation (`order_confirmations`, one per
     order, so a redelivered event does nothing);
   - queues the message. That is the quota unit, the log entry and the
     send job (ADR-0039). The message carries the SMS link
     `https://<primary host>/<locale>/o/<token>`; the token has 128 bits,
     and only its SHA-256 is stored;
   - schedules `messaging.auto_cancel_order` at `placed + N hours`;
   - warns the merchant when this message crossed 80% or 100% of the
     quota.

   Past the quota, nothing is sent and no deadline is scheduled. The
   confirmation is `skipped`, the merchant gets one `quota_reached` notice
   for the month, and the order stays pending for the merchant to
   confirm by hand. With no channel configured, the confirmation is
   `unreached`.
3. **An answer** is a WhatsApp button tap now; the SMS link page follows
   in 6c-2. It settles a pending confirmation once:
   - the order moves to confirmed or cancelled through
     `TransitionSystemTx`, with the actor `customer:whatsapp` (or
     `customer:sms`), in the same transaction;
   - if the merchant moved the order first, the state machine refuses
     the move and the confirmation is `stale`; nothing changes;
   - later answers change nothing.
4. **The deadline** cancels a still-pending order (`system:auto_cancel`;
   orders gives the stock back), but only if its message reached the
   channel: sent, delivered or read.
   - A message that never got out is `unreached`. Such an order is left
     to the merchant and is not cancelled, because a customer who was
     never asked must not lose their order.
   - An order the merchant already moved is `stale`.
5. **Notices** (`merchant_notifications`):
   - kinds: `order_confirmed`, `order_cancelled`, `order_auto_cancelled`,
     `confirmation_unreached`, `quota_nearly`, `quota_reached`;
   - each is created once, per order or per month and threshold;
   - they are kept 90 days and listed in the dashboard with an unread
     count;
   - only the quota warnings are also emailed (`messaging.email_notice`)
     to the owner and managers, so orders' notices do not flood inboxes.
6. **API** (tag `messaging`):
   - `getMessagingSettings` and `getOrderConfirmation` need
     `messaging.read`; `updateMessagingSettings` needs
     `messaging.manage`.
   - `listNotifications` and `markNotificationsRead` need
     `messaging.read`.

## Consequences

- The order's timeline shows who settled it: the customer by WhatsApp
  or SMS, the deadline, or the merchant.
- The merchant can see each order's confirmation and the notices. The
  screens come with the dashboard work (6g).
- A store over its quota keeps taking orders and confirms them by hand,
  as before P6.
- A changed auto-cancel time applies to orders placed afterwards; a
  scheduled deadline keeps its time.
- Customers' typed replies, the SMS link page, the "confirm on WhatsApp"
  button on the thank-you page and acknowledgements to the customer
  come in 6c-2.
