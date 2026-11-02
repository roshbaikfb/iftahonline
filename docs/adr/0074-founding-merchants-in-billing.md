# ADR-0074: The founding-merchants program, in billing

- Status: Accepted
- Date: 2026-11-02

## Context

The owner approved a founding-merchants program (2026-11-02):

- **Who:** the first paying merchants on a monthly or annual plan (100
  by default).
- **What they keep while subscribed:**
  - a discount for life (20% by default);
  - their price, if prices rise.
- **How membership is lost:** by cancelling, or by leaving a renewal
  unpaid more than 30 days. The seat is not given again.
- **Limits:** one store per merchant; pay-as-you-sell is not part of it.
- **Where it shows:** a real seat counter on the site, and a badge in
  the merchant's dashboard.
- **Who runs it:** the console turns it on and sets the seats and the
  discount.

Two parts of billing did not support this:

- Renewals were invoiced at the newest price of the plan, so a price
  lock needed a change to how invoices are built.
- No invoice line could hold a discount.

## Decision

1. **Billing owns the program.**
   - `founding_program` is one platform row, written by the console as
     `app_admin` (`billing.NewFounding`).
   - `founding_members` belongs to the stores, with RLS on `store_id`.
     A row keeps the store's owner, its seat, the discount it joined
     with, and its locked plan, cycle and price.
   - What crosses stores runs in `SECURITY DEFINER` functions:
     - `app.founding_open`: the discount a merchant gets now;
     - `app.founding_join`: takes a seat, under an advisory lock;
     - `app.founding_sweep`: ends memberships;
     - `app.founding_list`: the console's member list;
     - `app.founding_seats`: the seat counter.
2. **The first period.**
   - A merchant subscribing to a monthly or annual plan while seats are
     left, whose owner has never joined, pays the first invoice with the
     discount as its own line.
   - The store takes the next seat when that invoice is paid, never
     before.
   - If the seats ran out meanwhile, the store keeps that first
     discount but doesn't join.
3. **Renewals.**
   - A member renewing the same plan and cycle pays the lower of its
     locked price and the current one, then its discount, as a
     `founding_discount` line.
   - Renewing another plan or cycle (an upgrade) is charged that plan's
     current price, which becomes the new lock.
4. **Leaving.** `billing.founding_sweep` runs daily and ends a
   membership when:
   - the store's plan is no longer a subscription, or its subscription
     is closed (reason `cancelled`);
   - its renewal invoice has been open more than 30 days past due
     (reason `unpaid`).

   Seats are counted over every row, lost ones too, so a seat is never
   given again.
5. **One store per owner.**
   - Identity's `OwnerTx` gives billing the owner of the store in
     context. `founding_members.owner_id` is unique and covers lost
     members too.
6. **Invoice lines.**
   - `invoice_lines` accepts `founding_discount`, negative like a fee
     credit.
   - The checks were replaced `NOT VALID` (00083) and validated in
     00084 (expand, then validate).
7. **The console's member list** shows each store's ID with a link to
   its page. Reading every member's name would be one query per member
   (N+1).

## Consequences

- **Leaving takes up to a day to show:** a merchant who leaves keeps the
  discount until the next sweep. Their next invoice is still computed
  correctly, because the sweep runs well before invoices are due.
- **Changing the program's discount** affects only merchants who join
  after it.
- **S4b-2b, where merchants see it:**
  - **The site:** the home and pricing pages show the program while it
    is on with seats left. The section has its terms and a live count
    of seats left (`Showcase.FoundingSeats`, kept a minute, then the
    edge's 10 minutes). When the seats can't be read, the pages show
    without the section.
  - **The merchant:** a member sees «تاجر مؤسس», its seat and its
    discount on its billing page (`Billing.founding`). The badge goes
    when the membership ends, and stores never show it.
