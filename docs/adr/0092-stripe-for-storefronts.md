# ADR-0092: Stripe for merchants' storefronts

- Status: Accepted
- Date: 2026-11-05

## Context

The approved Stripe plan (G5) says that a merchant outside Egypt takes
its shoppers' cards on **its own Stripe account**, with its own keys, as
an Egyptian merchant does on its own Paymob account. Stripe Connect is
not used. Cash on delivery stays in both cases.

The payments module already takes a gateway through one contract
(ADR-0029, `gateway.Gateway`), passed by Paymob and the fake gateway.
Two of its parts were Paymob's:
- **Status by our reference.** `FetchStatus` asked by our reference only.
  Stripe cannot be asked by it reliably: its search is eventually
  consistent, up to a minute.
- **Paymob's keys.** Connecting required Paymob's keys: the API key and
  the webhook secret.

## Decision

1. **A `stripe` adapter** (`gateway/stripe`, on `platform/stripe`, shared
   with billing's ADR-0091) passes the same contract suite.
   - **Paying.** A payment is a Checkout Session in payment mode, cards
     only.
     - Our payment ID goes in `client_reference_id` and the metadata,
       and is the idempotency key.
     - The session expires after an hour, as the order waits for its
       card payment.
     - The shopper's card is not saved.
   - **Status.** The session read back with its PaymentIntent and latest
     charge:
     - paid when the session is paid and the intent succeeded, with what
       it received and what was refunded;
     - failed when the session expired or the intent was cancelled. A
       declined card stays on Stripe's page for another try;
     - pending otherwise.
     - The answer is believed only for our reference, in our currency.
   - **Refunds.** `POST /v1/refunds` on the PaymentIntent, sent once
     (ADR-0029).
   - **Webhooks.** Verified with the merchant's endpoint secret
     (`Stripe-Signature`, at most 5 minutes old). Only
     `checkout.session.*` events about our payments are read.
2. **`FetchStatus` takes a `gateway.Ref`**: our reference and the
   gateway's own payment (`Checkout.ProviderRef`, already stored on each
   payment). Paymob and the fake gateway still ask by ours. Stripe asks
   by its session.
3. **Each gateway names the keys it needs** (`gateway.KeysNeeded`).
   - Paymob, the default: the webhook secret and the API key.
   - Stripe: the secret (or restricted, `rk_`) key and the endpoint's
     signing secret (`whsec_`).
   - The contract's `api_key` becomes optional: a relaxed request, not a
     breaking change.
4. **Off until its screen** (`PAYMENTS_STRIPE_ENABLED`, default `false`).
   The dashboard's connect form is Paymob's until the approved mockups
   (G7). Which countries' merchants may connect it follows the payment
   rules (G6).

## Consequences

- **A merchant connects Stripe** by pasting a restricted key and a
  webhook endpoint secret. The endpoint is the connection's webhook URL,
  for the four `checkout.session.*` events. Stripe answers other events
  with retries, so the merchant subscribes to those four only.
- **The adapter is unverified against Stripe's test mode** until
  `STRIPE_SANDBOX_*` keys run its contract.
