# ADR-0038: Interim quotas, platform-owned messaging accounts, and a shared mailer

- Status: Accepted
- Date: 2026-10-28

## Context

P6 sends WhatsApp and SMS messages on each store's behalf: an order
confirmation with Confirm and Cancel buttons, then the customer's reply
by webhook. Three things it needs did not exist yet:

1. **A per-store quota.** Messages cost money. The spec asks for quotas
   through an Entitlements interface: stubbed now, implemented from
   plans in P7. When a store passes its quota, the message is not sent,
   the merchant is warned, and the order is unaffected.
2. **A platform-owned account.** The owner approved (2026-10-28) that
   messages go by default from **one WhatsApp number of the platform**,
   with the store's name in the message. Small merchants would otherwise
   have to set up Meta Business accounts themselves. A merchant may
   still connect their own number later. Meta sends a number's webhooks
   to one callback URL, and verifies that URL first with a GET handshake
   (`hub.mode=subscribe`, `hub.verify_token`, `hub.challenge`). The
   webhooks module (ADR-0024) only knew per-store connection URLs, and
   had no handshake.
3. **Email from a second module.** Merchant notices (quota warnings,
   confirmations, auto-cancellations) go by email as well as in the
   dashboard. The SMTP mailer lived in `identity/internal/notify`, which
   no other module can import.

## Decision

1. **`entitlements` module (interim).**
   - `ConsumeTx(ctx, tx, feature, n)` counts units in the **caller's**
     tenant transaction, with one conditional upsert on
     `usage_counters (store_id, feature, period)`:
     `… DO UPDATE SET used = used + n WHERE used + n <= limit`.
     - The row lock serializes a store's concurrent senders, so the last
       unit is spent once.
     - A rolled-back send never counts.
     - Past the limit, nothing is counted and `ErrQuotaExceeded` is
       returned with the month's usage.
   - `ReleaseTx` gives units back to the month they came from.
   - `Usage` reads the month's count.
   - Months are calendar months in UTC for every store.
   - Limits come from the environment
     (`ENTITLEMENTS_MESSAGES_PER_MONTH`, default 500); a WhatsApp message
     and an SMS are one unit each. P7 replaces the `Limits` source with
     the store's plan; the interface stays.
   - Warnings: `Usage.CrossedBy(n)` reports the 80% or 100% threshold
     that a consumption crossed, so the caller warns the merchant once
     per threshold and month.
2. **Webhook URL handshakes.** A `Provider` may also implement
   `Challenger`. `GET /v1/webhooks/{provider}/{store}/{connection}`
   then echoes the challenge when the token matches the connection's.
   - The echoed challenge must be 1 to 200 URL-safe characters, and is
     sent as `text/plain` with `nosniff` and `no-store`. The endpoint
     cannot be used to reflect markup.
   - A wrong token is 401 and logged; providers without a handshake are
     404.
3. **Platform-owned accounts get one URL:** `/v1/webhooks/{provider}`
   (POST and the GET handshake), for a `PlatformProvider`.
   - The provider verifies the whole delivery with the platform's
     secret. It then splits the delivery into one `Part` per store event:
     store, connection, event id, and that event alone as the body.
   - The provider places each event from data **we** sent and Meta
     echoes back. For WhatsApp, that is the message ID we put in
     `biz_opaque_callback_data` and in the signed button payload. It
     drops what it cannot place.
   - Each part is accepted in its own store's transaction, exactly like
     a connection's delivery: once per event id, with its body and its
     processing job.
   - `webhook_receipts` and `webhook_bodies` stay tenant tables. No
     platform-wide table and no migration are needed.
   - A failure part-way fails the delivery, and the provider retries
     it; the parts already accepted are then replays. A part whose store
     is gone is dropped with a log line.
   - There is a separate rate limit per platform account: 6,000 a
     minute.
4. **The mailer moves to `platform/mailer`.** It is used by identity and,
   from P6, by messaging. `mailer.FromConfig` picks SMTP, or the logging
   stand-in when `SMTP_ADDR` is unset. Phone-code senders
   (`OTPSender`) stay in identity: SMS will implement them from the
   messaging module and be wired in `cmd/platform`.

## Consequences

- The quota is enforced in the database, inside the send's own
  transaction. There is no counter in Redis to drift and no
  reconciliation job. The cost is one row lock per store while its send
  transaction is open; those transactions are short.
- The platform pays Meta for messages sent from its number. The quota
  is what bounds that cost until plans charge for it (P7).
- Meta webhooks for every store arrive at one URL. A bug that places an
  event in the wrong store would cross tenants. So events are placed only
  from IDs the platform generated and signed, and the WhatsApp provider's
  tests include a cross-tenant case (P6 6b).
- WhatsApp cannot be re-queried for an inbound reply or status, which
  ADR-0024 expects of providers. Processing will trust Meta's signature
  together with our own signature on the button payload. ADR-0039 (6b)
  records that deviation.
- `usage_counters` keeps one row per store, feature and month, with no
  purge. At 5,000 stores that is 60,000 rows a year.
