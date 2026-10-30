# ADR-0047: Plan features are checked where they are used

- Status: Accepted
- Date: 2026-10-30

## Context

ADR-0046 enforced the limits a plan counts. P7 7b-2 enforces its four
on/off features:
- card payments;
- coupons;
- ad pixels with server-side conversions;
- product feeds.

These features differ from counts in one way: what the merchant set up
keeps working by itself after the plan stops including it. A trial ends,
or a store moves to a smaller plan, and these keep running:
- a connected gateway;
- a saved coupon code;
- a pixel;
- a feed URL.

Refusing only new set-ups would leave these on for free.

## Decision

1. **Setting up is refused** with the same 403 `plan_limit` as a limit
   (`entitlements.Require`, which returns `NotInPlan`):

   | Feature | Refused request |
   |---|---|
   | `card_payments` | `connectPaymentGateway` |
   | `coupons` | `createCoupon` |
   | `pixels` | `savePixel` |
   | `feeds` | `renewProductFeedKey` (turning feeds on) |

   Turning things off, deleting and reading stay allowed, so a merchant
   can always clean up.
2. **Using is checked each time** (`entitlements.Allowed`), and what the
   merchant saved is kept, unused:

   | Feature | Where | Without the feature |
   |---|---|---|
   | `card_payments` | `CardEnabled` (storefront checkout), `StartCheckout` | No card option; a card order is not sent to the gateway (`card_payments_unavailable`) |
   | `coupons` | `Preview` (the storefront's quote, and every order before it redeems) | The code does not exist for the shopper (`not_found`) |
   | `pixels` | `Browser` (what pages load), `Track` (server conversions) | Pages load no pixel; no conversion is enqueued |
   | `feeds` | `FeedAllowed` (the storefront asks before serving) | The feed URL opens nothing (404) |

   Each check is a cached read (ADR-0045 §4): it costs no query on a hit.
3. **Coupons are checked at `Preview`, not again inside `RedeemTx`.**
   - Orders always preview a code before they place the order.
   - A cache miss inside the order's transaction would take a second
     connection.
   - A plan change between the two is accepted: they are milliseconds
     apart.
4. **Plan changes must reach the storefront's caches.**
   - Whether checkout offers cards, and the pixels a page loads, are in
     the storefront's page caches and at the edge (ADR-0018).
   - Today those caches follow the features' own change events
     (`payments.connection.changed`, `marketing.pixels.changed`). A plan
     change is not one of those events.
   - The plan-change event that 7d and 7e add must therefore invalidate
     the storefront's caches and purge the edge, as well as the grants
     (ADR-0045 §4).
   - Until then, a page cached before a plan change can show the old
     state for up to its TTL. Checkout itself (`StartCheckout`,
     `Preview`) is checked live.

## Consequences

- **Nothing is lost on a downgrade.** Upgrading again brings back
  everything that was set up: the gateway, the coupons, the pixels and
  the feed key.
- **A shopper on a lapsed store never pays by card and never gets a
  discount** the merchant no longer pays for. The cost is a few cached
  checks on the storefront's path.
- **Modules built without entitlements** (their own tests, `sfdemo`)
  allow every feature (`entitlements.Unrestricted`).
