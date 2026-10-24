# ADR-0031: Money safety after the P5 review

- Status: Accepted
- Date: 2026-10-24

## Context

A review of P5 (ADR-0024 to ADR-0030) found these problems:

1. **A Paymob refund could be sent twice.** The adapter retried it
   inside `retry.Do`. Paymob takes no idempotency key, so a refund whose
   answer was lost went out again.
2. **A refund could be recorded as made without being sent.** The
   refund job compared the gateway's refunded amount with the payment's
   `refunded_minor`, which only the later sync job updates. A second
   refund run before the first one's sync was recorded as made and never
   sent.
3. **A card order could hold its stock forever.** The hour's expiry was
   scheduled only when a checkout started. A card order whose shopper
   never reached the gateway stayed pending, holding its stock.
4. **Jobs took a second connection inside a transaction.** Jobs read
   the order through `orders.Get` while holding a transaction, so each
   held two pool connections, and enough of them at once deadlocked the
   pool.
5. **Smaller findings:**
   - the way back from the gateway was not rate-limited;
   - a Paymob inquiry was believed whatever order, currency or capture
     state it answered with;
   - providers' error bodies (which may echo a phone number or an email)
     were kept as they came;
   - three migrations validated `CHECK`s on `orders` and `order_events`
     under an `ACCESS EXCLUSIVE` lock;
   - shipments a carrier never finishes were polled forever;
   - the cursor and the credential sealing were duplicated.

## Decision

1. **A gateway sends a refund at most once.** The `Gateway` contract says
   so. A lost answer is a retryable error, and the caller asks
   `FetchStatus` before trying again.
2. **Refunds of one payment run one at a time, oldest first.**
   - The refund job holds the payment's row (`FOR UPDATE`) while it asks
     the gateway and refunds, with the calls bounded to 20 s. A newer
     refund snoozes while an older one is unanswered.
   - The job compares the gateway's refunded amount with the refunds
     recorded as made.
   - So when the gateway shows more than that, the extra is the oldest
     unanswered refund (this one), whose answer was lost. It is recorded,
     not sent again.
3. **A card order's expiry starts with the order.**
   - Payments subscribes to `orders.order.placed` (published in the
     order's transaction) as `payments.watch_card_orders`.
   - The handler schedules `payments.expire_order`, an hour after the
     order was placed, and unique per order.
   - The expiry asks the gateway one last time about each open payment,
     then cancels the order if nothing is open or captured.
   - The cancel locks the order and its payments: a payment the gateway
     confirms meanwhile waits, then finds the order cancelled and is
     refunded (ADR-0029), and a merchant's move wins.
   - Every role subscribes, because publishers need the subscriber list.
     The worker supplies the handler before River starts.
4. **Inside a transaction, read through it.**
   - `orders.Fulfilment` has `GetTx`, which reads and locks the order in
     the caller's transaction, and the shipping port has `OrderTx`.
   - The acceptance tests of payments and shipping run orders and the
     module on a one-connection pool, which deadlocked with the old
     reads.
5. **The smaller findings:**
   - The way back is bounded to 30 inquiries in 10 minutes per IP. Past
     the bound the shopper is still sent back, without the inquiry.
   - A Paymob inquiry counts only for the reference asked about and the
     payment's currency (when Paymob states one). An authorization
     without a capture is pending.
   - Providers' messages pass through `text.Redact`: emails and numbers
     of 7 or more digits are hidden.
   - From migration 00036, an `ALTER TABLE` that adds a `CHECK` or a
     foreign key to an existing table must be `NOT VALID`, then
     validated in a later migration, unless the line says why the table
     is small (`internal/tools/migsafe`). 00029, 00031 and 00034 ran
     before any production data and are not edited.
   - A shipment is polled for at most 60 days; its webhooks still move
     it.
   - `platform/cursor`, `envelope.SealJSON`/`OpenJSON`/`ConnectionAAD`
     and `webhooks.URL` replace the copies. The AAD is unchanged.

## Consequences

- **Webhooks for a payment can wait.** While a refund job holds a
  payment (20 s at most), that payment's webhook and sync jobs wait for
  it. Refunds are rare, and the jobs retry.
- **Expiry jobs per card order.** Card orders get an expiry job each,
  alongside the per-payment expiry kept from ADR-0029. Both are
  idempotent.
- **Not changed, by choice:**
  - A carrier webhook is not refused when the shipment belongs to
    another (older) connection of the same carrier. That is a merchant
    who reconnected, and the re-query uses the shipment's own
    connection.
  - Webhook receipts are purged per store as new ones arrive (FORCE RLS
    rules out a global purge). A store that stops receiving keeps its
    last receipts until it receives again.
  - `payments.order_id` has no foreign key: `orders` is partitioned by
    month, and its key includes the month.
- **Unverified against Paymob.** The currency check accepts an answer
  without a currency, to be confirmed in the sandbox run (ADR-0029).
