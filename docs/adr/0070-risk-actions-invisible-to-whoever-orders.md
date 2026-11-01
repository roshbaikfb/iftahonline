# ADR-0070: What the risk rules do never shows to whoever places the order

- Status: Accepted
- Date: 2026-11-01
- Amends: ADR-0066 (the customer's yes), ADR-0067 (decisions 3, 5, 6)

## Context

The P8 review found two problems with the risk rules (ADR-0066, ADR-0067).

1. **The customer of a verified order was told it is confirmed.** The
   rules want a verified order confirmed by phone, so the customer's yes
   leaves it pending. But the WhatsApp thanks and the SMS link's page
   still said «تم تأكيد طلبك».
2. **A typed phone's band could be read off the storefront.** Anyone can
   place a cash order with any phone number. What happened next showed
   the number's band, built from every store's delivery outcomes
   (ADR-0065), to that person:
   - The thank-you page asked a prepaid delivery to pay, showed its
     deposit, and redirected to the gateway.
   - `/v1/pay` opened the gateway for any order awaiting an online
     payment, and the thank-you page names the order.
   - The thank-you page's "confirm on WhatsApp" link was missing for a
     held or prepaid order, and present for a verified one where the
     store does not confirm.

   The band is meant for the store's own staff only. Shown on a public
   page, it tells anyone how a stranger's past deliveries went.

The owner chose (2026-11-01) to keep the prepaid delivery tied to the
band, but to send the request to pay to the phone itself, never to the
person who filled in the form.

## Decision

1. **A yes that leaves the order pending says so.**
   - The WhatsApp thanks (`AckText` outcome `awaiting_call`) and the SMS
     link's page (state `awaiting_call`) say «وصلنا ردك، وسيتصل بك المتجر
     لتأكيد الطلب».
   - Both read the order's own status: a yes followed by a still-pending
     order is never called confirmed.
   - Once the store confirms on the call, the page says the order is
     confirmed.
2. **The link to pay goes to the phone.**
   - A prepaid delivery gets a confirmation and a new message kind,
     `order_delivery_prepay`: WhatsApp template `order_delivery_prepay_v1`
     (the link in `{{5}}`), or the platform's SMS text.
   - It is sent whether or not the store confirms, and costs a quota unit
     like a confirmation.
   - The link is the SMS link's page (`/o/<token>`, 128 random bits). For
     a prepaid delivery it shows the amount due now and on delivery, a pay
     button, and a cancel button.
   - Paying confirms the order, as before. The customer's own yes does
     not (`orders.ErrDepositDue`): the confirmation stays open, and the
     WhatsApp thanks says paying confirms it.
   - It has no confirmation deadline. Payments cancels it unpaid after
     six hours (`DepositExpireAfter`), not one: the link may be opened
     later than a shopper still at checkout.
3. **`/v1/pay` pays a deposit only with the link's token.**
   - The optional `t` parameter carries the token. The api role checks it
     against the order's confirmation (`messaging.NewPayLinks`, the
     `payments.DepositLinkCheck` port).
   - Without a valid token, a prepaid delivery redirects to `return`,
     exactly as any cash order does. `StartCheckout` pays card orders
     only; `StartDepositCheckout` pays deposits.
4. **One thank-you page for every cash order.**
   - It never shows the deposit or sends the shopper to pay.
     `PayOnline` is set for card orders only.
   - Where the store confirms, every cash order gets a confirmation. A
     held order's confirmation sends nothing and has no deadline, so each
     page offers the same WhatsApp link.
   - Where the store does not confirm, the page offers no link, not even
     for the extra confirmation the rules asked for. That order is still
     asked, by message, on the phone.
   - A test asserts a prepaid delivery's thank-you page is byte for byte
     a plain cash order's.
5. **A yes to a held order** (through that link) waits for the merchant,
   like a verified one. The thanks says the store will call.

## Consequences

- **Positive:**
  - The person who places an order learns nothing of the phone's band
    from the storefront or `/v1/pay`. Only the phone's owner sees the
    request to pay.
  - A customer is never told an order is confirmed when it is not.
  - The prepaid delivery keeps its purpose: the risky customer pays the
    delivery before the parcel leaves.
- **Negative:**
  - **A new WhatsApp template.** `order_delivery_prepay_v1` needs Meta's
    approval (docs/go-live.md §1). Until then a prepaid delivery's link
    goes by SMS, or is not sent without one; the order is then cancelled
    unpaid after six hours, and the order page tells the merchant.
  - **Some signal remains, but it is slow and costly.** Someone who
    tracks the order (code and phone) can still see it cancelled six
    hours later if nobody pays. A plain order in a store that confirms is
    also cancelled when unanswered; in one that does not, it is not.
    Probing that way costs one real order per number and hours of
    waiting, and the checkout's rate limits apply. We accept it.
  - **The order page's card** follows the pay link's message and says the
    order waits for its payment, not for an answer.
  - **A held order's confirmation** (where the store confirms) stays
    pending without a message until the merchant acts. It exists only
    so the thank-you page is the same.
- **Follow-up:** a store may later want an explicit rule that prepays by
  governorate or amount for everyone, shown openly at checkout. That
  would be a separate, visible policy, not a band.
