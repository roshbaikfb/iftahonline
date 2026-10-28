# ADR-0041: Customers confirm on WhatsApp first, and are thanked

- Status: Accepted
- Date: 2026-10-28

## Context

The order confirmation (ADR-0040) sends a WhatsApp template. Meta
charges for a template that opens a conversation. A text the business
sends inside a conversation the customer opened is free for 24 hours.

The owner approved (2026-10-28) a cheaper path first:
- the thank-you page offers a "confirm on WhatsApp" button;
- the button opens a chat with the platform's number, with a ready text;
- the customer sends it, and the order is confirmed.

The paid template goes out only to customers who do not. The owner also
approved thanking the customer after they answer.

Every store shares the platform's one number (ADR-0038). The customer's
text carries nothing we signed. It must still name its order, and only
that order's customer may confirm it.

## Decision

1. **A WhatsApp code per confirmation.** When the platform has a number
   (`WHATSAPP_DISPLAY_NUMBER`) and a WhatsApp channel, each confirmation
   gets a code: 8 characters of the Crockford alphabet, 40 random bits.
   - The code is unique across stores, with a partial unique index on
     `order_confirmations.wa_code`. A clash with another store's code is
     redrawn, inside a savepoint.
   - `app.confirmation_store(code)` is `SECURITY DEFINER`. It gives
     `app_user` the store of a code, and nothing else, using a read-only
     policy for the table's owner, as `app.resolve_host` does for hosts.
2. **The thank-you page** shows the button for a cash-on-delivery order
   still waiting for its answer. The link is `https://wa.me/<number>`
   with the text «أؤكد طلبي رقم <code>» (or "I confirm my order <code>").
   The storefront role reads the number from its own config, and the
   link from `messaging.ConfirmLinks.WhatsAppLink`.
3. **The paid template waits** 10 minutes (`CustomerFirst`) when a code
   was issued.
   - When its time comes and the confirmation is no longer pending (the
     customer, or the merchant, settled it), it is not sent.
   - The message fails with `answered_first`, and its quota unit comes
     back.
4. **The customer's text** reaches the platform's webhook URL.
   - The provider finds the code in the text and places the event in
     the code's store. A text without a known code is not placed.
     Reading the code is forgiving about case, Arabic-Indic digits, and
     O/I/L read as 0/1, and the reader is fuzzed.
   - Processing finds the confirmation by the code in that store, and
     **requires the sender's number to be the order's phone**. Anyone
     may send a code; only the customer's own WhatsApp confirms. A
     mismatch changes nothing (`phone_mismatch`).
   - The order is then confirmed as by a button tap (actor
     `customer:whatsapp`). A text can only confirm; cancelling stays a
     button or the SMS link.
5. **Thanks.** A customer who answered on WhatsApp, by tap or text, gets
   a text in the conversation they opened: the order confirmed,
   cancelled, or no longer waiting.
   - This is the kind `order_answer_ack`. It costs no quota unit, goes
     by WhatsApp only (never SMS), and is sent once, when the answer
     settled the confirmation.
   - Answers on the SMS link get the page instead.

6. **`MessageKind` becomes an extensible list** (ADR-0032's rule).
   `order_answer_ack` appears in the message log (`listMessages`), so the
   gate counts a new value in a response as breaking. It is accepted by
   one line in `api/openapi-breaking-ignore.txt`. Clients must show a
   kind they do not know as a message, and the description says so.
   Today the only client is our dashboard, which is generated from the
   same file and does not show the log yet.
   - The SMS text endpoints keep their own kind, `MessageTextKind`
     (`order_confirmation` only), so their contract does not change.

## Consequences

- An order whose customer confirms in the first 10 minutes costs no
  paid message. Every other customer gets the template 10 minutes later
  than before.
- A customer's text, with its sender's number, is stored in the store's
  webhook inbox for 90 days, like every webhook body (ADR-0024). Texts
  with no known code are not stored.
- Without `WHATSAPP_DISPLAY_NUMBER`, nothing changes from ADR-0040: no
  codes, no button, no wait.
- The thanks rely on the customer's open conversation. If WhatsApp
  refuses the text, it fails quietly in the log.
