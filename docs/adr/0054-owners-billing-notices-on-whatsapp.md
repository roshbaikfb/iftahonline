# ADR-0054: Owners' billing notices on WhatsApp

- Status: Accepted
- Date: 2026-10-30

## Context

The P7 spec asks for dunning "via WhatsApp/email". ADR-0051 emails a
store's owners at each step:
- payment failed;
- suspended;
- paid.

The platform's WhatsApp number (ADR-0038) only sends customer messages
today, and every customer message:
- costs a unit of the store's quota;
- is logged in the store's message log, whose `kind` enum is part of
  `/v1`;
- needs a template that Meta approved for the number.

A billing notice is the platform talking to its merchant. It should not
cost the merchant a message, and it does not belong in their customer
message log. Adding a message kind would also break `/v1`, as an enum
value added to a response.

## Decision

1. **`messaging.MerchantSender.SendMerchant(ctx, to, kind, locale,
   values)`** sends a merchant notice at once.
   - It goes on WhatsApp, from the platform's number, with one of three
     templates: `billing_payment_failed_v1`, `billing_store_suspended_v1`,
     `billing_payment_received_v1`.
   - Their texts are in `docs/modules/messaging.md` for Meta.
   - It is not counted in the quota and not logged. Its reference
     (`mn_<ULID>`) places none of WhatsApp's statuses, which are
     dropped.
   - Without WhatsApp configured, it sends nothing.
2. **The owners' phones** come from `identity.StaffPhones(db, roles…)`:
   verified phones only, of the store's members with the roles, in one
   query.
3. **Billing's notifier** (`cmd/platform`) emails first, then sends the
   template to each owner's phone. The values are the store's name, the
   invoice, its amount and the closing date.
   - **WhatsApp is best effort.** A failure is logged with the error
     redacted, never returned. The notice job therefore never retries
     because of WhatsApp, and never emails twice.
   - **No import cycle.** Billing does not import messaging (messaging
     imports entitlements, which imports billing); `cmd/platform` maps
     each notice to its template.

## Consequences

- **Owners hear on the channel they use**, at no cost to their quota,
  and their customer message log stays theirs.
- **Before launch,** the three templates need Meta's approval. Until
  then WhatsApp refuses them, the refusal is logged, and the email still
  goes.
- **An owner without a verified phone** gets the email only.
