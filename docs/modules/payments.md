# Module: payments

The payments module takes **card payments** through the merchant's own
gateway account. Paymob is the first gateway. Shoppers pay on the
gateway's hosted page, so card data never reaches the platform. Design:
[ADR-0029](../adr/0029-card-payments-paymob.md); the shopper's trip
through the API host: [ADR-0030](../adr/0030-card-checkout-through-api-host.md). Webhooks and sealed
keys: [ADR-0024](../adr/0024-inbound-webhooks-envelope-encryption-card-data.md).

## Public API (`internal/modules/payments/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount` | Wiring, and the merchant HTTP API (behind identity's membership middleware) |
| `Module.Checkout() Checkout` | `CardEnabled`, `StartCheckout`, `Refresh` for the store in ctx |
| `Module.MountCheckout` | The shopper's checkout routes (`checkout` tag) and, with the fake gateway, its hosted page (api role; needs `Deps.Hosts`, `ClientIP`, `Limiter`) |
| `NewCardCheck(pool) CardCheck`, `StartURL` | For the storefront, which holds no keys: does the store take cards, and where its shoppers pay |
| `Module.RegisterWorkers` | The expiry, refund, webhook and sync jobs (worker role) |
| `Module.WebhookProviders` | One per gateway; register them on the webhooks module (api role) |
| `Module.FakePayer()` | Pays the fake gateway's payments; nil unless `PAYMENTS_FAKE_GATEWAY=true` |
| `Payment`, `Status`, `CheckoutPage`, `PaymentStatusChanged` | DTOs; `EventPaymentStatusChanged` |

**Dependencies:** orders (`orders.Payments`: read an order, set its
payment status, move it), the webhooks inbox (worker), the envelope
`Sealer`, and River jobs.

## Rules

- **Connections.**
  - One active gateway per store.
  - The keys are checked with the gateway, then envelope-sealed, and
    are never returned.
  - Disconnecting stops new checkouts. Existing payments are still
    followed and can still be refunded.
- **Payments.**
  - A payment is one attempt to pay one card order: `pending`, then
    `paid` (or `failed`, `expired`), then `partially_refunded` or
    `refunded`.
  - A shopper who comes back within 30 minutes reuses the pending page.
- **The gateway's word only.**
  - Every webhook and every return of the shopper is re-queried with
    `FetchStatus` before anything changes.
  - Webhook bodies are only a pointer to the payment.
- **The order follows** in the same transaction:
  - paid: the order is marked `paid` and confirmed if pending;
  - money taken twice, or for a cancelled order: refunded at once;
  - refunds: the order is marked `partially_refunded` or `refunded`.
- **Expiry.** 60 minutes after the order is placed:
  - the gateway is asked once more;
  - an unpaid payment expires;
  - an order with no open or captured payment is cancelled, which
    releases its stock.
- **Refunds.**
  - They are partial or full, reserved when asked, and never more than
    was paid.
  - The job checks what the gateway already refunded before asking, so
    a retried job never refunds twice.

## The shopper's checkout (tag `checkout`, ADR-0030)

| Route | What |
|---|---|
| `GET /v1/pay/{store}/{order}?return=` | Opens or reuses the hosted page and redirects there (303). An order no longer awaiting payment, or a store without cards, goes straight back. Rate-limited per IP (20 in 10 min). |
| `GET /v1/pay/{store}/{order}/return/{to}` | The gateway's way back: asks the gateway now (`RefreshOrder`), then redirects to `to` (base64url) |
| `/v1/fake-gateway/checkout/{ref}` | The fake gateway's page: pay or decline (development only) |

The return URL must be an http(s) URL on a host of the same store
(tenancy's resolver); anything else is 404 before any payment is made.

## Gateways

- **The contract suite** (`internal/gateway/gatewaytest`) runs against
  every adapter.
- **`fakepay`:** in memory, signed with HMAC-SHA256 in `X-Fake-Signature`
  (not `fake`: that is the fake carrier, and webhook providers have
  unique names).
  It is used in tests, and in development when `PAYMENTS_FAKE_GATEWAY=true`.
- **`paymob`:**
  - The Intention API with the unified checkout; status from
    `transaction_inquiry`; refunds from `void_refund/refund`.
  - Webhooks carry the HMAC-SHA512 in `?hmac=`.
  - The region follows the currency (EGP: accept, SAR: ksa, AED: uae),
    unless `PAYMOB_BASE_URL` overrides it.
  - It follows the documentation and is unverified until the sandbox
    run: `PAYMOB_SANDBOX_SECRET_KEY=… PAYMOB_SANDBOX_PUBLIC_KEY=… PAYMOB_SANDBOX_API_KEY=… PAYMOB_SANDBOX_HMAC_SECRET=… PAYMOB_SANDBOX_INTEGRATION_ID=… go test ./internal/modules/payments/internal/gateway/paymob/`.
- **The webhook parser** is fuzzed (`FuzzVerifyWebhook`).

## HTTP API (tag `payments`)

| Operation | Permission |
|---|---|
| `listPaymentConnections` | payments.manage |
| `connectPaymentGateway` (checks the keys; one active gateway) | payments.manage |
| `disconnectPaymentGateway` | payments.manage |
| `listPayments` (`order_id`, or `cursor` + `limit` ≤ 200) | orders.read |
| `getPayment` (with its refunds) | orders.read |
| `refundPayment` (`amount_minor`, `reason` ≤ 300; 202) | payments.refund |

`payments.manage` is the owner's alone. `payments.refund` belongs to the
owner and the manager.

## Jobs

| Kind | When | What |
|---|---|---|
| `payments.expire_payment` | 60 min after the order | Last inquiry, then expire, and cancel the order if nothing is open or paid |
| `payments.process_webhook` | An accepted webhook | Re-query, apply, record the receipt's outcome (`updated`, `unchanged`, `unknown_payment`, `unreadable`) |
| `payments.refund_payment` | A refund asked | Check what was already refunded, refund, record |
| `payments.sync_payment` | After a refund | Re-query and apply the new amounts |

## Data

`payment_connections`, `payments`, `payment_refunds` (migration 00035):
all with `store_id`, behind FORCE RLS, with composite foreign keys
within the store.
