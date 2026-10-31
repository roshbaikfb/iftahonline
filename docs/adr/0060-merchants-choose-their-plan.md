# ADR-0060: Merchants choose their plan

- Status: Accepted
- Date: 2026-10-31

## Context

Until now a merchant could see their billing and pay an open invoice, but
could not:
- choose a plan;
- start a trial;
- switch to annual billing;
- cancel, or take a cancellation back.

The user approved the rules on 2026-10-31 with the 7f mockups:
1. **A paid plan from the free plan, a trial or pay as you sell:** the
   first period is paid at once on Paymob's page. The period starts
   when it is paid, and the card is saved for renewals.
2. **Between paid plans, or a change of cycle:** applies at the next
   renewal, with no proration.
3. **Pay as you sell or the free plan:**
   - from a paid plan, at the end of the paid period;
   - from the free plan or a trial, at once.

Two facts of the existing billing shape the design:
- **Only one invoice per account and month.** `invoices_account_period_key`
  allows a single invoice per account per month, but a first period may
  be paid in a month that also has a monthly invoice.
- **New invoices are charged at once.** A new invoice's first charge is
  due on issue (`next_attempt_at`, ADR-0051). A first period without a
  card would fail and push the store into dunning.

## Decision

1. **`domain.Subscription.Choose` decides** what choosing plan p (and
   cycle c) does at a given time:

   | Action | When |
   |---|---|
   | `apply` | The free plan or pay as you sell, from the free plan, a trial or pay as you sell |
   | `pay` | A paid plan, from the free plan, a trial or pay as you sell |
   | `schedule` | During a paid period: another paid plan or cycle at the renewal, or pay as you sell or the free plan after it |

   - **Taking a change back.** Choosing the current plan and cycle again
     takes a scheduled change back.
   - **Refusals.**
     - Agency plans stay the console's (ADR-0059).
     - A past-due, suspended or cancelled store, or a store whose
       renewal is billed but unpaid, settles first (409
       `settle_first`); the price and plan of a billed renewal are
       fixed.
2. **Scheduled changes** live on the subscription (migration 00067):
   - `next_plan_id` and `next_cycle`. `RenewalDue` prices the renewal
     from them (the newest version of the next plan's code, ADR-0056),
     and `Settle` applies the cycle and clears them.
   - Leaving for pay as you sell is a cancellation with `next_plan_id`
     set. `EndPeriod` moves the store to that plan instead of the free
     plan.
3. **First-period invoices** are invoices with `purpose = 'subscribe'`:
   - **Fields:** `plan_id`, `cycle`, `for_store_id`.
   - **Not charged automatically.** They have no next attempt, so
     dunning never charges them; the merchant pays on Paymob's page
     (ADR-0053).
   - **Payment starts the period.** Paying one starts the store's period
     of that plan and cycle at the payment.
   - **A failed card changes nothing,** because nothing was owed yet.
   - **A new choice voids** an earlier unpaid one.
   - **Uniqueness:** the monthly invoice keeps one per account and month
     as a partial unique index (`invoices_monthly_key`), created before
     the old constraint is dropped.
   - **The account:** a store with no account gets one when it first
     subscribes.
4. **The API** (tag `billing`, permission `billing.manage`, owner only):
   - `GET …/billing/plans`: the public plans on sale in the store's
     currency (prices per cycle, pay as you sell's fee, grants, whether
     each is current, whether a trial is available) and the features
     catalog that names them;
   - `POST …/billing/subscription`: `{plan_code, cycle}` returns the
     action and, for `pay`, the checkout URL;
   - `POST …/billing/trial`, `…/cancel`, `…/resume`.
   - `Billing` gains `next_plan_name_ar` and `_en`, and `next_cycle`.

## Consequences

- **A trial ends on payment.** Paying during a trial starts the paid
  period at once, and the trial's remaining days are not added.
- **An abandoned checkout leaves an open invoice.** It is a first-period
  invoice that is never charged. It is voided by the next choice, and it
  blocks joining an agency until it is settled or replaced.
- **Changes are not notified in advance.** A price change from a new
  plan version still reaches a paid store at renewal without notice
  (ADR-0056). The billing page shows the scheduled change.
