# Module: messaging

The messaging module sends **customer messages** on each store's behalf.
Messages go from the platform's own WhatsApp number, with SMS (Twilio)
as the fallback. Every message:
- costs a quota unit (`entitlements`);
- is logged with its delivery status;
- comes back by webhook: statuses and button replies.

Design: [ADR-0038](../adr/0038-entitlements-platform-accounts-mailer.md)
(quotas, the platform account),
[ADR-0039](../adr/0039-messaging-channels-and-log.md) (channels, the log,
whose word counts),
[ADR-0040](../adr/0040-order-confirmation-by-message.md) (order
confirmation),
[ADR-0041](../adr/0041-customer-started-confirmation.md) (customers
confirm on WhatsApp first; thanks).

## Public API (`internal/modules/messaging/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount`, `Module.RegisterWorkers` | Wiring; the merchant HTTP API (behind identity's membership middleware); the send and webhook jobs (the worker role) |
| `Module.Sender()` → `Sender` | `EnqueueTx(ctx, tx, Outgoing)` queues a message in the caller's transaction; `Get` reads the log |
| `Module.PlatformProviders()` | The channels' webhook providers, for `webhooks.Module.RegisterPlatform` |
| `Module.OrderPlacedHandler()`, `ConfirmOrdersSubscriber` | The order confirmation; subscribe it to `orders.EventOrderPlaced` (worker role) |
| `Deps.Orders`, `Deps.Stores`, `Deps.Contacts`, `Deps.Mailer` | The confirmation's collaborators (worker role): `orders.Fulfilment`, the store's name, locale and host, the staff emails (`identity.StaffEmails`) |
| `Module.Fakes()` | The fake channels, for tests and demos |
| `NewConfirmLinks(LinksDeps)` → `ConfirmLinks` | The storefront role's side (no secrets): `LinkConfirmation` and `AnswerLink` by SMS token; `WhatsAppLink` for the thank-you page |
| `Outgoing` | `Kind`, `Locale` (`ar`/`en`), optional `OrderID`, `To` (E.164), `Vars` (customer and store names, order reference, total, confirm URL) |
| `Queued` | The message's ID, its status (`queued` or `skipped_quota`), and the store's quota `Usage` after it |
| `Message`, `Status`, `Reply`, `Answer` | The log entry and the answers |

**Warning the merchant** is the caller's job. Warn when
`Queued.Usage.CrossedBy(1)` is `entitlements.Nearly` or `Reached`, or
when the status is `skipped_quota`.

## Channels

| Name | Adapter | Configured by | Notes |
|---|---|---|---|
| `whatsapp` | WhatsApp Cloud API | `WHATSAPP_PHONE_NUMBER_ID`, `WHATSAPP_ACCESS_TOKEN`, `WHATSAPP_APP_SECRET`, `WHATSAPP_VERIFY_TOKEN` (`WHATSAPP_GRAPH_URL` optional) | Template messages with quick-reply buttons. Webhook: `/v1/webhooks/whatsapp`, with the GET handshake |
| `twilio` | Twilio Programmable Messaging | `TWILIO_ACCOUNT_SID`, `TWILIO_AUTH_TOKEN`, `TWILIO_FROM` (number, sender ID or `MG…` service) | Text messages. Status callback `/v1/webhooks/twilio?m=<ref>`; statuses are read back from Twilio |
| `fake_whatsapp`, `fake_sms` | In memory | `MESSAGING_FAKE_CHANNELS=true` (not in production) | Fill whichever channel is not configured |

`WHATSAPP_DISPLAY_NUMBER` (E.164, public; also read by the storefront
role) turns on the thank-you page's "confirm on WhatsApp" link.

Neither adapter has run against the real service yet: there is no Meta
app and no Twilio account. Both pass the contract suite
(`internal/channel/channeltest`) against in-process stand-ins of the
APIs, written from the providers' documentation.

## The platform's WhatsApp templates

Before WhatsApp can send anything, create these templates once in Meta's
WhatsApp Manager, on the platform's number. They need Meta's approval.

- **Template:** `order_confirmation_v1`.
- **Category:** Utility.
- **Languages:** Arabic (`ar`) and English (`en`).
- **Body variables:**
  - `{{1}}` the customer's name;
  - `{{2}}` the store's name;
  - `{{3}}` the order reference;
  - `{{4}}` the total.
