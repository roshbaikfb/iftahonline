# ADR-0091: Stripe for the platform's billing outside Egypt

- Status: Accepted
- Date: 2026-11-05

## Context

Merchants outside Egypt pay the platform in dollars (ADR-0090) through
the platform's own Stripe account, held by its UK company (the approved
Stripe plan, G4). Paymob cannot charge dollars. The payment rules
(ADR-0089) already name each country's billing gateway: `paymob` for
Egypt, `stripe` for the others.

Until now billing had one checkout gateway, Paymob, by name. A checkout
did not record its gateway, and Paymob's callbacks reached one URL.

## Decision

1. **A checkout opens on its account's country's billing gateway**: the
   payment rule of the account's country, else the others' (`ZZ`).
   - The checkout records it (`billing_checkouts.gateway`, migration
     00106; older rows are Paymob's).
   - Without that gateway configured, the page is unavailable (503). It
     never falls back to another gateway: the rule is the console's
     decision.
2. **Each checkout gateway has its own callback URL**:
   `/v1/webhooks/<gateway>_billing`, i.e. `paymob_billing` (unchanged)
   and `stripe_billing`.
   - The receipt's provider names the gateway that processes it.
   - A callback placed in a checkout opened on another gateway is not
     acted on (outcome `other_gateway`).
3. **The gateway interface** grows, for both gateways:
   - `ParseCallback` gets the request's headers (Stripe signs in
     `Stripe-Signature`, Paymob in the query).
   - `Paid` takes a `PaidQuery` and answers a `Payment`. Stripe is asked
     by the Checkout Session (`billing_checkouts.session_id`), and its
     answer carries the card the payment saved. The card is saved before
     the payment is recorded, so a failure retries both. Paymob's card
     still arrives in a callback of its own.
4. **The Stripe adapter** (`gateway/stripe`, on `platform/stripe`, which
   G5 shares):
   - **Checkout.** A Checkout Session in payment mode. It creates a
     customer and saves the card for off-session use
     (`setup_future_usage=off_session`). Our reference goes in
     `client_reference_id` and the metadata. The idempotency key is
     `checkout:<reference>`.
   - **Card on file.** The customer and payment method, sealed like
     Paymob's token.
   - **Charge.** An off-session PaymentIntent, confirmed at once.
     - Stripe is first searched by the attempt's reference. The request
       then carries `charge:<reference>` as its idempotency key. A lost
       answer is found, never charged twice.
     - A decline is a result with Stripe's message.
     - A bank asking for authentication is a decline
       (`authentication_required`): the merchant pays on the page, which
       saves a new card.
     - A payment still processing is not known yet: the job retries.
   - **Webhooks.**
     - A webhook is believed only with the endpoint's HMAC-SHA256
       signature, compared in constant time, at most 5 minutes old.
     - Only `checkout.session.*` events about one of our checkouts are
       read.
     - Nothing is marked paid until Stripe confirms it: the session is
       complete and paid, for our reference, in full, in its currency.
   - **The API version** is pinned (`2024-06-20`).
5. **Configuration** (api and worker), both values or neither, checked
   at startup:
   - `BILLING_STRIPE_SECRET_KEY`: `sk_` or, preferably, a restricted
     `rk_` key;
   - `BILLING_STRIPE_WEBHOOK_SECRET`: `whsec_`;
   - `BILLING_STRIPE_BASE_URL`: tests only.

## Consequences

- **Gulf billing can be switched on** once the Stripe account is set up
  and the contract is run against Stripe's test mode (the pre-launch
  checklist). The dunning charges each account's card with the gateway
  that saved it.
- **Older Gulf accounts in riyals or dirhams** (ADR-0090) open their
  checkouts on Stripe too, in their own currency.
- **A merchant whose bank requires 3DS on every charge** pays each
  invoice on the page: off-session charges are declined for them.
- **Stripe's customers are not reused across checkouts.** Each page
  creates one; the card on file is the last one saved.
