# ADR-0052: Charging merchants' cards on file with Paymob

- Status: Accepted
- Date: 2026-10-30

## Context

The approved payment methods are Paymob with a saved card, a pay link,
and a transfer the platform confirms. ADR-0051's dunning charges the
card on file through a `Gateway`. These are not the stores' own Paymob
accounts (ADR-0029): they are **the platform's** accounts, one per
region, charging merchants.

Paymob's official "Pay with saved card" collection
(PaymobAccept/API-Postman-Collections) describes the flow.

**Saving a card:**
- The first payment goes through a 3DS Intention paid on the Unified
  Checkout with "save card" ticked.
- A `TOKEN` callback follows, signed with HMAC-SHA512 over `card_subtype`,
  `created_at`, `email`, `id`, `masked_pan`, `merchant_id`, `order_id`
  and `token`, in that order.

**A merchant-initiated (MIT) charge:**
- An Intention on the account's **MOTO** integration, which Paymob
  enables on request.
- Then `POST /api/acceptance/payments/pay` with
  `{source: {identifier: <token>, subtype: "TOKEN"}, payment_token: <payment key>}`.
- It is approved when `success` and `txn_response_code = "APPROVED"`.

Payments' adapter already did, inside its own module:
- Paymob's region by currency;
- one JSON request with Paymob's error shapes;
- the transaction callback's HMAC;
- auth tokens and transaction inquiries.

A second module needing the same API is the CLAUDE.md signal for
`internal/platform`.

## Decision

1. **`platform/paymob`** holds what both modules need: `BaseFor`, `Do`,
   `AuthToken`, `Inquire`, `Transaction`, `ParseCallback`,
   `Verify`/`Sign`/`MAC` over a field list, `TransactionFields` and
   `TokenFields`.
   - Payments' adapter is rebased on it, with its behaviour and tests
     unchanged. Its own errors (`gateway.ErrAuth`, `gateway.ErrRejected`)
     are mapped from the platform's.
   - Each module keeps its own adapter.
2. **Billing's adapter** (`billing/internal/gateway/paymob`) charges a
   card on file:
   - **Inquiry first.** It asks Paymob, through an auth token from the
     region's API key, for a transaction of the attempt's reference, and
     uses that answer if there is one.
   - **Only then the charge.** It creates the MOTO Intention (its
     `special_reference` is the attempt's reference) and pays with the
     token.
   - **Outcomes.** A pending transaction, or a lost answer, is an error:
     the job retries, and the retry asks first.
3. **One reference per attempt:** `<invoice number>-<attempt>`.
   - A retried job of the same attempt finds its own charge instead of
     making a second one.
   - Paymob refuses a reused `special_reference`, so a reference is never
     reused across attempts.
4. **Configuration.** The platform's accounts are configured per
   currency: `BILLING_PAYMOB_<EGP|SAR|AED>_{SECRET_KEY,API_KEY,MOTO_INTEGRATION_ID}`.
   - All three are required, or none for that currency.
   - A currency without an account cannot be charged: its charge job
     fails and retries, visibly, rather than counting a decline against
     the merchant.

## Consequences

- **No duplicated Paymob client.** The two adapters differ only where
  their jobs differ: stores' checkouts and refunds, the platform's MOTO
  charges.
- **Unverified against Paymob.** The contract is tested against a
  stand-in modelled on the collection, including a lost answer that
  must not charge twice. Run it against Paymob's test mode before
  launch, with the MOTO integration Paymob issues.
- **Still to come:**
  - saving the card (the 3DS checkout and the `TOKEN` callback);
  - the merchant's "pay now" page;
  - the owner's WhatsApp notice.

  These are 7d-2b and 7d-2c. Until a card can be saved, keep
  `BILLING_COLLECTION_ENABLED` off.