- **Buttons:** two quick replies, in this order:
  - Arabic: «تأكيد الطلب» then «إلغاء الطلب».
  - English: "Confirm order" then "Cancel order".

Body, Arabic:

> أهلاً {{1}}، شكراً لطلبك من {{2}}. رقم الطلب {{3}} والإجمالي {{4}}. هل تؤكد الطلب؟

Body, English:

> Hi {{1}}, thank you for your order from {{2}}. Order {{3}}, total {{4}}. Do you confirm it?

### Merchant notices (ADR-0054)

The platform also tells a store's **owners** about billing on WhatsApp,
from its own number. These are not customer messages:
- they are not counted in the store's quota and not in its message log;
- `Module.Merchant().SendMerchant` sends them at once, from billing's
  notice job, to the owners' verified phones.

Submit these templates to Meta as **Utility**, Arabic and English:

| Template | Body ({{1}}…) |
|---|---|
| `billing_payment_failed_v1` | ar: «{{1}}: تعذّر تحصيل الفاتورة {{2}} بقيمة {{3}}. سنحاول مرة أخرى، وإذا لم تُدفع يُغلق المتجر يوم {{4}} مع الاحتفاظ ببياناته. ادفع أو غيّر البطاقة من صفحة الفوترة.» en: "{{1}}: we could not charge invoice {{2}} ({{3}}). We will try again; if it stays unpaid, the store closes on {{4}}, its data kept. Pay or change the card on the billing page." |
| `billing_store_suspended_v1` | ar: «{{1}}: أُغلق المتجر لفاتورة غير مدفوعة. بياناته محفوظة؛ ادفع الفاتورة ليعود.» en: "{{1}}: the store is closed for an unpaid invoice. Its data is kept; pay the invoice to open it again." |
| `billing_payment_received_v1` | ar: «{{1}}: دُفعت الفاتورة {{2}} بقيمة {{3}}. شكرًا لك.» en: "{{1}}: invoice {{2}} ({{3}}) is paid. Thank you." |

## HTTP API (tag `messaging`)

| Operation | Permission | Notes |
|---|---|---|
| `listMessages` `GET /v1/stores/{store}/messages` | messaging.read | The log, newest first, paged by cursor; `status` filters. `order_id` gives an order's messages, without paging. Numbers are masked |
| `getMessageUsage` `GET …/messaging/usage` | messaging.read | The month's `used`, `limit`, `remaining` and `level` (`ok`, `nearly` at 80%, `reached`) |
| `listMessageTexts` `GET …/messaging/texts` | messaging.read | The SMS text for every kind and locale: the store's own (`custom`), or the default |
| `saveMessageText` `PUT …/messaging/texts/{kind}/{locale}` | messaging.manage | 1 to 400 characters, with only `{{name}}`, `{{store}}`, `{{ref}}`, `{{total}}` and `{{url}}`; an order confirmation needs `{{url}}` (422 otherwise). Audited |
| `resetMessageText` `DELETE …/messaging/texts/{kind}/{locale}` | messaging.manage | Back to the default; returns it. Audited |
| `getMessagingSettings` `GET …/messaging/settings` | messaging.read | `confirm_cod` (default true), `auto_cancel_hours` (default 24) |
| `updateMessagingSettings` `PUT …/messaging/settings` | messaging.manage | Hours 1 to 72; applies to orders placed afterwards. Audited |
| `getOrderConfirmation` `GET …/messaging/confirmations/{order}` | messaging.read | The order's confirmation: `pending`, `confirmed`, `cancelled`, `expired`, `skipped`, `unreached` or `stale`; 404 when it asked for none |
| `listNotifications` `GET …/messaging/notifications` | messaging.read | Notices, newest first, with the unread count; `unread_only` filters |
| `markNotificationsRead` `POST …/messaging/notifications/read` | messaging.read | Up to 100 IDs; returns how many were unread |

Support reads the log (they follow up confirmations); the owner and the
manager also write the texts. WhatsApp always sends the platform's
approved templates, so only the SMS text is the store's.

## Sending

