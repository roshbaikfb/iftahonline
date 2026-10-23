# ADR-0029: Card payments: one gateway contract, a fake, Paymob, and the gateway's word

- Status: Accepted
- Date: 2026-10-23

## Context

Stores in Egypt and the Gulf mostly sell cash on delivery, but many
shoppers want to pay by card, and Gulf shoppers expect it. P5 asks for:

- a `PaymentGateway` interface (create a payment on a hosted page,
  verify webhooks, fetch status, refund);
- a Paymob adapter and a fake gateway, with one contract suite;
- merchant credentials envelope-encrypted;
- inbound webhooks stored raw, verified, processed once, and state
  changed only after re-querying the provider;
- no card number anywhere (a CI grep test).

Decisions taken with the plan:

- card-paid orders confirm themselves;
- an unpaid card order is cancelled after 60 minutes, releasing stock;
- partial refunds are allowed;
- KMS was not chosen, so the key-encryption key stays the env keyring
  behind the `KEK` interface (ADR-0024);
- there are no sandbox accounts yet: the fake, plus local stand-ins
  built from Paymob's documentation.

ADR-0024 already gives us the webhooks inbox, sealed credentials and the
no-card-data test. This ADR covers what is specific to payments.

## Decision

1. **The money belongs to the merchant.** Each store connects its own
   Paymob account (a "connection", like a carrier's). The platform is
   never the merchant of record and holds no funds. The keys
   (secret, public, API key, HMAC secret, card integration IDs) are
   sealed with AAD `payments:<gateway>:<store>:<connection>`. They are
   checked with the gateway before they are stored. There is one active
   gateway per store.
2. **One contract** (`internal/gateway`): `Verify`, `CreatePayment`,
   `VerifyWebhook`, `ParseWebhook`, `FetchStatus`, `Refund`. The suite
   in `gatewaytest` runs against the fake and against Paymob's local
   stand-in (`paymobtest`). It runs against Paymob's test mode when keys
   are given (`PAYMOB_SANDBOX_*`).
   - **Paymob.** The Intention API makes the hosted page
     (`/v1/intention/` with the secret key, then
     `/unifiedcheckout/?publicKey&clientSecret`). `special_reference` is
     our payment ID. Status comes from `transaction_inquiry` by
     merchant order ID, refunds from `void_refund/refund`, both with an
     auth token from the API key. Webhooks are verified by the HMAC
     (SHA-512 over Paymob's 20 ordered fields, in `?hmac=`), compared in
     constant time. The base URL follows the currency (Egypt, Saudi
     Arabia, the UAE) unless `PAYMOB_BASE_URL` overrides it.
   - **The fake** is in memory, signs its webhooks with HMAC-SHA256
     (`X-Fake-Signature`), and is offered only when
     `PAYMENTS_FAKE_GATEWAY=true` (refused in production).
     `Module.FakePayer` plays the shopper in end-to-end tests and demos.
3. **A payment is one attempt** (migration 00035: `payments`,
   `payment_refunds`, `payment_connections`).
   - **Checkout.** `StartCheckout` writes a `pending` payment and its
     expiry job in one transaction, then asks the gateway for the
     hosted page (8 s bound while the shopper waits). A failure marks
     the attempt `failed` and returns 503.
   - **Reuse.** A shopper who comes back within 30 minutes gets the same
     page, not a second payment.
   - **The order.** It holds `payment_method` (`cod`/`card`) and
     `payment_status` (`unpaid`, `awaiting`, `paid`,
     `partially_refunded`, `refunded`, migration 00034). Card orders
     carry no COD fee.
4. **Only the gateway's word changes state.** A webhook says only
   "look at payment X". The job asks the gateway (`FetchStatus`), and
   `domain.Next` derives the payment's new status from that answer
   alone. The webhook body's amounts are never trusted, and unsigned
   fields (Paymob's `merchant_order_id`) are only a lookup key. So a
   replayed, reordered or forged-but-valid delivery cannot move money.
   - The inbox accepts a delivery once per provider event ID.
   - A replay is `duplicate`, and a re-fetch that changes nothing is
     `unchanged`. Replaying a webhook five times changes state once,
     which is tested end to end with River.
5. **The order follows its payment** in the same transaction (actor
   `gateway:<name>`):
   - a newly captured payment marks the order `paid` and confirms it if
     it is pending;
   - money that should not have been taken is refunded at once: a
     second payment of an order already paid, or a payment that arrives
     after the order was cancelled;
   - refunds mark the order `partially_refunded` or `refunded`;
   - `payments.payment.status_changed` is published on every status
     change.
6. **Expiry.** A job runs 60 minutes after the order was placed:
   - It asks the gateway one last time, so a last-second payment wins.
   - A payment still not paid becomes `expired`.
   - If none of the order's payments is open or captured, the order is
     cancelled (actor `system:payment_expiry`), and cancelling releases
     its stock.
7. **Refunds.**
   - **Request.** `Refund` reserves the amount at once: requested and
     succeeded refunds can never exceed what was paid. The job then asks
     the gateway.
   - **No double refunds.** Paymob takes no idempotency key, so the job
     calls `FetchStatus` first. If the gateway already shows more
     refunded than we recorded, the refund is recorded instead of asked
     for again. A success is recorded before anything else can fail.
   - **Then** a sync job fetches the new amounts. A refusal fails the
     refund with the gateway's reason.
8. **Permissions** (ADR-0006):
   - `payments.manage` (connect or disconnect the gateway) is the
     owner's alone, because it decides where the store's money goes;
   - `payments.refund` belongs to the owner and the manager;
   - reading payments needs `orders.read`.
9. **Retention.** Raw webhook bodies are kept 90 days (the partition
   retention of ADR-0024). Payments and refunds are kept, as accounting
   records.

## Consequences

- **Unverified against Paymob.** The Paymob adapter follows the
  documentation and is unverified until it runs against test keys. The
  contract suite and the stand-in fix our reading of the docs; a test
  run against Paymob may change field names.
- **Who confirms.** Card orders need no confirmation call, but a merchant
  who wants to call anyway must do it after the order is confirmed.
- **Expiry cancels for good.** An order cancelled by expiry stays
  cancelled. If the shopper pays later, the money is refunded
  automatically, and they place a new order.
- **Gateway access is outbound only.** Webhooks reach us at
  `/v1/webhooks/<gateway>/<store>/<connection>`, a URL sent with every
  payment, so merchants configure nothing at Paymob beyond their keys.
- **Disconnecting stops new checkouts only.** Payments already made are
  still followed, and can be refunded, with the sealed keys.
