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
whose word counts).

## Public API (`internal/modules/messaging/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount`, `Module.RegisterWorkers` | Wiring; the merchant HTTP API (behind identity's membership middleware); the send and webhook jobs (the worker role) |
| `Module.Sender()` → `Sender` | `EnqueueTx(ctx, tx, Outgoing)` queues a message in the caller's transaction; `Get` reads the log |
| `Module.PlatformProviders()` | The channels' webhook providers, for `webhooks.Module.RegisterPlatform` |
| `Module.SetReplyHandler(ReplyHandler)` | What acts on a customer's first answer, in the transaction that records it (6c: the order confirmation) |
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

## HTTP API (tag `messaging`)

| Operation | Permission | Notes |
|---|---|---|
| `listMessages` `GET /v1/stores/{store}/messages` | messaging.read | The log, newest first, paged by cursor; `status` filters. `order_id` gives an order's messages, without paging. Numbers are masked |
| `getMessageUsage` `GET …/messaging/usage` | messaging.read | The month's `used`, `limit`, `remaining` and `level` (`ok`, `nearly` at 80%, `reached`) |
| `listMessageTexts` `GET …/messaging/texts` | messaging.read | The SMS text for every kind and locale: the store's own (`custom`), or the default |
| `saveMessageText` `PUT …/messaging/texts/{kind}/{locale}` | messaging.manage | 1 to 400 characters, with only `{{name}}`, `{{store}}`, `{{ref}}`, `{{total}}` and `{{url}}`; an order confirmation needs `{{url}}` (422 otherwise). Audited |
| `resetMessageText` `DELETE …/messaging/texts/{kind}/{locale}` | messaging.manage | Back to the default; returns it. Audited |

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
| `messages` | Tenant table under RLS. Partitioned monthly on the ULID time; partitions dropped after 6 whole months. PK `(store_id, created_at, id)`: a webhook's lookup reads one partition, and a page of the log reads the partitions' keys backwards with no sort (both pinned by plan tests). `messages_store_order` serves an order's messages. `app_user` cannot delete |
| `message_templates` | Tenant table under RLS. The store's SMS text per `(kind, locale)`; no row means the default |

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
- **Fuzzing:**
  - `domain`: `FuzzParseRef`, `FuzzParsePayload`.
  - `whatsapp`, `twilio`: `FuzzVerifyWebhook`.