1. **`EnqueueTx` runs in the caller's transaction.** It consumes one
   quota unit, writes the log entry with the recipient and values
   sealed, and inserts `messaging.send_message`.
   - Past the quota, the message is logged as `skipped_quota` and nothing
     is sent.
   - With no channel configured, the message is logged as failed with
     `no_channel`.
2. **The send job tries WhatsApp first.** An SMS uses the store's own
   text for the kind and locale, or the default.
   - If WhatsApp refuses the customer, the job falls back to SMS once,
     as a new job. The fallback costs no second unit.
   - Rejected messages, refused credentials, and transient errors on the
     last of 8 tries all fail the message, and its unit is given back.
3. **WhatsApp may report the customer unreachable later** (a `failed`
   status, code 131026). The message then falls back to SMS the same
   way. Statuses of the earlier attempt are stale from then on.

## Order confirmation (ADR-0040)

1. **On `orders.order.placed`:** a cash-on-delivery order still pending,
   in a store that confirms, gets one transaction. It opens its
   confirmation, queues the message with the SMS link
   (`/<locale>/o/<token>`; only the token's hash is stored), and
   schedules the deadline at `placed + auto_cancel_hours`. Past the
   quota: `skipped`, no deadline, and a `quota_reached` notice.
2. **The customer's tap** moves the order through
   `orders.Fulfilment.TransitionSystemTx` (actor `customer:whatsapp`),
   once. If the merchant moved the order first, the confirmation is
   `stale`.
3. **The SMS link** (`/<locale>/o/<token>` on the store's host) shows the
   order and takes the answer through `ConfirmLinks.AnswerLink` (actor
   `customer:sms`). The token is found by its hash in the host's store
   only. A customer marked unreached may still answer.
4. **The deadline** cancels the order (actor `system:auto_cancel`) if
   its message reached the channel. If not, the confirmation is
   `unreached`: the merchant decides, and is told.
5. **Customers first on WhatsApp** (ADR-0041, with
   `WHATSAPP_DISPLAY_NUMBER`):
   - Each confirmation gets an 8-character code, unique across stores.
   - The thank-you page links to `wa.me/<number>` with «أؤكد طلبي رقم
     <code>».
   - The template waits 10 minutes; if the order was settled by then,
     it is not sent and its unit comes back (`answered_first`).
   - The customer's text is placed by its code (`app.confirmation_store`)
     and confirms only when sent from the order's phone.
6. **Thanks:** an answer on WhatsApp (tap or text) is thanked by a free
   text (`order_answer_ack`): no quota, WhatsApp only, once.
7. **Notices** are made once each and kept 90 days. The quota warnings
   are also emailed to the owner and managers.
8. **Risk rules** (ADR-0066).
   - **Held order.** It gets no confirmation, so it has no deadline
     either.
   - **Verified order.** It is confirmed by message even where
     `confirm_cod` is off.
   - **A "yes" to a verified order.** Orders refuses the customer's move
     (`ErrNeedsCall`). The confirmation is still `confirmed`, but the
     order stays pending, and the `order_confirmed` notice carries
     `needs_call: true`.

## Dashboard (6g)

The merchant dashboard follows the approved 6g mockups:
- **رسائل العملاء** (`/stores/{id}/messages`): the confirmation switch and the
  auto-cancel hours (saved when the field is left), the month's quota (a
  warning at `nearly` and `reached`), the SMS text per language with
  placeholder buttons and a live preview (a text without `{{url}}` cannot be
  saved), and the WhatsApp template as the customer sees it. Support reads it;
  the owner and managers change it.
- **سجل الرسائل** (`…/messages/log`): one tab per status, 50 at a time;
  `?order=` shows one order's messages.
- **The bell** in the top bar: the unread count and the latest notices, a panel
  under it (a whole page on a phone); a notice's link or "mark all read" marks
  them read. Hidden from the shipping role (no `messaging.read`). Every screen
  mounts its own bell, so the last read is kept for a minute per store
  (`lib/messages.ts`): screens opened meanwhile show it without asking again.
- **The order page**: the customer confirmation card (the message's way, the
  answer, the auto-cancel time), shown first on a phone.

### Plan-limit notices (ADR-0046)

`Module.PlanLimitHandler`, subscribed as `messaging.plan_limits` to
`entitlements.usage.crossed` in the worker, adds a `quota_nearly` or
`quota_reached` notice with a `feature`. Without `feature`, the notice is
the messages quota:
- one per feature, month and threshold;
- with the `feature`, `used`, `limit` and `period`;
- emailed to the staff with a link to `/billing`.

Orders still come in; the notice asks for an upgrade.

## Webhooks

Each event is placed in its store by our reference
(`m1.<store>.<message>`) or our signed button payload
(`b1.<store>.<message>.<c|x>.<mac>`), then accepted once in that store
(ADR-0038). The processing job, `messaging.process_webhook`:
- **Statuses** move the message forward only. A status of an earlier
  attempt is `stale`. A status that arrives before the send was recorded
  waits 15 s.
  - For Twilio, the job reads the status back and applies Twilio's
    answer.
- **Button replies** record the first answer (later taps are
  `repeated_reply`) and mark the message read. They are then handed to
  the reply handler.
- **Customers' texts** are not placed yet (6c).

## Tables

| Table | Notes |
|---|---|
| `messages` | Tenant table under RLS. Partitioned monthly on the ULID time; partitions dropped after 6 whole months. PK `(store_id, created_at, id)`: a webhook's lookup reads one partition, and a page of the log reads every partition through that key, never by a scan (both pinned by plan tests). `messages_store_order` serves an order's messages, from the order's time on. `messages_store_status` (00054) serves one status's page without reading the others (pinned by `TestDashboardQueryPlans`). `app_user` cannot delete |
| `message_templates` | Tenant table under RLS. The store's SMS text per `(kind, locale)`; no row means the default |
| `messaging_settings` | Tenant table under RLS. `confirm_cod`, `auto_cancel_hours`; no row means the defaults |
| `order_confirmations` | Tenant table under RLS. One per order (PK `(store_id, order_id)`), with its message, status, deadline, answer, the SMS token's SHA-256 (unique per store), and the WhatsApp code (unique across stores; `app.confirmation_store` maps it to its store) |
| `merchant_notifications` | Tenant table under RLS. Unique `(store_id, kind, dedup_key)`; purged after 90 days as new ones arrive. The partial index `merchant_notifications_unread` (00054) serves the bell's unread count |

## Rules worth knowing

- **Numbers in clear are never written outside `content`,** and the
  module never logs them.
  - The webhook parts carry no sender number or text: texts are not
    placed.
  - WhatsApp's raw body, with every store's events, is not stored.
    Each store's inbox keeps only its own events.
- **Testing:**
  - The contract suite runs on the fake and on both stand-ins.
  - `internal/app` runs on real PostgreSQL. It covers:
    - sending by WhatsApp;
    - falling back to SMS when the send refuses the customer, and when a
      later status reports them unreachable;
    - stale statuses;
    - failures giving the unit back;
    - the quota being exceeded (nothing sent, no job);
    - no channel;
    - button replies (first one wins, forged payloads not placed);
    - cross-tenant references and payloads;
    - statuses arriving before the send was recorded;
    - Twilio's read-back winning over the webhook;
    - the store's texts (saved, used by the send job, reset, audited);
    - paging the log;
    - the plans of the message lookup and the log page.
  - `internal/httpapi` covers the happy path, validation, authorization
    and a cross-tenant test for every operation.
  - `acceptance_test.go` (P6 acceptance) runs catalog, orders, the
    webhooks inbox and messaging with River. It covers:
    - the confirmation round trip with the fake channel (confirm,
      cancel, replays, the merchant first);
    - the deadline, whether the customer was reached or not;
    - the quota exceeded (not sent, warned once in the dashboard and by
      email, order unaffected);
    - card orders and stores that opted out;
    - the SMS round trip (WhatsApp unreachable, then the SMS and its
      link confirm the order);
    - the customer-started confirmation (the link's ready text from the
      customer's phone confirms the order and is thanked; a stranger's
      does not; the delayed template is withdrawn and its unit
      returned).
- **Fuzzing:**
  - `domain`: `FuzzParseRef`, `FuzzParsePayload`, `FuzzFindCode`.
  - `whatsapp`, `twilio`: `FuzzVerifyWebhook`.
