# ADR-0039: Messaging channels, the message log, and WhatsApp's word

- Status: Accepted
- Date: 2026-10-28

## Context

P6 sends customer messages for each store: first the order
confirmation, with Confirm and Cancel buttons. The owner approved
(2026-10-28) that messages go from **the platform's own WhatsApp
number** (ADR-0038), with SMS as the fallback, through one platform-wide
SMS account.

There is no Meta app and no SMS account yet. So the adapters must be
built from the providers' documentation, tested against in-process
stand-ins, and marked unverified until real test accounts exist. That
is what we did for Bosta and Paymob.

What the providers allow:
- **WhatsApp Cloud API:**
  - It takes no idempotency key.
  - It accepts a message, and only later reports by webhook that the
    customer is not on WhatsApp (a `failed` status with code 131026).
  - It returns our `biz_opaque_callback_data` with every status of the
    message.
  - It returns a quick-reply button's payload when the customer taps it.
  - It signs webhooks with HMAC-SHA256 under the app secret, and batches
    up to 1,000 updates in one delivery.
  - There is no API that tells us what a customer replied, or a
    message's status, after the fact.
- **Twilio:**
  - It signs status callbacks with HMAC-SHA1 over the URL and the form.
  - A message's status can be read back.

## Decision

1. **One channel contract**
   (`messaging/internal/channel`): `Send`, `VerifyWebhook` and
   `Undeliverable(code)`; `Fetcher` is optional, for a channel whose
   statuses can be read back.
   - The errors are `ErrUndeliverable` (try another channel),
     `ErrRejected`, `ErrAuth`, and transient errors.
   - `Send` never retries on its own, because there is no idempotency
     key.
   - A fake adapter, WhatsApp Cloud and Twilio SMS all pass one contract
     suite. The two real adapters run it against stand-ins of the
     provider APIs.
2. **The message log** is the `messages` table, partitioned monthly on
   the message's ULID time and kept 6 months.
   - The recipient and the template values are sealed in `content`
     (secretbox, AAD `messaging:<store>:<message>`). The log shows only
     the masked number. The send job is the only code that opens
     `content`.
   - Statuses only move forward: queued, sent, delivered, read; failed
     only from queued or sent. Statuses arrive out of order and more
     than once.
3. **Queuing is transactional.** `Sender.EnqueueTx` runs in the caller's
   transaction:
   - It consumes one quota unit, writes the log entry and inserts the
     send job.
   - Past the quota, the message is logged as `skipped_quota` and nothing
     is sent. That is not an error, so the order goes on; the caller warns
     the merchant from the returned usage.
   - A message that no channel delivered fails, and its quota unit is
     given back.
4. **WhatsApp first, then SMS, once.** The message falls back to SMS in
   two cases: WhatsApp refuses the customer when we send, or a `failed`
   status reports the customer unreachable later. The SMS is a new send
   job; it costs no second quota unit.
   - Statuses of the earlier WhatsApp attempt are then **stale**: a
     status counts only when its provider and provider message ID match
     the message's current ones.
5. **Every webhook event is placed in its store by something we
   issued.**
   - A status is placed by our reference `m1.<store>.<message>`
     (WhatsApp's `biz_opaque_callback_data`, Twilio's callback query).
   - A button reply is placed by our payload
     `b1.<store>.<message>.<c|x>.<mac>`, an HMAC-SHA256 truncated to 128
     bits under a key derived (HKDF) from `OTP_PEPPER`.
   - Each event then goes through the per-store inbox (ADR-0038), and the
     processing job checks again that the reference's store is the job's
     store.
   - A customer's free text is not placed yet. The customer-initiated
     confirmation in 6c will place it by a code.
6. **Whose word counts (deviation from ADR-0024).**
   - Twilio statuses are hints: the job reads the status back from
     Twilio and applies what Twilio says.
   - WhatsApp cannot be asked, so its statuses and replies are applied on
     the evidence of Meta's signature over the delivery and, for a reply,
     our own signature on the payload. Nothing that moves money depends on
     a WhatsApp status.
   - A reply only records the customer's first answer. 6c's confirmation
     then moves the order through `orders.Fulfilment`, whose state
     machine refuses an order the merchant already handled.
7. **A status can arrive before the send is recorded.** If a status
   comes before the send job has recorded the message as sent, the
   processing job waits 15 seconds and tries again.
8. **Configuration:**
   - `WHATSAPP_PHONE_NUMBER_ID`, `WHATSAPP_ACCESS_TOKEN`,
     `WHATSAPP_APP_SECRET`, `WHATSAPP_VERIFY_TOKEN` (and optionally
     `WHATSAPP_GRAPH_URL`): all or nothing.
   - `TWILIO_ACCOUNT_SID`, `TWILIO_AUTH_TOKEN`, `TWILIO_FROM`: all or
     nothing.
   - `MESSAGING_FAKE_CHANNELS` fills the channels that are not
     configured with fakes. It is refused in production.
   - The webhook URLs are `/v1/webhooks/whatsapp` and
     `/v1/webhooks/twilio` under the platform's public origin.

## Consequences

- If an answer is lost between WhatsApp or Twilio accepting a message
  and our recording it, the retried job sends the message again. The
  customer may get it twice; a second reply changes nothing.
- The platform's WhatsApp templates have to be created once in Meta's
  Business Manager, and approved, before messages can go out.
  `docs/modules/messaging.md` has their texts.
- The message log is per store and kept 6 months. The store's customers'
  numbers are never written in clear outside `content`.
- Until the real accounts have run the contract suite, treat the
  adapters' field details as unverified.
