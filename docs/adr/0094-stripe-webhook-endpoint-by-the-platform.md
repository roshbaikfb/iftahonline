# ADR-0094: The platform creates a merchant's Stripe webhook endpoint

- Status: Accepted
- Date: 2026-11-06

## Context

The approved G7 mockup asked a merchant connecting Stripe (ADR-0092) to:
1. copy the connection's webhook URL;
2. create an endpoint for it in Stripe;
3. paste the endpoint's signing secret.

That order cannot work. The URL holds the connection's ID
(`/v1/webhooks/stripe/{store}/{connection}`), and the connection exists
only after the keys are given.

The owner chose (2026-11-06) that the platform creates the endpoint
itself.

The same review settled how the dollar plans show VAT. The mockup had a
payment summary before paying, but choosing a plan applies it at once and
the plans API returned no tax. The owner chose a note on each paid plan.

## Decision

1. **Connecting Stripe takes one key.** It is a restricted key with:
   - Checkout Sessions: Write
   - PaymentIntents: Read
   - Refunds: Write
   - Webhook Endpoints: Write

   A secret key also works.
2. **Gateways may provision their endpoint** (`gateway.Provisioner`).
   `Connect` works in this order:
   1. It checks the key.
   2. It takes the connection's ID.
   3. It creates the endpoint at the connection's URL:
      - for the four `checkout.session.*` events;
      - on the pinned API version;
      - with the URL as the idempotency key.
   4. It keeps the endpoint's ID and secret with the key, sealed.

   If Stripe refuses the endpoint, the key is refused. A connection that
   is not kept deletes the endpoint again, best effort.
3. **Disconnecting deletes the endpoint** in a job
   (`payments.unprovision_webhook`), inserted in the disconnect's
   transaction. An endpoint already gone is no error.
4. **The contract** relaxes `webhook_secret` to optional (Paymob still
   needs its HMAC).
5. **The plans API returns the tax** of the store's country (`tax_bps`).
   Each paid plan shows "قبل ضريبة القيمة المضافة (15%)". Choosing a plan
   is unchanged.

## Consequences

- **A restricted key without Webhook Endpoints: Write** cannot connect.
  The dashboard lists the four permissions.
- **Stripe for storefronts stays behind `PAYMENTS_STRIPE_ENABLED`.** It
  goes on after a run in Stripe's test mode (docs/go-live.md).
