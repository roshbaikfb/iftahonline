# ADR-0075: The console's monthly recurring revenue and new stores

- Status: Accepted
- Date: 2026-11-03

## Context

The console's home kept a slot for revenue and growth until the server
could compute them. The owner approved the plan on 2026-11-03.

- **The figures:** monthly recurring revenue per currency, and new
  stores in the last 7 and 30 days, with a bar per day.
- **The owner's decision:** a past-due subscription is not counted until
  it pays.

"Revenue" needs one meaning that matches what invoices charge:

- Founding members pay less than the list price.
- An agency pays one flat price for many stores.
- A retired plan renews into the version on sale.

## Decision

### Monthly recurring revenue

Revenue is summed per currency, never converted. It covers every
subscription with status `active` whose plan is a subscription or an
agency, and that is not set to cancel at the period's end. Each one
counts at what its next renewal will be charged:

- **Plan and cycle:** the plan and cycle it renews into (`next_plan_id`,
  `next_cycle`). A retired plan counts as the version on sale.
- **Currency:** its account's currency, or its store's before its first
  invoice. `RenewalDue` resolves the plan and currency the same way.
- **Founding members:** a member who keeps the program counts at
  `Member.Renewal` less its discount, the same code the invoice uses.
- **Annual:** an annual price counts as its month's share, divided by 12
  and rounded half up.
- **Agencies:** an agency's flat price counts once per agency account.
- **Tax:** before VAT.

Not counted:

- trials;
- past-due subscriptions (the owner's decision);
- suspended and cancelled subscriptions;
- subscriptions set to cancel at the period's end;
- the free plan;
- pay-as-you-sell fees, which are commission, not recurring revenue.

### Where the work happens

- **Billing:** `app.billing_paying()`, a `SECURITY DEFINER` function in
  the style of `app.billing_health()`, returns one row per paying
  subscription: price, kind, account and founding terms. It returns
  prices only. Go sums them in `domain.MRR`, so the founding rules are
  not written twice.
- **Tenancy:** counts the stores per Cairo day for the last 30 days. A
  new index, `stores_created_at_idx` on `(created_at DESC, id DESC)`,
  serves that count and the console's directory sort.
- **Admin:** `GET /admin/v1/metrics` reads both under the health page's
  rules:
  - each section has its own timeout;
  - a section that fails is named in `errors`;
  - the endpoint still answers.
- **The console:** reads the figures when the home opens and on
  «تحديث الآن», not every minute like the health check. The «اشتراكات
  مدفوعة» tile now counts these paying accounts. The health check's
  `active` count included stores on the free plan.

### Cost

Measured on 5,000 stores (4,092 paying subscriptions, 50 agencies, 100
founding members):

- `app.billing_paying()` runs in about 16 ms. Folding the price into
  the plan lookup cut it from about 97 ms.
- The new-stores count runs in 0.25 ms, using the new index.

The figures are read only by the console, on demand.

## Consequences

- The figure is a run rate of the next renewals, not money collected.
  Collected revenue would come from paid invoices, and would be a
  separate figure.
- If the invoice's renewal pricing changes, `app.billing_paying()` must
  change with it. `TestPaying` covers each rule on real PostgreSQL.
