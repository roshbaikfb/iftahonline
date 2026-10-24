# ADR-0030: Card checkout passes through the API host

- Status: Accepted
- Date: 2026-10-24

## Context

ADR-0029 gave us card payments on the backend. Shoppers now need to pay
from the storefront:

- choose card at checkout;
- go to the gateway's hosted page;
- come back to a thank-you page that knows whether they paid.

Opening a hosted page, or asking the gateway about a payment, needs the
merchant's gateway keys. The keys are sealed with the platform keyring
(ADR-0024). The storefront role does not load the keyring, on purpose:

- it is the role most exposed to the internet (every shopper, every
  bot);
- whoever controls a process with the keyring can open every
  merchant's keys, and refund from their accounts.

We will not widen that by giving the storefront the keyring.

## Decision

1. **The shopper passes through the API host**, which already holds the
   keys (api role, `checkout` tag, no session):
   1. The storefront places the card order. It never redirects from
      the form: it sends the shopper to its own thank-you page with
      `?pay=1`.
   2. The thank-you page sends the shopper on, with a one-second meta
      refresh and a visible "pay now" link, to
      `GET {CHECKOUT_URL}/v1/pay/{store}/{order}?return=<thank-you URL>`.
   3. The API host opens (or reuses, within 30 minutes) the gateway's
      page and redirects there (303).
   4. The gateway sends the shopper back to
      `/v1/pay/{store}/{order}/return/{to}`. `to` is the thank-you URL,
      base64url-encoded, so the gateway's own query parameters cannot
      change it.
   5. That route asks the gateway at once (`RefreshOrder`, 8 s bound),
      then redirects to the thank-you page.
2. **Why the meta refresh.** The storefront's CSP has
   `form-action 'self'`, which browsers apply to a form's whole
   redirect chain. A redirect from the form would therefore need the
   API host and every gateway host in it. A meta refresh is a
   navigation, not a form submission.
3. **No open redirect.** The return URL must be an http(s) URL, without
   credentials, at most 2,000 bytes, on a host that resolves (through
   tenancy's resolver) to the store in the path. Anything else is 404,
   before any payment is made.
4. **The order states the shopper sees.** A failure never strands the
   shopper on the API host: every error redirects to the thank-you page,
   which shows one of:
   - **paid**;
   - **not paid yet**: it looks again every 10 seconds, because the
     webhook may be seconds away, and offers "pay now";
   - **cancelled**: the hour passed (ADR-0029).
5. **Abuse bounds.** Starting a checkout is rate-limited per client IP:
   20 in 10 minutes. A shopper retrying reuses the pending page, so a
   retry costs no new gateway call.
6. **The storefront reads, and holds no keys.**
   - `payments.CardCheck` (`NewCardCheck(pool)`) only reads whether
     the store has an active connection.
   - Checkout from the cart offers card payment when it does. A failed
     check offers cash on delivery only.
   - A card choice that is no longer available is refused (409), never
     silently placed as cash on delivery.
   - The quote leaves out the COD fee for card payment.
7. **Local trials.** With `PAYMENTS_FAKE_GATEWAY=true`, the fake
   gateway's hosted page is served at `/v1/fake-gateway/checkout/{ref}`:
   pay or decline, then back through the return route. It carries its
   own CSP (`form-action 'self'`, and it goes back by meta refresh).
   Locally, `CHECKOUT_URL` is the dashboard's dev server, which proxies
   `/v1` to the api.

## Consequences

- **The shopper sees a second host.** `app.iftahonline.com` appears in
  the address bar for an instant on the way to the gateway and back. It
  is not a page they read.
- **The storefront role still has no keyring.** Compromising it gives no
  merchant's gateway keys.
- **The API host serves shoppers.** It takes a small amount of
  shopper-facing traffic (two redirects per card order), within its rate
  limits.
- **Landing pages stay cash on delivery for now.** Their quick-order
  forms are edge-cached and shared between shoppers. Offering cards
  there means invalidating those pages when a merchant connects or
  disconnects a gateway, which is left for later.
- **Merchant-facing setup is unchanged.** Merchants configure nothing new
  in Paymob: the return URL travels with each payment.
