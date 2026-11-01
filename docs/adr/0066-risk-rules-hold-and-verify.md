# ADR-0066: Risk rules: hold or verify risky customers' orders

- Status: Accepted; amended by ADR-0070 (what the customer is told)
- Date: 2026-11-01

## Context

ADR-0065 gives each customer's phone a band. P8 also asks for per-store
rules: prepaid shipping for high risk, auto-hold, and extra confirmation.
The owner approved these choices (2026-11-01):

- **Extra confirmation.** The confirmation message is sent even where the
  store turned confirmations off, and the customer's "yes" does not
  confirm the order: the merchant calls first.
- **Plan feature.** The rules are a plan feature (`risk_rules`); the bands
  stay free for everyone.
- **Prepaid shipping** comes separately (8d), because it changes how
  orders are paid.

Constraints:

- **Orders cannot import risk.** Risk reads orders through
  `orders.Outcomes`, so the dependency runs the other way. Orders asks
  through a port that `cmd/platform` adapts, as it does for shipping and
  promotions.
- **Placement is the storefront's hot path.** No external call is allowed
  there (CLAUDE.md).
- **Card orders** are paid upfront, and payments confirm them on their
  own (ADR-0029). A hold there would block that confirmation.

## Decision

1. **The rules** (`risk_settings`, migration 00075, a tenant table under
   RLS).
   - Each store has two rules: `hold_from` and `verify_from`. Each is
     `off`, `medium` or `high`. A store without a row has both off.
   - `domain.Rules.Decide(band)` checks the hold first, then the
     verification.
   - A `new` customer is never held or verified: no history is no risk.
   - **API** (tag `risk`):
     - `getRiskSettings` needs orders.read. It returns the rules, plus
       `available`: whether the plan includes them.
     - `updateRiskSettings` needs orders.manage and is audited
       (`risk.rules_updated`).
     - Turning a rule on needs `risk_rules` (403 `plan_limit`). Turning
       them off never does.
   - **Plan feature `risk_rules`** (a flag) is seeded for every paid plan
     and not for the free plan. The console can change it like any
     feature (ADR-0056). A store whose plan loses it keeps its rules
     saved, but they do nothing until the plan has them again.
2. **At placement** (`orders.RiskCheck`, adapted from
   `risk.NewChecker`).
   - **Which orders.** Only storefront cash-on-delivery orders are
     checked. Staff who place an order have talked to the customer, and
     card orders are paid upfront.
   - **Cost.** One tenant transaction, two queries: the phone's signals
     (`app.risk_signals`) and the rules. When a rule fires, the plan check
     uses the entitlements cache.
   - **What it records.** The order keeps the verdict: `risk_action`
     (`hold`, `verify`, later `released`) and `risk_band`, the band at
     that moment (migration 00074, expand only). Both are in the API.
   - **Failure.** A check that fails, fails the placement, like any other
     database error on that path. Skipping the rules silently would let
     risky orders through. *Amended 2026-11-01 (P8 review, the owner's
     decision):* the rules fail open. A check that errs, or takes longer
     than `RiskTimeout` (300 ms), places the order with no action and logs
     a warning; a card check that errs holds a prepaid delivery instead.
     Losing a real order costs the merchant more than one unchecked
     order, which still waits for the merchant to confirm it.
   - **No dependency cycle.** `risk.NewChecker` needs only the pool, the
     phone key and the plan, not orders. So the orders module can be
     built with it, in every role that places orders (api, storefront,
     worker).
3. **Hold.**
   - A held order gets no confirmation message (messaging's
     `ConfirmOrder` skips it), so it also has no auto-cancel deadline.
   - It cannot be confirmed by anyone (`409 order_held`) until
     `releaseOrder` lifts the hold. That needs orders.manage, writes a
     timeline note, is audited (`order.released`), and sets
     `risk_action = released`.
   - Releasing an order that is not held, or no longer pending, is
     `409 order_not_held`.
   - Cancelling a held order needs no release.
4. **Verify.**
   - The confirmation message is sent even where the store turned
     confirmations off.
   - The customer's own "yes" (any `customer:*` actor) cannot confirm the
     order. Orders refuses it with `ErrNeedsCall`.
   - **Messaging keeps the answer.** The confirmation is `confirmed`, the
     order stays pending, and the merchant's `order_confirmed` notice
     carries `needs_call: true`. That is a new optional field rather than
     a new kind, so the notification kind enum, a response enum, does
     not change (no breaking change in /v1).
   - The customer's "no" cancels the order as usual. The auto-cancel
     deadline still applies to an unanswered message.
   - Staff confirm a verified order normally, after the call.
5. **Dashboard.**
   - The messages page has a "حماية الدفع عند الاستلام" card with the two
     rules. It is shown disabled, with a link to the plans, when the plan
     lacks them.
   - A held order shows why it is held and a "فك الحجز" button.
   - A verified order reminds the merchant to call.
   - The notice of a verified "yes" reads "وافق العميل على الطلب: اتصل به
     للتحقق قبل التأكيد".

## Consequences

- **Positive:**
  - Merchants stop shipping to customers who usually refuse, without
    refusing those customers' orders.
  - Every rule decision is visible on the order (`risk_action`,
    `risk_band`) and can be undone (release, or staff confirming).
  - The band at placement is kept for analytics (8e).
- **Negative:**
  - **Two more queries on every storefront cash order,** even in stores
    without rules. The band snapshot is worth it, and both use indexes.
  - **A held order waits for a person.** A merchant who never looks
    keeps it pending; it is never cancelled automatically.
  - **Verification costs a message** in stores that turned confirmations
    off.
  - **A downgraded plan stops the rules silently** at placement. The
    settings page says so (`available: false`).
